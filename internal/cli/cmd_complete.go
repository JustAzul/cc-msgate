package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "complete", Summary: "run one completion from Codex, with no conversation or run", Run: runComplete})
}

const completeUsageLine = "usage: msgate complete --provider codex [--model <model>] [--effort <effort>] [--session-id <id>] [--run-id <run_id>] [--source <source>] [--call-id <id>] [--attr <key>=<value>]... [--timeout <seconds>] [--json]  (the request is JSON on standard input)"

// completeProvider is the only provider a completion runs on.
const completeProvider = "codex"

// unnamedModel is what a call records as its model when the caller named none:
// the provider answered with whatever model it is set to.
const unnamedModel = "provider-default"

// Outcomes of a completion.
const (
	outcomeAnswered   = "answered"
	outcomeEmptyReply = "empty-reply"
	outcomeAPIError   = "api-error"
)

// completeFlags are the flags of `complete`.
type completeFlags struct {
	provider, model, effort, sessionID, runID, source, callID string
	runIDGiven, callIDGiven, asJSON                           bool
	timeoutS                                                  int
	attrs                                                     callAttrs
}

// parseCompleteFlags reads the flags of `complete` and the arguments left
// after them.
func parseCompleteFlags(ctx *Context, args []string) (completeFlags, []string, error) {
	var f completeFlags
	fset := flag.NewFlagSet("complete", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.StringVar(&f.provider, "provider", completeProvider, "the provider to ask; only codex")
	fset.StringVar(&f.model, "model", "", "the model to ask")
	fset.StringVar(&f.effort, "effort", "", "the reasoning effort")
	fset.StringVar(&f.sessionID, "session-id", ctx.Getenv("CLAUDE_CODE_SESSION_ID"), "the Claude Code session id")
	fset.StringVar(&f.runID, "run-id", "", "the run the call was about")
	fset.StringVar(&f.source, "source", "cli", "what made the call")
	fset.StringVar(&f.callID, "call-id", "", "the call's own id; a call recorded twice under one id counts once")
	fset.Var(&f.attrs, "attr", "key=value recorded with the call; repeatable")
	fset.Var(timeoutFlag{&f.timeoutS}, "timeout", "kill the provider after this many seconds (default: no timeout)")
	fset.BoolVar(&f.asJSON, "json", false, "print one JSON object")
	positional, err := parseInterspersed(fset, args)
	fset.Visit(func(given *flag.Flag) {
		f.runIDGiven = f.runIDGiven || given.Name == "run-id"
		f.callIDGiven = f.callIDGiven || given.Name == "call-id"
	})
	return f, positional, err
}

// problem names the first thing `complete` cannot take, or "" when there is
// none.
func (f completeFlags) problem(positional []string) string {
	switch {
	case len(positional) != 0:
		return fmt.Sprintf("complete takes no arguments, got %q", positional[0])
	case f.provider != completeProvider:
		return fmt.Sprintf("--provider must be %s, got %q", completeProvider, f.provider)
	case f.callIDGiven && !callIDPattern.MatchString(f.callID):
		return fmt.Sprintf("--call-id must match %s, got %q", callIDPattern, f.callID)
	}
	return ""
}

// modelCall is the record of the call: the shape `usage add` writes.
func (f completeFlags) modelCall(usage provider.Usage, now time.Time) telemetry.ModelCall {
	model := f.model
	if model == "" {
		model = unnamedModel
	}
	record := usageAddFlags{
		sessionID: f.sessionID, provider: f.provider, model: model, source: f.source,
		runID: f.runID, callID: f.callID, runIDGiven: f.runIDGiven, callIDGiven: f.callIDGiven, attrs: f.attrs,
	}
	return record.modelCall(usage, now)
}

// completeAnswer is what a finished completion tells its caller.
type completeAnswer struct {
	Outcome string        `json:"outcome"`
	Text    string        `json:"text"`
	Model   string        `json:"model"`
	Usage   completeUsage `json:"usage"`
	Failure string        `json:"failure,omitempty"`
}

// completeUsage is the token counts Codex reported for the call.
type completeUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

func runComplete(ctx *Context, args []string) int {
	ctx.JSON = wantsJSON(args)
	f, positional, err := parseCompleteFlags(ctx, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, completeUsageLine)
		return ExitOK
	}
	if err != nil {
		return failFlagParse(ctx, completeUsageLine, err)
	}
	ctx.JSON = f.asJSON
	if problem := f.problem(positional); problem != "" {
		return ctx.Fail(ExitUsage, "%s", problem)
	}
	raw, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading stdin: %v", err)
	}
	req, err := parseCompleteRequest(raw)
	if err != nil {
		return ctx.Fail(ExitUsage, "stdin: %v", err)
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	return completeIn(ctx, store.Open(home), f, req)
}

// completeIn runs the request and reports it, with scratch files that last as
// long as the call.
func completeIn(ctx *Context, st *store.Store, f completeFlags, req completeRequest) int {
	prov, ok := provider.Get(f.provider)
	if !ok {
		return ctx.Fail(ExitUsage, "unknown provider %q (known: %v)", f.provider, provider.Names())
	}
	if err := prov.Capabilities().Unsupported(provider.Needs{Command: "exec", Sandbox: "read-only", Harness: provider.HarnessBare}); err != nil {
		return ctx.Fail(ExitUsage, "provider %q %v", f.provider, err)
	}
	cwd := filepath.Join(st.Home, "oneshot")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		return ctx.Fail(ExitInternal, "creating the working directory: %v", err)
	}
	scratch, err := os.MkdirTemp(st.Home, "complete-")
	if err != nil {
		return ctx.Fail(ExitInternal, "creating the scratch directory: %v", err)
	}
	defer os.RemoveAll(scratch)
	instructions, err := callInstructions(st, scratch, req.System)
	if err != nil {
		return ctx.Fail(ExitInternal, "writing the harness instructions: %v", err)
	}
	outputPath := filepath.Join(scratch, "output.md")
	plan, err := prov.BuildPlan(provider.Request{
		Command: "exec", Cwd: cwd, Model: f.model, Effort: f.effort, Sandbox: "read-only",
		Harness: provider.HarnessBare, HarnessInstructions: instructions, Env: ctx.Env,
		OutputPath: outputPath, Ephemeral: true,
	})
	if errors.Is(err, provider.ErrBinaryMissing) {
		return ctx.Fail(ExitProviderMissing, "building the provider plan: %v", err)
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "building the provider plan: %v", err)
	}
	res, err := runner.RunOneShot(runner.OneShot{
		Provider: prov, Plan: plan, Prompt: []byte(req.prompt()), Env: ctx.Env,
		Timeout: time.Duration(f.timeoutS) * time.Second, Shutdown: runner.ShutdownFromEnv(ctx.Getenv),
	})
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	return finishComplete(ctx, st, prov, f, res, outputPath)
}

// callInstructions is the instructions file the call runs on: the system text
// in a file of the call's own, else the one msgate keeps for the harness.
func callInstructions(st *store.Store, scratch, system string) (string, error) {
	if system == "" {
		return st.HarnessInstructionsPath(), st.EnsureHarnessInstructions(harnessInstructions)
	}
	path := filepath.Join(scratch, "instructions.md")
	return path, os.WriteFile(path, []byte(system), 0o600)
}

// finishComplete reports a finished call. A call msgate ended, or that never
// started, is an SDK failure; any other is an answer, an empty reply or an API
// error, and is recorded when it used tokens.
func finishComplete(ctx *Context, st *store.Store, prov provider.Provider, f completeFlags, res runner.OneShotResult, outputPath string) int {
	switch res.SDKStatus {
	case runner.SDKStatusCancelled:
		return ctx.Fail(ExitCancelled, "%s", res.Error)
	case runner.SDKStatusTimeout:
		return ctx.Fail(ExitTimeout, "%s", res.Error)
	case runner.SDKStatusProviderMissing:
		return ctx.Fail(ExitProviderMissing, "%s", res.Error)
	}
	now := ctx.Now().UTC().Truncate(time.Second)
	if res.Usage != nil {
		call := f.modelCall(*res.Usage, now)
		if err := telemetry.AppendModelCall(st.Home, call, now, telemetry.Options{}); err != nil {
			ctx.Warnf("could not write the model call: %v", err)
		}
	}
	answer, err := completeAnswerOf(prov, f, res, outputPath)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading the answer: %v", err)
	}
	if answer.Outcome == outcomeAPIError {
		ctx.Errorf("%s", res.Error)
		if err := markProviderDown(st, prov, res.Error, now); err != nil {
			ctx.Warnf("could not mark provider %s unavailable: %v", prov.Name(), err)
		}
	}
	return printComplete(ctx, answer, res.ExitCode)
}

// completeAnswerOf is the outcome of a call whose provider ran to an exit: an
// API error when the exit was not zero, else the text of the final message. A
// final message that cannot be read, other than for being absent, is an error.
func completeAnswerOf(prov provider.Provider, f completeFlags, res runner.OneShotResult, outputPath string) (completeAnswer, error) {
	answer := completeAnswer{Model: f.model, Usage: completeUsageOf(res.Usage)}
	if answer.Model == "" {
		answer.Model = unnamedModel
	}
	if res.ExitCode != 0 {
		answer.Outcome = outcomeAPIError
		answer.Failure = string(provider.ClassifyFailure(prov, res.Error))
		return answer, nil
	}
	text, err := os.ReadFile(outputPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) { // no file: the provider wrote no final message
		return answer, err
	}
	answer.Text = string(text)
	answer.Outcome = outcomeAnswered
	if strings.TrimSpace(answer.Text) == "" {
		answer.Outcome = outcomeEmptyReply
	}
	return answer, nil
}

func completeUsageOf(u *provider.Usage) completeUsage {
	if u == nil {
		return completeUsage{}
	}
	return completeUsage{u.InputTokens, u.CachedInputTokens, u.OutputTokens, u.ReasoningOutputTokens}
}

// printComplete prints the answer, as the text alone or as the --json object,
// and returns the exit code.
func printComplete(ctx *Context, answer completeAnswer, exit int) int {
	if ctx.JSON {
		if printJSON(ctx, answer) != 0 {
			return ExitInternal
		}
		return exit
	}
	if _, err := io.WriteString(ctx.Stdout, answer.Text); err != nil {
		return ctx.Fail(ExitInternal, "printing the answer: %v", err)
	}
	return exit
}
