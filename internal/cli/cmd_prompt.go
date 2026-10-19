package cli

import (
	"flag"
	"io"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "prompt", Summary: "print the prompt a run was started with", Run: runPrompt})
}

const promptUsage = "usage: msgate prompt <run_id>"

// runPrompt writes prompt.md to stdout byte for byte: the output is the
// prompt itself, so there is no --json form and no added newline.
func runPrompt(ctx *Context, args []string) int {
	fset := flag.NewFlagSet("prompt", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	positional, err := parseInterspersed(fset, args)
	if err != nil {
		return failFlagParse(ctx, promptUsage, err)
	}

	if len(positional) != 1 {
		return ctx.Fail(ExitUsage, promptUsage)
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
	prompt, err := st.ReadPrompt(state.RunID)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading the prompt of run %s: %v", state.RunID, err)
	}
	if _, err := ctx.Stdout.Write(prompt); err != nil {
		return ctx.Fail(ExitInternal, "writing the prompt of run %s: %v", state.RunID, err)
	}
	return ExitOK
}
