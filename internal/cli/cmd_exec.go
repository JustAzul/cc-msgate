package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/settings"
	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "exec", Summary: "start a conversation and run its first turn", Run: runExec})
}

var execSpec = turnSpec{name: "exec", usage: "[flags] <prompt | -> [-- native flags]"}

func runExec(ctx *Context, args []string) int {
	p, exit, done := parseTurnArgs(ctx, execSpec, args)
	if done {
		return exit
	}

	prov, ok := provider.Get(p.provider)
	if !ok {
		return ctx.Fail(ExitUsage, "unknown provider %q (known: %v)", p.provider, provider.Names())
	}
	eff := settings.Resolve(settings.Flags{Model: p.model, Effort: p.effort, Sandbox: p.sandbox, EffortSource: p.effortSource}, p.settingsSource)
	if err := prov.Capabilities().Unsupported(provider.Needs{Command: "exec", Sandbox: eff.Sandbox, Harness: p.harness}); err != nil {
		return ctx.Fail(ExitUsage, "provider %q %v", p.provider, err)
	}
	if flagText, reserved := reservedPassthrough(prov, p.harness, p.passthrough); reserved {
		return ctx.Fail(ExitUsage, "native flag %q is reserved by msgate and cannot be passed after --", flagText)
	}

	if len(p.positional)+boolToInt(p.promptFile != "") != 1 {
		return ctx.Fail(ExitUsage, "exactly one prompt source is required: a positional prompt, - for stdin, or --prompt-file")
	}
	prompt, code := readPrompt(ctx, p)
	if code != 0 {
		return code
	}

	cwd, code := resolveCwd(ctx, p.cwd, p.dryRun)
	if code != 0 {
		return code
	}

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	return runTurn(ctx, turn{
		command: "exec", flags: p, prov: prov, eff: eff, cwd: cwd, prompt: prompt, st: store.Open(home),
		req: provider.Request{
			Command: "exec", Cwd: cwd, Model: eff.Model, Effort: eff.Effort, Sandbox: eff.Sandbox,
			Passthrough: p.passthrough, Harness: p.harness,
		},
	})
}

// printRunResult prints the --json object for a finished run and returns its
// exit code.
func printRunResult(ctx *Context, res runner.Result) int {
	s := res.State
	out := struct {
		ConversationID string  `json:"conversation_id"`
		RunID          string  `json:"run_id"`
		State          string  `json:"state"`
		Outcome        *string `json:"outcome"`
		SDKStatus      string  `json:"sdk_status"`
		ProviderExit   *int    `json:"provider_exit"`
		ExitCode       int     `json:"exit_code"`
		OutputPath     string  `json:"output_path"`
		RunDir         string  `json:"run_dir"`
	}{s.ConversationID, s.RunID, s.State, s.Outcome, res.SDKStatus, res.ProviderExit, res.ExitCode, s.OutputPath, s.RunDir}
	if printJSON(ctx, out) != 0 {
		return ExitInternal
	}
	return res.ExitCode
}

func printJSON(ctx *Context, v any) int {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		ctx.Errorf("encoding output: %v", err)
		return ExitInternal
	}
	ctx.Stdout.Write(append(data, '\n'))
	return 0
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// readPrompt returns the prompt for the single chosen source. Dry runs do not
// consume standard input.
func readPrompt(ctx *Context, p parsedTurn) ([]byte, int) {
	switch {
	case p.promptFile != "":
		data, err := os.ReadFile(p.promptFile)
		if err != nil {
			return nil, ctx.Fail(ExitUsage, "reading --prompt-file: %v", err)
		}
		return data, 0
	case p.positional[0] == "-":
		if p.dryRun {
			return nil, 0
		}
		data, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return nil, ctx.Fail(ExitUsage, "reading the prompt from standard input: %v", err)
		}
		return data, 0
	default:
		return []byte(p.positional[0]), 0
	}
}

// resolveCwd makes the working directory absolute. A real run requires it to
// exist; a dry run only describes the plan.
func resolveCwd(ctx *Context, flagValue string, dryRun bool) (string, int) {
	cwd := flagValue
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", ctx.Fail(ExitInternal, "cannot determine the working directory: %v", err)
		}
		cwd = wd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", ctx.Fail(ExitUsage, "--cwd: %v", err)
	}
	if !dryRun {
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return "", ctx.Fail(ExitUsage, "--cwd %q is not an existing directory", abs)
		}
	}
	return abs, 0
}

// chooseRunID validates a caller-supplied run id or generates a fresh one.
func chooseRunID(ctx *Context, st *store.Store, supplied string, now time.Time) (string, int) {
	if supplied != "" {
		if err := store.ValidateRunID(supplied); err != nil {
			return "", ctx.Fail(ExitUsage, "invalid --run-id: %v", err)
		}
		if st.RunExists(supplied) {
			return "", ctx.Fail(ExitUsage, "run id %q already exists", supplied)
		}
		return supplied, 0
	}
	for i := 0; i < 5; i++ {
		id := store.NewRunID(now)
		if !st.RunExists(id) {
			return id, 0
		}
	}
	return "", ctx.Fail(ExitInternal, "could not generate a unique run id")
}

// reservedPassthrough returns the first native token the provider reserves, or
// that sets a config key the harness level sets. Each token is checked alone and
// joined to its successor, so option/value pairs such as "-c model=x" are seen
// as one string.
func reservedPassthrough(prov provider.Provider, level string, args []string) (string, bool) {
	reserved := func(arg string) bool { return prov.ReservedFlag(arg) || harnessReserved(prov, level, arg) }
	for i, a := range args {
		if reserved(a) {
			return a, true
		}
		if i+1 < len(args) {
			if pair := a + " " + args[i+1]; reserved(pair) {
				return pair, true
			}
		}
	}
	return "", false
}
