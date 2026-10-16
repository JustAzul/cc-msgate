package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "status", Summary: "show one run, or the session's active and recent runs", Run: runStatus})
}

// statusLimit is how many runs a listing shows.
const statusLimit = 20

func runStatus(ctx *Context, args []string) int {
	var asJSON bool
	var sessionID string
	fset := flag.NewFlagSet("status", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.StringVar(&sessionID, "session-id", ctx.Getenv("CLAUDE_CODE_SESSION_ID"), "Claude Code session id")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, "usage: msgate status [run_id] [--session-id <id>] [--json]")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	if len(positional) > 1 {
		return ctx.Fail(ExitUsage, "status takes at most one run id")
	}

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	if len(positional) == 1 {
		return showRun(ctx, st, positional[0], asJSON)
	}
	return listRuns(ctx, st, sessionID, asJSON)
}

func showRun(ctx *Context, st *store.Store, runID string, asJSON bool) int {
	state, code := loadRun(ctx, st, runID)
	if code != 0 {
		return code
	}
	return printRun(ctx, st, state, asJSON)
}

// printRun prints one run as status shows it.
func printRun(ctx *Context, st *store.Store, state store.State, asJSON bool) int {
	runID := state.RunID
	var req requestRecord
	if err := st.ReadRequest(runID, &req); err != nil && !errors.Is(err, os.ErrNotExist) {
		ctx.Warnf("could not read the request of run %s: %v", runID, err)
	}
	if !st.RunExists(runID) {
		return ctx.Fail(ExitNotFound, "no run %q", runID)
	}
	v := viewOf(state, req)
	if asJSON {
		return printJSON(ctx, v)
	}
	rows := [][2]string{
		{"run_id", v.RunID}, {"conversation_id", v.ConversationID}, {"turn", fmt.Sprint(v.Turn)},
		{"state", v.State}, {"kind", v.kind()}, {"purpose", v.Purpose}, {"admitted_at", v.AdmittedAt},
		{"output_path", v.OutputPath},
	}
	for _, r := range rows {
		fmt.Fprintf(ctx.Stdout, "%s: %s\n", r[0], r[1])
	}
	return ExitOK
}

func listRuns(ctx *Context, st *store.Store, sessionID string, asJSON bool) int {
	views, err := sessionRuns(ctx, st, sessionID)
	if err != nil {
		return ctx.Fail(ExitInternal, "listing runs: %v", err)
	}
	if asJSON {
		return printJSON(ctx, struct {
			Runs []runView `json:"runs"`
		}{views})
	}
	if len(views) == 0 {
		return ExitOK
	}
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tSTATE\tKIND\tPURPOSE\tCONVERSATION\tTURN\tADMITTED")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", v.RunID, v.State, v.kind(), v.Purpose, v.ConversationID, v.Turn, v.AdmittedAt)
	}
	tw.Flush()
	return ExitOK
}

// sessionRuns returns the most recent runs dispatched under the session id,
// newest first. It reads the session's index and opens only the runs it lists,
// stopping once a listing is full, so its cost does not depend on how many
// runs other sessions have. Ids whose run is gone are skipped; a session with
// no index has no runs.
func sessionRuns(ctx *Context, st *store.Store, sessionID string) ([]runView, error) {
	ids, err := st.SessionRunIDs(sessionID)
	if err != nil {
		return nil, err
	}
	views := []runView{}
	for _, id := range ids {
		if len(views) == statusLimit {
			break
		}
		state, err := observeRun(ctx, st, id)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				ctx.Warnf("skipping run %s: %v", id, err)
			}
			continue
		}
		var req requestRecord
		if err := st.ReadRequest(id, &req); err != nil {
			ctx.Warnf("skipping run %s: %v", id, err)
			continue
		}
		views = append(views, viewOf(state, req))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].AdmittedAt != views[j].AdmittedAt {
			return views[i].AdmittedAt > views[j].AdmittedAt
		}
		return views[i].RunID > views[j].RunID
	})
	return views, nil
}
