package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "stats", Summary: "summarize telemetry (runs by purpose and origin, usage, cost)", Run: runStats})
}

func runStats(ctx *Context, args []string) int {
	w, asJSON, exit, done := windowFlags(ctx, "stats", args)
	if done {
		return exit
	}
	ctx.JSON = asJSON
	folded, code := foldWindow(ctx, w)
	if code != 0 {
		return code
	}
	var days *int
	if !w.all {
		days = &w.days
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	stats := telemetry.ComputeStatsWithModelCalls(folded.Runs, folded.ModelCalls, folded.Skipped, days, readPriceCache(ctx, store.Open(home)))
	if asJSON {
		data, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			return ctx.Fail(ExitInternal, "encoding stats: %v", err)
		}
		fmt.Fprintf(ctx.Stdout, "%s\n", data)
		return ExitOK
	}
	printStatsSummary(ctx, stats, folded.Skipped)
	return ExitOK
}

// printStatsSummary writes the short human-readable form of the statistics.
func printStatsSummary(ctx *Context, stats *telemetry.Obj, skipped telemetry.Skipped) {
	window := "all history"
	if d, ok := stats.Get("window_days").(int64); ok {
		window = fmt.Sprintf("last %d days", d)
	}
	fmt.Fprintf(ctx.Stdout, "runs: %d (%s)\n", stats.Get("total"), window)
	if stats.Get("empty") == false {
		if span, ok := stats.Get("span").(*telemetry.Obj); ok {
			fmt.Fprintf(ctx.Stdout, "span: %v .. %v\n", span.Get("first"), span.Get("last"))
		}
		fmt.Fprintf(ctx.Stdout, "by source: %s\n", countsLine(stats.Get("by_source")))
		fmt.Fprintf(ctx.Stdout, "by provider: %s\n", countsLine(stats.Get("by_provider")))
		if d, ok := stats.Get("duration_ms").(*telemetry.Obj); ok && d.Get("count") != int64(0) {
			fmt.Fprintf(ctx.Stdout, "duration_ms: median %v (min %v, max %v)\n", d.Get("median"), d.Get("min"), d.Get("max"))
		}
	}
	printByPurpose(ctx, stats)
	tokenCounts := func(u *telemetry.Obj) string {
		return countsLine(withoutKey(u, "runs_with_usage", "cost_usd", "cost_complete"))
	}
	totals, _ := stats.Get("usage_totals").(*telemetry.Obj)
	byModel, _ := stats.Get("usage_by_model").(*telemetry.Obj)
	if totals != nil && totals.Get("runs_with_usage") != int64(0) {
		fmt.Fprintf(ctx.Stdout, "tokens: %s (%v runs with usage)\n", tokenCounts(totals), totals.Get("runs_with_usage"))
	}
	if byModel != nil {
		for _, p := range byModel.Pairs() {
			u := p.Value.(*telemetry.Obj)
			fmt.Fprintf(ctx.Stdout, "tokens %s: %s (%v runs)\n", p.Key, tokenCounts(u), u.Get("runs_with_usage"))
		}
	}
	printModelCalls(ctx, stats)
	printCosts(ctx, stats, totals, byModel)
	if n := skipped.Total(); n > 0 {
		fmt.Fprintf(ctx.Stdout, "skipped lines: %d (unknown_kind=%d unknown_version=%d unparseable=%d)\n",
			n, skipped.UnknownKind, skipped.UnknownVersion, skipped.Unparseable)
	}
}

// printByPurpose writes one line per purpose and origin: its runs, the count of
// each outcome and, when any run carries them, the findings sums.
func printByPurpose(ctx *Context, stats *telemetry.Obj) {
	byPurpose, _ := stats.Get("by_purpose").(*telemetry.Obj)
	if byPurpose == nil || len(byPurpose.Pairs()) == 0 {
		return
	}
	fmt.Fprintln(ctx.Stdout, "by purpose:")
	for _, purpose := range byPurpose.Pairs() {
		for _, origin := range purpose.Value.(*telemetry.Obj).Pairs() {
			bucket := origin.Value.(*telemetry.Obj)
			line := fmt.Sprintf("  %s %s: %s; outcomes %s", purpose.Key, origin.Key, plural(int(bucket.Get("runs").(int64)), "run"), countsLine(bucket.Get("outcomes")))
			if f, ok := bucket.Get("findings").(*telemetry.Obj); ok && f.Get("annotated") != int64(0) {
				line += "; findings " + countsLine(f)
			}
			fmt.Fprintln(ctx.Stdout, line)
		}
	}
}

// printModelCalls writes how many model calls each source made, when any did.
func printModelCalls(ctx *Context, stats *telemetry.Obj) {
	bySource, _ := stats.Get("model_calls_by_source").(*telemetry.Obj)
	if bySource == nil || len(bySource.Pairs()) == 0 {
		return
	}
	calls := &telemetry.Obj{}
	for _, p := range bySource.Pairs() {
		calls.Set(p.Key, p.Value.(*telemetry.Obj).Get("calls"))
	}
	fmt.Fprintf(ctx.Stdout, "model calls by source: %s\n", countsLine(calls))
}

// printCosts writes the session cost, then the cost of each priced model. A
// cost that is null prints no line.
func printCosts(ctx *Context, stats, totals, byModel *telemetry.Obj) {
	if totals == nil || byModel == nil {
		return
	}
	if cost, ok := totals.Get("cost_usd").(telemetry.Cost); ok {
		line := "cost: $" + cost.Cents()
		if totals.Get("cost_complete") == false {
			line += fmt.Sprintf(" (incomplete: %s)", strings.Join(unpricedModels(stats), ", "))
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
	for _, p := range byModel.Pairs() {
		if cost, ok := p.Value.(*telemetry.Obj).Get("cost_usd").(telemetry.Cost); ok {
			fmt.Fprintf(ctx.Stdout, "cost %s: $%s\n", p.Key, cost.Cents())
		}
	}
}

func unpricedModels(stats *telemetry.Obj) []string {
	models, _ := stats.Get("unpriced_models").([]string)
	return models
}

func withoutKey(o *telemetry.Obj, keys ...string) *telemetry.Obj {
	out := &telemetry.Obj{}
	for _, p := range o.Pairs() {
		if !slices.Contains(keys, p.Key) {
			out.Set(p.Key, p.Value)
		}
	}
	return out
}

// countsLine renders an object of counts as "key=n key=n".
func countsLine(v any) string {
	o, ok := v.(*telemetry.Obj)
	if !ok || len(o.Pairs()) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(o.Pairs()))
	for _, p := range o.Pairs() {
		parts = append(parts, fmt.Sprintf("%s=%v", p.Key, p.Value))
	}
	return strings.Join(parts, " ")
}
