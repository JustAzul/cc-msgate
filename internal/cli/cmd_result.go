package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "result", Summary: "print a finished run's output", Run: runResultCmd})
}

func runResultCmd(ctx *Context, args []string) int {
	if rest, asked := withoutFlag(args, reportFlag); asked {
		return runResultReport(ctx, rest)
	}
	if len(args) != 1 {
		return ctx.Fail(ExitUsage, "usage: msgate result <run_id>")
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	state, code := loadRun(ctx, st, args[0])
	if code != 0 {
		return code
	}
	if !isTerminalState(state.State) {
		return ctx.Fail(ExitBusy, "run %s is still %s; wait for it with: msgate wait %s", state.RunID, state.State, state.RunID)
	}
	f, err := os.Open(state.OutputPath)
	if errors.Is(err, os.ErrNotExist) {
		if !st.RunExists(state.RunID) {
			return ctx.Fail(ExitNotFound, "no run %q", state.RunID)
		}
		fmt.Fprintf(ctx.Stderr, "msgate: run %s produced no output file\n", state.RunID)
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "opening the output: %v", err)
	}
	defer f.Close()
	if _, err := io.Copy(ctx.Stdout, f); err != nil {
		return ctx.Fail(ExitInternal, "printing the output: %v", err)
	}
	return ExitOK
}
