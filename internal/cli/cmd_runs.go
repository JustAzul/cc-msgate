package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "runs", Summary: "print folded telemetry runs as JSONL", Run: runRuns})
}

const defaultWindowDays = 7

// window is the time range a reader command covers, and the one Claude Code
// session it is narrowed to when sessionID is set.
type window struct {
	all       bool
	days      int
	sessionID string
}

// since is the start of the window; nil reads everything.
func (w window) since(now time.Time) *time.Time {
	if w.all {
		return nil
	}
	t := now.UTC().Add(-time.Duration(w.days) * 24 * time.Hour)
	return &t
}

// windowFlags parses the flags the reader commands share (--days, --all,
// --session-id, --json) and no positionals. exit is meaningful when done is
// true.
func windowFlags(ctx *Context, name string, args []string) (w window, asJSON bool, exit int, done bool) {
	var all bool
	var sessionID string
	days := defaultWindowDays
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.IntVar(&days, "days", defaultWindowDays, "window size in days")
	fset.BoolVar(&all, "all", false, "read all history")
	fset.StringVar(&sessionID, "session-id", "", "only the runs of this Claude Code session")
	fset.BoolVar(&asJSON, "json", false, "print JSON")
	ctx.JSON = wantsJSON(args)
	usage := fmt.Sprintf("usage: msgate %s [--days N | --all] [--session-id ID] [--json]", name)
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(ctx.Stderr, usage)
			return w, false, ExitOK, true
		}
		return w, false, failFlagParse(ctx, usage, err), true
	}
	explicitDays := false
	fset.Visit(func(f *flag.Flag) { explicitDays = explicitDays || f.Name == "days" })
	switch {
	case fset.NArg() != 0:
		return w, false, ctx.Fail(ExitUsage, "%s takes no arguments, got %q", name, fset.Arg(0)), true
	case all && explicitDays:
		return w, false, ctx.Fail(ExitUsage, "--days and --all are mutually exclusive"), true
	case days < 0:
		return w, false, ctx.Fail(ExitUsage, "--days must not be negative"), true
	}
	return window{all: all, days: days, sessionID: sessionID}, asJSON, 0, false
}

// foldWindow reads and folds the telemetry of the window under the resolved home.
func foldWindow(ctx *Context, w window) (telemetry.Folded, int) {
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return telemetry.Folded{}, ctx.Fail(ExitInternal, "%v", err)
	}
	folded, err := telemetry.Fold(home, w.since(ctx.Now()))
	if err != nil {
		return telemetry.Folded{}, ctx.Fail(ExitInternal, "reading telemetry: %v", err)
	}
	if w.sessionID != "" {
		folded.Runs = telemetry.InSession(folded.Runs, w.sessionID)
		folded.ModelCalls = telemetry.InSession(folded.ModelCalls, w.sessionID)
	}
	return folded, 0
}

func runRuns(ctx *Context, args []string) int {
	w, asJSON, exit, done := windowFlags(ctx, "runs", args)
	if done {
		return exit
	}
	ctx.JSON = asJSON
	folded, code := foldWindow(ctx, w)
	if code != 0 {
		return code
	}
	for _, run := range folded.Runs {
		line, err := telemetry.MarshalRun(run)
		if err != nil {
			return ctx.Fail(ExitInternal, "encoding a run: %v", err)
		}
		fmt.Fprintf(ctx.Stdout, "%s\n", line)
	}
	if n := folded.Skipped.Total(); n > 0 {
		s := folded.Skipped
		fmt.Fprintf(ctx.Stderr, "msgate: skipped %d telemetry lines (unknown_kind=%d unknown_version=%d unparseable=%d)\n",
			n, s.UnknownKind, s.UnknownVersion, s.Unparseable)
	}
	return ExitOK
}
