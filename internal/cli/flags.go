package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/settings"
)

// turnFlags are the flags shared by commands that run a provider turn.
type turnFlags struct {
	provider       string
	purpose        string
	settingsSource string
	effortSource   string // "" when --effort-source was not passed
	model          string
	effort         string
	sandbox        string
	harness        string // full, lean or bare; on send, the conversation's stored level unless passed
	cwd            string
	source         string
	sessionID      string
	runID          string
	promptFile     string
	cleanSentinel  string
	materialLabel  string
	timeoutS       int // 0: no timeout
	json           bool
	dryRun         bool
	background     bool
	agentFeedback  bool
	attrs          attrList
}

// timeoutFlag parses --timeout: a positive whole number of seconds.
type timeoutFlag struct{ seconds *int }

func (f timeoutFlag) String() string { return "" }

func (f timeoutFlag) Set(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fmt.Errorf("--timeout needs a positive whole number of seconds, got %q", raw)
	}
	*f.seconds = n
	return nil
}

// attrList collects --attr and --attr-json values; a later duplicate key
// replaces an earlier one.
type attrList struct {
	values map[string]any
}

func (l *attrList) set(key string, value any) {
	if l.values == nil {
		l.values = map[string]any{}
	}
	l.values[key] = value
}

// object returns the attrs as a JSON object; never nil.
func (l attrList) object() map[string]any {
	out := make(map[string]any, len(l.values))
	for k, v := range l.values {
		out[k] = v
	}
	return out
}

// attrFlag is a repeatable flag.Value feeding an attrList.
type attrFlag struct {
	list   *attrList
	asJSON bool
}

func (f attrFlag) String() string { return "" }

func (f attrFlag) Set(raw string) error {
	name := "--attr"
	if f.asJSON {
		name = "--attr-json"
	}
	key, value, ok := strings.Cut(raw, "=")
	if !ok || key == "" {
		return fmt.Errorf("%s needs key=value with a non-empty key, got %q", name, raw)
	}
	if !f.asJSON {
		f.list.set(key, value)
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%s %q: the value is not valid JSON: %v", name, key, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%s %q: the value holds more than one JSON value", name, key)
	}
	f.list.set(key, v)
	return nil
}

// settingsSourceFlag parses --settings-source: the source recorded for the
// settings the caller passes.
type settingsSourceFlag struct{ target *string }

func (f settingsSourceFlag) String() string {
	if f.target == nil {
		return ""
	}
	return *f.target
}

func (f settingsSourceFlag) Set(raw string) error {
	if !settings.ValidSource(raw) {
		return fmt.Errorf("--settings-source needs %q or %q, got %q", settings.SourceFlag, settings.SourceProfile, raw)
	}
	*f.target = raw
	return nil
}

// effortSourceFlag parses --effort-source: the source recorded for the effort
// the caller passes, in place of --settings-source's.
type effortSourceFlag struct{ target *string }

func (f effortSourceFlag) String() string {
	if f.target == nil {
		return ""
	}
	return *f.target
}

func (f effortSourceFlag) Set(raw string) error {
	if !settings.ValidSource(raw) {
		return fmt.Errorf("--effort-source needs %q or %q, got %q", settings.SourceFlag, settings.SourceProfile, raw)
	}
	*f.target = raw
	return nil
}

// failFlagParse reports a refused flag parse: the command's usage on stderr
// first, then one last line carrying the error, so the line a caller keeps is
// the error. It returns the usage exit.
func failFlagParse(ctx *Context, usage string, err error) int {
	fmt.Fprintln(ctx.Stderr, usage)
	return ctx.Fail(ExitUsage, "%v", err)
}

// parsedTurn is the result of parsing a turn command's arguments.
type parsedTurn struct {
	turnFlags
	positional  []string
	passthrough []string
	// given holds the names of the flags the caller passed explicitly, so a
	// command can tell "not passed" from a flag's default.
	given map[string]bool
}

// turnSpec describes one turn command for parseTurnArgs.
type turnSpec struct {
	name  string
	usage string                 // synopsis printed after "msgate <name> "
	extra func(fs *flag.FlagSet) // registers the command's own flags; may be nil
}

// parseTurnArgs parses the flags of a turn command with the standard library.
// Flags and positionals may be interspersed; everything after the first bare
// "--" is native passthrough, verbatim. exit is meaningful when done is true.
func parseTurnArgs(ctx *Context, spec turnSpec, args []string) (p parsedTurn, exit int, done bool) {
	head := args
	for i, a := range args {
		if a == "--" {
			head = args[:i]
			p.passthrough = append([]string(nil), args[i+1:]...)
			break
		}
	}

	fs := flag.NewFlagSet(spec.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is reported once, by failFlagParse's last line
	fs.Usage = func() { fmt.Fprintf(ctx.Stderr, "usage: msgate %s %s\n", spec.name, spec.usage) }
	f := &p.turnFlags
	fs.StringVar(&f.provider, "provider", "codex", "provider to run")
	fs.StringVar(&f.purpose, "purpose", "adhoc", "caller's label for the run's purpose")
	f.settingsSource = settings.SourceFlag
	fs.Var(settingsSourceFlag{&f.settingsSource}, "settings-source", "source recorded for the model, effort and sandbox passed: flag or profile (default flag)")
	fs.Var(effortSourceFlag{&f.effortSource}, "effort-source", "source recorded for the effort passed, in place of --settings-source's: flag or profile")
	fs.StringVar(&f.model, "model", "", "model")
	fs.StringVar(&f.effort, "effort", "", "reasoning effort")
	fs.StringVar(&f.sandbox, "sandbox", "", "read-only or workspace-write")
	f.harness = provider.HarnessFull
	fs.Var(harnessFlag{&f.harness}, "harness", "how much of the provider's own instructions, skills and tools the run keeps: full, lean or bare (default full)")
	fs.StringVar(&f.cwd, "cwd", "", "working directory (default: current)")
	fs.StringVar(&f.source, "source", "cli", "who dispatched the run")
	fs.StringVar(&f.sessionID, "session-id", ctx.Getenv("CLAUDE_CODE_SESSION_ID"), "Claude Code session id")
	fs.StringVar(&f.runID, "run-id", "", "caller-supplied run id")
	fs.StringVar(&f.promptFile, "prompt-file", "", "read the prompt from a file")
	fs.StringVar(&f.cleanSentinel, "clean-sentinel", "", "output text that classifies the run as clean")
	fs.StringVar(&f.materialLabel, "material-label", "ok", "outcome label for material output")
	fs.Var(timeoutFlag{&f.timeoutS}, "timeout", "kill the provider after this many seconds (default: no timeout)")
	fs.BoolVar(&f.json, "json", false, "print one JSON object instead of the output path")
	fs.Var(attrFlag{list: &f.attrs}, "attr", "attribute key=value (repeatable)")
	fs.Var(attrFlag{list: &f.attrs, asJSON: true}, "attr-json", "attribute key=<json> (repeatable)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the provider plan and execute nothing")
	fs.BoolVar(&f.background, "background", false, "admit the run as a job and return once its worker is running")
	fs.BoolVar(&f.agentFeedback, "agent-feedback", isOn(ctx.Getenv(agentFeedbackEnv)),
		"show the run in its Claude Code session while it works (needs a session id; default from "+agentFeedbackEnv+")")
	if spec.extra != nil {
		spec.extra(fs)
	}

	rest := head
	for {
		if err := fs.Parse(rest); err != nil {
			ctx.JSON = wantsJSON(head)
			if errors.Is(err, flag.ErrHelp) {
				ctx.emitErrorJSON(ExitOK, "help requested")
				return p, ExitOK, true
			}
			return p, ctx.Fail(ExitUsage, "%v", err), true
		}
		ctx.JSON = p.json
		if fs.NArg() == 0 {
			p.given = map[string]bool{}
			fs.Visit(func(f *flag.Flag) { p.given[f.Name] = true })
			requireSessionForFeedback(ctx, f)
			return p, 0, false
		}
		p.positional = append(p.positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// agentFeedbackEnv turns --agent-feedback on for every turn of a caller that
// cannot change its arguments; a msgate that predates the flag ignores it.
const agentFeedbackEnv = "MSGATE_AGENT_FEEDBACK"

func isOn(value string) bool {
	return value == "1" || strings.EqualFold(value, "true")
}

// requireSessionForFeedback turns agent feedback off for a run with no session
// id, which no Claude Code session could show, and says why on stderr. The run
// itself goes ahead: a display option never fails its caller.
func requireSessionForFeedback(ctx *Context, f *turnFlags) {
	if !f.agentFeedback || f.sessionID != "" {
		return
	}
	f.agentFeedback = false
	fmt.Fprintln(ctx.Stderr, "msgate: --agent-feedback ignored: no session id (pass --session-id or set CLAUDE_CODE_SESSION_ID)")
}
