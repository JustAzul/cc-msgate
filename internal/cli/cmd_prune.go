package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "prune", Summary: "remove the run directories of old finished runs", Run: runPrune})
}

// defaultRetentionDays is how old a finished run must be to be pruned when
// neither --older-than nor MSGATE_RETENTION_DAYS says otherwise.
const defaultRetentionDays = 30

// retentionDays is a non-negative whole number of days.
type retentionDays struct {
	days *int
	set  *bool
}

func (f retentionDays) String() string { return "" }

func (f retentionDays) Set(raw string) error {
	n, err := parseDays(raw)
	if err != nil {
		return fmt.Errorf("--older-than needs a whole number of days, 0 or more, got %q", raw)
	}
	*f.days, *f.set = n, true
	return nil
}

func parseDays(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("not a whole number of days")
	}
	return n, nil
}

// pruneReport is what prune prints: one JSON object with --json, else a line.
type pruneReport struct {
	SDKStatus     string `json:"sdk_status"`
	ExitCode      int    `json:"exit_code"`
	Error         string `json:"error,omitempty"`
	Ran           bool   `json:"ran"`
	DryRun        bool   `json:"dry_run"`
	OlderThanDays int    `json:"older_than_days"`
	Removed       int    `json:"removed"`
	Kept          int    `json:"kept"`
	Skipped       int    `json:"skipped"`
	Failed        int    `json:"failed"`
	BytesFreed    int64  `json:"bytes_freed"`
	StoppedEarly  bool   `json:"stopped_early"`
}

func runPrune(ctx *Context, args []string) int {
	var asJSON, dryRun, auto bool
	var days int
	var daysGiven bool
	fset := flag.NewFlagSet("prune", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.BoolVar(&dryRun, "dry-run", false, "count what would be removed and remove nothing")
	fset.BoolVar(&auto, "auto", false, "prune at most once a day, within a short time budget, and say nothing unless it fails")
	fset.Var(retentionDays{&days, &daysGiven}, "older-than", "remove runs that ended more than this many days ago")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, "usage: msgate prune [--older-than <days>] [--dry-run] [--auto] [--json]")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	if len(positional) != 0 {
		return ctx.Fail(ExitUsage, "prune takes no arguments")
	}
	if !daysGiven {
		if days, err = envRetentionDays(ctx.Getenv); err != nil {
			return ctx.Fail(ExitUsage, "%v", err)
		}
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	if auto {
		return autoPrune(ctx, store.Open(home), days, dryRun)
	}
	return pruneHome(ctx, store.Open(home), days, dryRun, 0, false)
}

// envRetentionDays is MSGATE_RETENTION_DAYS, or the default when it is unset.
func envRetentionDays(getenv func(string) string) (int, error) {
	raw := getenv("MSGATE_RETENTION_DAYS")
	if raw == "" {
		return defaultRetentionDays, nil
	}
	n, err := parseDays(raw)
	if err != nil {
		return 0, fmt.Errorf("MSGATE_RETENTION_DAYS must be a whole number of days, 0 or more, got %q", raw)
	}
	return n, nil
}

// pruneHome prunes the store and prints the report. A budget of 0 is no bound.
func pruneHome(ctx *Context, st *store.Store, days int, dryRun bool, budget time.Duration, quiet bool) int {
	opts := store.PruneOptions{
		OlderThan: time.Duration(days) * 24 * time.Hour, Now: ctx.Now, DryRun: dryRun, Budget: budget,
		Warn: func(msg string) { ctx.Warnf("%s", msg) },
	}
	if step, ok := envMillis(ctx.Getenv, "MSGATE_TEST_PRUNE_STEP_MS"); ok {
		opts.AfterRun = func(string) { time.Sleep(step) } // makes a run's handling slow enough to outlast a short budget
	}
	res, err := st.Prune(opts)
	if err != nil {
		return ctx.Fail(ExitInternal, "pruning: %v", err)
	}
	rep := pruneReport{
		SDKStatus: "ok", Ran: true, DryRun: dryRun, OlderThanDays: days,
		Removed: res.Removed, Kept: res.Kept, Skipped: res.Skipped, Failed: res.Failed,
		BytesFreed: res.BytesFreed, StoppedEarly: res.Stopped,
	}
	if res.Failed > 0 {
		rep.SDKStatus, rep.ExitCode = sdkStatusFor(ExitInternal), ExitInternal
		rep.Error = fmt.Sprintf("%d run(s) could not be removed", res.Failed)
	}
	return printPrune(ctx, rep, quiet)
}

// printPrune prints the report. A quiet report is printed only with --json or
// when the pass failed.
func printPrune(ctx *Context, rep pruneReport, quiet bool) int {
	if quiet && !ctx.JSON && rep.ExitCode == 0 {
		return 0
	}
	if ctx.JSON {
		if printJSON(ctx, rep) != 0 {
			return ExitInternal
		}
		return rep.ExitCode
	}
	remove, free := "removed", "freed"
	if rep.DryRun {
		remove, free = "would remove", "would free"
	}
	fmt.Fprintf(ctx.Stdout, "%s %d, kept %d, skipped %d, %s %d bytes\n", remove, rep.Removed, rep.Kept, rep.Skipped, free, rep.BytesFreed)
	if rep.Error != "" {
		ctx.Errorf("%s", rep.Error)
	}
	return rep.ExitCode
}

const (
	// autoPruneInterval is the least time between two automatic passes.
	autoPruneInterval = 24 * time.Hour
	// autoPruneBudget is how long an automatic pass may run before it stops
	// and leaves the rest to the next one.
	autoPruneBudget = 3 * time.Second
)

// autoPrune runs a pass when the opt-out is unset, the home exists, no other
// automatic pass is running and the last one started over a day ago. The
// stamp is written under the prune lock before the pass starts, so two
// sessions starting together prune once.
func autoPrune(ctx *Context, st *store.Store, days int, dryRun bool) int {
	skipped := pruneReport{SDKStatus: "ok", DryRun: dryRun, OlderThanDays: days}
	if autoPruneOff(ctx, st) {
		return printPrune(ctx, skipped, true)
	}
	unlock, err := st.LockPrune()
	if errors.Is(err, store.ErrPruneLockHeld) {
		return printPrune(ctx, skipped, true)
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "locking the prune: %v", err)
	}
	defer unlock()
	now := ctx.Now()
	if last, ok := st.PruneStamp(); ok && last.Before(now) && now.Sub(last) < autoPruneInterval {
		return printPrune(ctx, skipped, true)
	}
	if !dryRun {
		if err := st.WritePruneStamp(now); err != nil {
			return ctx.Fail(ExitInternal, "writing the prune stamp: %v", err)
		}
	}
	budget := autoPruneBudget
	if d, ok := envMillis(ctx.Getenv, "MSGATE_TEST_PRUNE_BUDGET_MS"); ok {
		budget = d
	}
	return pruneHome(ctx, st, days, dryRun, budget, true)
}

// autoPruneOff reports whether an automatic pass has nothing to do: the
// opt-out is set, or the home does not exist yet.
func autoPruneOff(ctx *Context, st *store.Store) bool {
	if ctx.Getenv("MSGATE_NO_PRUNE") == "1" {
		return true
	}
	_, err := os.Stat(st.Home)
	return errors.Is(err, os.ErrNotExist)
}
