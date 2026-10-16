package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "cancel", Summary: "ask a run to stop", Run: runCancel})
}

const cancelUsage = "usage: msgate cancel <run_id> [--json]"

func runCancel(ctx *Context, args []string) int {
	var asJSON bool
	fset := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, cancelUsage)
		return ExitOK
	}
	if err != nil {
		return failFlagParse(ctx, cancelUsage, err)
	}
	ctx.JSON = asJSON
	if len(positional) != 1 {
		return ctx.Fail(ExitUsage, "cancel takes exactly one run id")
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
	if !isTerminalState(state.State) {
		if state, code = requestCancel(ctx, st, state); code != 0 {
			return code
		}
	}
	return printRun(ctx, st, state, asJSON)
}

// requestCancel records the cancel request, then makes it take effect: a run
// that has not been taken by a worker is cancelled here; a running one is told
// to stop through its worker, which finalizes it. It returns the run as it
// stands afterwards.
func requestCancel(ctx *Context, st *store.Store, state store.State) (store.State, int) {
	runID := state.RunID
	if err := st.WriteCancelRequest(runID, ctx.Now()); err != nil {
		if !st.RunExists(runID) {
			return state, ctx.Fail(ExitNotFound, "no run %q", runID)
		}
		return state, ctx.Fail(ExitInternal, "recording the cancel request: %v", err)
	}
	wait := admissionWait(ctx.Getenv)
	deadline := time.Now().Add(wait)
	for {
		var err error
		if state, err = observeRun(ctx, st, runID); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return state, ctx.Fail(ExitNotFound, "no run %q", runID)
			}
			return state, ctx.Fail(ExitInternal, "reading run %s: %v", runID, err)
		}
		switch {
		case isTerminalState(state.State):
			return state, 0 // settled meanwhile; its own result stands
		case state.State == "running":
			signalWorker(ctx, state)
			return state, 0
		}
		settled, done, err := cancelQueued(ctx, st, state)
		if err != nil {
			return state, ctx.Fail(ExitInternal, "cancelling run %s: %v", runID, err)
		}
		if done {
			return settled, 0
		}
		// A worker holds the lock and has not yet written running.
		if !time.Now().Before(deadline) {
			return state, ctx.Fail(ExitInternal, "run %s is still queued after %s with a worker holding it", runID, wait)
		}
		time.Sleep(admissionPoll)
	}
}

// cancelQueued cancels a queued run that no worker has taken. It reports done
// false when a worker holds the lock, and when the run moved on meanwhile.
func cancelQueued(ctx *Context, st *store.Store, state store.State) (store.State, bool, error) {
	lock, err := st.LockRun(state.RunID)
	if errors.Is(err, store.ErrRunLockHeld) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	defer lock.Close()
	cur, err := st.ReadState(state.RunID)
	if err != nil || cur.State != "queued" {
		return cur, err == nil && isTerminalState(cur.State), err
	}
	return runner.CancelQueued(settleJob(ctx, st, cur)).State, true, nil
}

// signalWorker asks the worker of a running run to stop with SIGTERM; it ends
// the provider's group and finalizes the run as cancelled, unless the provider
// has already exited, in which case the run keeps the provider's result. A
// worker that is already gone is not an error: the run is finishing.
func signalWorker(ctx *Context, state store.State) {
	if state.WorkerPID <= 1 {
		return
	}
	if err := syscall.Kill(state.WorkerPID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		ctx.Warnf("could not signal the worker of run %s: %v", state.RunID, err)
	}
}
