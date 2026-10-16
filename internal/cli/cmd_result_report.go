package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

const (
	reportFlag = "--report"
	// reportNotTerminal is the exit of a report asked for a run that is still
	// going; it shares the number of a wait that timed out.
	reportNotTerminal = ExitWaitTimeout
	// noModel names the model of a run that recorded no model at all.
	noModel = "default"
	// reportNoPurpose names the purpose of a run whose request recorded none.
	reportNoPurpose = "adhoc"
	runsDirName     = "runs"
)

// tagStart matches a "<" that opens a tag-shaped token.
var tagStart = regexp.MustCompile(`<(/?[A-Za-z][A-Za-z0-9_-]*)`)

// runResultReport prints the delivery report of a terminal run: one block that
// says what ran and how it ended, followed by its output with markup made inert.
func runResultReport(ctx *Context, args []string) int {
	if len(args) != 1 {
		return ctx.Fail(ExitUsage, "usage: msgate result <run_id | output path> %s", reportFlag)
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	runID, ok := reportRunID(args[0])
	if !ok {
		return ctx.Fail(ExitNotFound, "no run %q", args[0])
	}
	state, code := loadRun(ctx, st, runID)
	if code != 0 {
		return code
	}
	if !isTerminalState(state.State) {
		return ctx.Fail(reportNotTerminal, "run %s is still %s; wait for it with: msgate wait %s", state.RunID, state.State, state.RunID)
	}
	output, err := os.ReadFile(st.OutputPath(runID))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctx.Fail(ExitInternal, "opening the output: %v", err)
	}
	fmt.Fprint(ctx.Stdout, renderReport(reportOf(ctx, st, state), string(output)))
	return ExitOK
}

// reportRunID names the run a target points at. A bare target is a run id. A
// path names its run by the run directory it lies in, the directory under a
// "runs" directory, wherever the home then was.
func reportRunID(target string) (string, bool) {
	if !strings.ContainsRune(target, filepath.Separator) {
		return target, true
	}
	for p := filepath.Clean(target); p != filepath.Dir(p); p = filepath.Dir(p) {
		if filepath.Base(filepath.Dir(p)) == runsDirName {
			return filepath.Base(p), true
		}
	}
	return "", false
}

// report is what the delivery report says about a run.
type report struct {
	runID, conversationID string
	purpose, model        string
	outcome, excerpt      string
	delivered             bool
	outputPath            string
}

func reportOf(ctx *Context, st *store.Store, state store.State) report {
	var req requestRecord
	if err := st.ReadRequest(state.RunID, &req); err != nil && !errors.Is(err, fs.ErrNotExist) {
		ctx.Warnf("could not read the request of run %s: %v", state.RunID, err)
	}
	recorded := recordedRun(ctx, st, state)
	r := report{
		runID: state.RunID, conversationID: state.ConversationID, outcome: state.State,
		purpose:    firstNonEmpty(req.Purpose, stringField(recorded, "purpose"), reportNoPurpose),
		model:      firstNonEmpty(stringField(recorded, "model_used"), derefString(req.Model), noModel),
		outputPath: st.OutputPath(state.RunID),
	}
	if state.Outcome != nil {
		r.outcome = *state.Outcome
	}
	r.delivered = deliversOutput(r.outcome)
	if state.ErrorExcerpt != nil {
		r.excerpt = *state.ErrorExcerpt
	}
	return r
}

// deliversOutput says whether a run that ended with this outcome produced an
// answer: a run that failed, timed out, was cancelled, was lost or wrote
// nothing did not.
func deliversOutput(outcome string) bool {
	switch outcome {
	case "error", "timeout", "cancelled", "lost", "empty":
		return false
	}
	return true
}

// recordedRun is the run's telemetry record, which holds what the provider
// reported using; nil when there is none or it cannot be read.
func recordedRun(ctx *Context, st *store.Store, state store.State) map[string]any {
	var since *time.Time
	if at, err := time.Parse(time.RFC3339, state.AdmittedAt); err == nil {
		since = &at
	}
	folded, err := telemetry.Fold(st.Home, since)
	if err != nil {
		ctx.Warnf("could not read the telemetry of run %s: %v", state.RunID, err)
		return nil
	}
	for _, run := range folded.Runs {
		if id, _ := run["run_id"].(string); id == state.RunID {
			return run
		}
	}
	return nil
}

func stringField(rec map[string]any, key string) string {
	s, _ := rec[key].(string)
	return s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// renderReport lays out the report: the heading, how the run ended when it
// delivered nothing, the output, then the run's trailer and where the full
// output lives.
func renderReport(r report, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## msgate report · %s · %s · %s\n", escapeTags(r.purpose), escapeTags(r.model), r.outcome)
	if !r.delivered {
		b.WriteString(endedLine(r) + "\n")
	}
	b.WriteString("\n")
	if output != "" {
		b.WriteString(escapeTags(strings.TrimRight(output, "\n")))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "[msgate run %s · conversation %s · %s]\nFull output: %s\n", r.runID, r.conversationID, r.outcome, r.outputPath)
	return b.String()
}

func endedLine(r report) string {
	if r.excerpt == "" {
		return "ended " + r.outcome
	}
	return "ended " + r.outcome + ": " + escapeTags(r.excerpt)
}

// escapeTags writes the "<" that opens a tag-shaped token as "&lt;", so markup
// in text a run produced cannot act as markup where the report is read.
func escapeTags(text string) string { return tagStart.ReplaceAllString(text, "&lt;$1") }

// withoutFlag removes every occurrence of a bare flag and says whether it was there.
func withoutFlag(args []string, flag string) (rest []string, found bool) {
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}
