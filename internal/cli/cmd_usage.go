package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "usage", Summary: "usage add records the token usage of a model call", Run: runUsage})
}

const usageAddLine = "usage: msgate usage add --session-id <id> --provider anthropic --model <model> --source <source> [--run-id <run_id>] [--call-id <id>] [--attr <key>=<value>]... [--ts <RFC 3339>] [--json]"

// What a call id, an attr and an attr count may be: the record of a call is
// read back by whatever made it, so each is held to a shape it can rely on.
const (
	maxAttrs          = 32
	maxAttrValueChars = 200
)

var (
	callIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{0,199}$`)
	attrKeyPattern = regexp.MustCompile(`^[a-z_]{1,32}$`)
)

// callAttrs collects the repeated --attr key=value flags.
type callAttrs map[string]string

func (a *callAttrs) String() string { return fmt.Sprint(map[string]string(*a)) }

func (a *callAttrs) Set(raw string) error {
	key, value, found := strings.Cut(raw, "=")
	switch {
	case !found:
		return fmt.Errorf("%q is not key=value", raw)
	case !attrKeyPattern.MatchString(key):
		return fmt.Errorf("attr key %q must match %s", key, attrKeyPattern)
	case utf8.RuneCountInString(value) > maxAttrValueChars:
		return fmt.Errorf("attr %s: the value is longer than %d characters", key, maxAttrValueChars)
	}
	if _, repeated := (*a)[key]; repeated {
		return fmt.Errorf("attr %s is given twice", key)
	}
	if len(*a) == maxAttrs {
		return fmt.Errorf("at most %d attrs are taken", maxAttrs)
	}
	if *a == nil {
		*a = callAttrs{}
	}
	(*a)[key] = value
	return nil
}

// callTime is an RFC 3339 instant.
type callTime struct {
	at      time.Time
	isGiven bool
}

func (f *callTime) String() string { return f.at.Format(time.RFC3339) }

func (f *callTime) Set(raw string) error {
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return fmt.Errorf("%q is not an RFC 3339 time", raw)
	}
	f.at, f.isGiven = at.UTC().Truncate(time.Second), true
	return nil
}

// modelCallProviders maps each provider a model call can be recorded for to
// the price-list namespace (litellm_provider) its models are priced under.
// These providers have no model catalog.
var modelCallProviders = map[string]string{"anthropic": "anthropic"}

func runUsage(ctx *Context, args []string) int {
	if len(args) > 0 && args[0] == "add" {
		return runUsageAdd(ctx, args[1:])
	}
	ctx.JSON = wantsJSON(args)
	fmt.Fprintln(ctx.Stderr, usageAddLine)
	return ctx.Fail(ExitUsage, "usage needs a subcommand: add")
}

// usageAddFlags is what `usage add` was asked to record.
type usageAddFlags struct {
	sessionID, provider, model, source, runID, callID string
	runIDGiven, callIDGiven, asJSON                   bool
	attrs                                             callAttrs
	ts                                                callTime
}

// parseUsageAddFlags reads the flags of `usage add` and the arguments left
// after them.
func parseUsageAddFlags(args []string) (usageAddFlags, []string, error) {
	var f usageAddFlags
	fset := flag.NewFlagSet("usage add", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.StringVar(&f.sessionID, "session-id", "", "the Claude Code session id")
	fset.StringVar(&f.provider, "provider", "", "the model provider")
	fset.StringVar(&f.model, "model", "", "the model called")
	fset.StringVar(&f.source, "source", "", "what made the call")
	fset.StringVar(&f.runID, "run-id", "", "the run the call was about")
	fset.StringVar(&f.callID, "call-id", "", "the call's own id; a call recorded twice under one id counts once")
	fset.Var(&f.attrs, "attr", "key=value recorded with the call; repeatable")
	fset.Var(&f.ts, "ts", "when the call was made, as an RFC 3339 time (default: now)")
	fset.BoolVar(&f.asJSON, "json", false, "print one JSON object")
	positional, err := parseInterspersed(fset, args)
	fset.Visit(func(given *flag.Flag) {
		f.runIDGiven = f.runIDGiven || given.Name == "run-id"
		f.callIDGiven = f.callIDGiven || given.Name == "call-id"
	})
	return f, positional, err
}

// problem names the first thing `usage add` cannot take, or "" when there is
// none.
func (f usageAddFlags) problem(positional []string) string {
	switch {
	case len(positional) != 0:
		return fmt.Sprintf("usage add takes no arguments, got %q", positional[0])
	case f.sessionID == "":
		return "usage add needs a non-empty --session-id"
	case f.model == "":
		return "usage add needs a non-empty --model"
	case f.source == "":
		return "usage add needs a non-empty --source"
	case f.callIDGiven && !callIDPattern.MatchString(f.callID):
		return fmt.Sprintf("--call-id must match %s, got %q", callIDPattern, f.callID)
	}
	if _, ok := modelCallProviders[f.provider]; !ok {
		return fmt.Sprintf("--provider must be anthropic, got %q", f.provider)
	}
	return ""
}

// calledAt is when the call was made: --ts, else now.
func (f usageAddFlags) calledAt(now time.Time) time.Time {
	if f.ts.isGiven {
		return f.ts.at
	}
	return now
}

// modelCall is the record of one call with usage made at calledAt. An absent
// --run-id records null, an absent --call-id a new id, and attrs are recorded
// only when there are some.
func (f usageAddFlags) modelCall(usage provider.Usage, now time.Time) telemetry.ModelCall {
	calledAt := f.calledAt(now)
	call := telemetry.ModelCall{
		CallID: store.NewCallID(now), TS: calledAt.Format(time.RFC3339), SessionID: f.sessionID,
		Provider: f.provider, Model: f.model, Source: f.source, Usage: usage,
	}
	if f.callIDGiven {
		call.CallID = f.callID
	}
	if f.runIDGiven {
		call.RunID = &f.runID
	}
	if len(f.attrs) > 0 {
		call.Attrs = f.attrs
	}
	return call
}

func runUsageAdd(ctx *Context, args []string) int {
	ctx.JSON = wantsJSON(args)
	f, positional, err := parseUsageAddFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, usageAddLine)
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = f.asJSON
	if problem := f.problem(positional); problem != "" {
		return ctx.Fail(ExitUsage, "%s", problem)
	}

	raw, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading stdin: %v", err)
	}
	usage, err := NormalizeAnthropicUsage(raw)
	if err != nil {
		return ctx.Fail(ExitUsage, "stdin: %v", err)
	}
	report := printUsageAddText
	if f.asJSON {
		report = printUsageAddJSON
	}
	if usage.IsZero() {
		return report(ctx, "")
	}

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	now := ctx.Now().UTC().Truncate(time.Second)
	call := f.modelCall(usage, now)
	// The record goes in the month file of the call's time, so a call recorded
	// twice across a month's end still lands twice in one file.
	if err := telemetry.AppendModelCall(home, call, f.calledAt(now), telemetry.Options{}); err != nil {
		return ctx.Fail(ExitInternal, "writing the model call: %v", err)
	}
	return report(ctx, call.CallID)
}

// printUsageAddJSON reports the outcome as one JSON object; an empty callID
// means nothing was recorded.
func printUsageAddJSON(ctx *Context, callID string) int {
	var id any
	if callID != "" {
		id = callID
	}
	out, err := json.Marshal(struct {
		SDKStatus string `json:"sdk_status"`
		ExitCode  int    `json:"exit_code"`
		Recorded  bool   `json:"recorded"`
		CallID    any    `json:"call_id"`
	}{"ok", ExitOK, callID != "", id})
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	fmt.Fprintf(ctx.Stdout, "%s\n", out)
	return ExitOK
}

// printUsageAddText prints the call id, or nothing when nothing was recorded.
func printUsageAddText(ctx *Context, callID string) int {
	if callID != "" {
		fmt.Fprintln(ctx.Stdout, callID)
	}
	return ExitOK
}

// anthropicUsage is the usage object Anthropic reports. A missing key reads
// as zero; any other key is ignored.
type anthropicUsage struct {
	Input         nonNegInt `json:"input_tokens"`
	Output        nonNegInt `json:"output_tokens"`
	CacheRead     nonNegInt `json:"cache_read_input_tokens"`
	CacheCreation nonNegInt `json:"cache_creation_input_tokens"`
}

// nonNegInt decodes a JSON non-negative integer and nothing else.
type nonNegInt int64

func (n *nonNegInt) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return fmt.Errorf("%s is not a non-negative integer", b)
	}
	var num json.Number
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&num); err != nil {
		return fmt.Errorf("%s is not a non-negative integer", b)
	}
	v, err := num.Int64()
	if err != nil || v < 0 {
		return fmt.Errorf("%s is not a non-negative integer", b)
	}
	*n = nonNegInt(v)
	return nil
}

// NormalizeAnthropicUsage turns Anthropic's usage object (one JSON object)
// into msgate's usage shape: input is the total input including the cache
// read and cache creation tokens, cached is the cache reads, cache write is
// the cache creations, and reasoning is zero. It fails when the input is not
// one JSON object, or a count is not a non-negative integer, or the input
// total overflows.
func NormalizeAnthropicUsage(raw []byte) (provider.Usage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return provider.Usage{}, errors.New("expected one JSON object")
	}
	var in anthropicUsage
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&in); err != nil {
		return provider.Usage{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return provider.Usage{}, errors.New("expected one JSON object")
	}
	input, read, write := int64(in.Input), int64(in.CacheRead), int64(in.CacheCreation)
	if input > math.MaxInt64-read || input+read > math.MaxInt64-write {
		return provider.Usage{}, errors.New("token counts are too large")
	}
	return provider.Usage{
		InputTokens:           input + read + write,
		CachedInputTokens:     read,
		CacheWriteInputTokens: write,
		OutputTokens:          int64(in.Output),
	}, nil
}
