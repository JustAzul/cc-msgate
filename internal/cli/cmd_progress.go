package cli

import (
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "progress", Summary: "print what a run's provider has done so far", Run: runProgress})
}

// progressResult is what `progress --json` prints. A follower passes next as
// --from on its next call to read only what is new.
type progressResult struct {
	RunID   string                `json:"run_id"`
	State   string                `json:"state"`
	Next    int                   `json:"next"`
	Entries []store.ProgressEntry `json:"entries"`
	// Usage and CostUSD are what the run has used so far and what that costs
	// at the cached prices; null while the provider cannot say.
	Usage   *provider.Usage `json:"usage"`
	CostUSD *string         `json:"cost_usd"`
}

const progressUsage = "usage: msgate progress <run_id> [--from <n>] [--json]"

func runProgress(ctx *Context, args []string) int {
	var asJSON bool
	var from int
	fset := flag.NewFlagSet("progress", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.Var(fromFlag{&from}, "from", "skip the entries numbered below this")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if err != nil {
		return failFlagParse(ctx, progressUsage, err)
	}
	ctx.JSON = asJSON
	if len(positional) != 1 {
		return ctx.Fail(ExitUsage, progressUsage)
	}

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	state, code := loadRun(ctx, st, positional[0])
	if code != 0 {
		return code
	}
	entries, err := st.ReadProgress(state.RunID, from)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading the progress of run %s: %v", state.RunID, err)
	}

	if asJSON {
		live := liveReadingOf(ctx, st, state)
		result := progressResult{
			RunID: state.RunID, State: state.State, Next: nextSeq(entries, from), Entries: entries,
			Usage: live.Usage, CostUSD: live.CostUSD,
		}
		if printJSON(ctx, result) != 0 {
			return ExitInternal
		}
		return ExitOK
	}
	for _, e := range entries {
		fmt.Fprintf(ctx.Stdout, "%s: %s\n", e.Kind, e.Text)
	}
	return ExitOK
}

func nextSeq(entries []store.ProgressEntry, from int) int {
	if len(entries) == 0 {
		return from
	}
	return entries[len(entries)-1].Seq + 1
}

// fromFlag parses --from: a whole number, zero or more.
type fromFlag struct{ n *int }

func (f fromFlag) String() string { return "" }

func (f fromFlag) Set(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fmt.Errorf("--from needs a whole number, zero or more, got %q", raw)
	}
	*f.n = n
	return nil
}
