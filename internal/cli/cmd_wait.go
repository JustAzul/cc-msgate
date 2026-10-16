package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "wait", Summary: "block until a run finishes, then print it like a foreground run", Run: runWait})
}

const (
	waitPoll = 25 * time.Millisecond
	// finalizeWait is how long wait gives a terminal run's worker to release
	// the run lock; MSGATE_TEST_FINALIZE_WAIT_MS overrides it in tests.
	finalizeWait = 10 * time.Second
)

func runWait(ctx *Context, args []string) int {
	var asJSON bool
	var timeoutS int
	fset := flag.NewFlagSet("wait", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.Var(timeoutFlag{&timeoutS}, "timeout", "give up after this many seconds, leaving the run as it is")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, "usage: msgate wait <run_id> [--timeout <seconds>] [--json]")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	if len(positional) != 1 {
		return ctx.Fail(ExitUsage, "wait takes exactly one run id")
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
	// One deadline covers both the wait for the terminal state and the wait
	// for the run's finalization.
	var expired <-chan time.Time
	if timeoutS > 0 {
		t := time.NewTimer(time.Duration(timeoutS) * time.Second)
		defer t.Stop()
		expired = t.C
	}
	state, code = awaitTerminal(ctx, st, state, expired, time.Duration(timeoutS)*time.Second)
	if code != 0 {
		return code
	}
	if code = awaitFinalized(ctx, st, state, expired, time.Duration(timeoutS)*time.Second); code != 0 {
		return code
	}

	if !st.RunExists(state.RunID) {
		return ctx.Fail(ExitNotFound, "no run %q", state.RunID)
	}
	exit := ExitInternal
	if state.ExitCode != nil {
		exit = *state.ExitCode
	}
	if asJSON {
		if printJSON(ctx, resultOf(state)) != 0 {
			return ExitInternal
		}
		return exit
	}
	fmt.Fprintln(ctx.Stdout, state.OutputPath)
	return exit
}

// awaitTerminal polls the run until it is terminal. A nil expired channel
// waits without limit; when it fires first the run is left as it is and the
// exit is 5. timeout is only named in the message.
func awaitTerminal(ctx *Context, st *store.Store, state store.State, expired <-chan time.Time, timeout time.Duration) (store.State, int) {
	tick := time.NewTicker(waitPoll)
	defer tick.Stop()
	for !isTerminalState(state.State) {
		select {
		case <-expired:
			return state, ctx.Fail(ExitWaitTimeout, "run %s is still %s after %s", state.RunID, state.State, timeout)
		case <-tick.C:
		}
		next, err := observeRun(ctx, st, state.RunID)
		if errors.Is(err, os.ErrNotExist) {
			return state, ctx.Fail(ExitNotFound, "no run %q", state.RunID)
		}
		if err != nil {
			return state, ctx.Fail(ExitInternal, "reading run %s: %v", state.RunID, err)
		}
		state = next
	}
	return state, 0
}

// awaitFinalized waits for the terminal run's lock to be released: the worker
// still writes the conversation marker and the telemetry record after the
// state turns terminal, and holds the lock until it exits. It gives up after
// finalizeWait with one warning, and gives up with exit 5 when expired fires
// first.
func awaitFinalized(ctx *Context, st *store.Store, state store.State, expired <-chan time.Time, timeout time.Duration) int {
	bound := finalizeWait
	if d, ok := envMillis(ctx.Getenv, "MSGATE_TEST_FINALIZE_WAIT_MS"); ok {
		bound = d
	}
	giveUp := time.NewTimer(bound)
	defer giveUp.Stop()
	tick := time.NewTicker(waitPoll)
	defer tick.Stop()
	for {
		held, err := st.RunLockHeld(state.RunID)
		if err != nil {
			return ctx.Fail(ExitInternal, "reading run %s: %v", state.RunID, err)
		}
		if !held {
			return 0
		}
		select {
		case <-expired:
			return ctx.Fail(ExitWaitTimeout, "run %s is %s but still finishing after %s", state.RunID, state.State, timeout)
		case <-giveUp.C:
			ctx.Warnf("run %s is %s but its worker still holds the run after %s; its record may be missing", state.RunID, state.State, bound)
			return 0
		case <-tick.C:
		}
	}
}
