// Package provider defines the contract between the SDK and agent CLIs.
package provider

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Capabilities declares what a provider supports.
type Capabilities struct {
	Commands          []string `json:"commands"`
	ReviewTargets     []string `json:"review_targets"`
	Sandboxes         []string `json:"sandboxes"`
	ReportsUsage      bool     `json:"reports_usage"`
	PreassignsSession bool     `json:"preassigns_session"`
	Harnesses         []string `json:"harnesses"`
}

// Harness levels: how much of its own instructions, skills and tools a
// provider adds to a request. Every provider runs HarnessFull.
const (
	HarnessFull = "full"
	HarnessLean = "lean"
	HarnessBare = "bare"
)

// ValidHarness reports whether level is one of the three harness levels.
func ValidHarness(level string) bool {
	return level == HarnessFull || level == HarnessLean || level == HarnessBare
}

// Supports reports whether the provider supports the command.
func (c Capabilities) Supports(command string) bool { return contains(c.Commands, command) }

// SupportsSandbox reports whether the provider supports the sandbox mode.
func (c Capabilities) SupportsSandbox(mode string) bool { return contains(c.Sandboxes, mode) }

// Needs is what one request requires of a provider.
type Needs struct {
	Command      string // "exec", "review" or "resume"
	Sandbox      string // empty when no sandbox mode is requested
	ReviewTarget string // empty when no review target is requested
	Harness      string // empty when no harness level is requested; full needs nothing
}

// Unsupported returns an error naming the first capability the request needs
// that the provider does not declare, or nil.
func (c Capabilities) Unsupported(n Needs) error {
	if n.Command != "" && !c.Supports(n.Command) {
		return fmt.Errorf("does not support the %s command (supported: %v)", n.Command, c.Commands)
	}
	if n.Sandbox != "" && !c.SupportsSandbox(n.Sandbox) {
		return fmt.Errorf("does not support sandbox mode %q (supported: %v)", n.Sandbox, c.Sandboxes)
	}
	if n.ReviewTarget != "" && !contains(c.ReviewTargets, n.ReviewTarget) {
		return fmt.Errorf("does not support review target %q (supported: %v)", n.ReviewTarget, c.ReviewTargets)
	}
	if !c.SupportsHarness(n.Harness) {
		return fmt.Errorf("does not support harness level %q (supported: %v)", n.Harness, c.Harnesses)
	}
	return nil
}

// SupportsHarness reports whether the provider runs the harness level. Every
// provider runs full, which asks nothing of it, and an empty level means full.
func (c Capabilities) SupportsHarness(level string) bool {
	return level == "" || level == HarnessFull || contains(c.Harnesses, level)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// StdinSource says what the provider receives on standard input.
type StdinSource string

const (
	StdinPrompt StdinSource = "prompt"
	StdinEmpty  StdinSource = "empty"
)

// Request is the provider-neutral description of one turn.
type Request struct {
	Command   string // "exec", "review" or "resume"
	Cwd       string
	Model     string
	Effort    string
	Sandbox   string
	SessionID string // provider session id, for resume
	// ReviewTarget is "base", "uncommitted" or "commit" for a review; ReviewRef
	// is the branch or commit it names (empty for "uncommitted").
	ReviewTarget string
	ReviewRef    string
	Passthrough  []string
	OutputPath   string
	// Harness is the level the turn runs at; empty means full. At any other
	// level HarnessInstructions is the absolute path of the instructions file
	// the caller provides to replace the provider's own base instructions.
	Harness             string
	HarnessInstructions string
	// Env is the environment the planned process runs under; a provider reads
	// its own configuration through it when a plan depends on that.
	Env []string
}

// Plan is how to start the provider process.
type Plan struct {
	Argv      []string          `json:"argv"` // argv[0] is the provider binary name
	Stdin     StdinSource       `json:"stdin"`
	Dir       string            `json:"cwd"`
	EnvAdd    map[string]string `json:"env_add"`
	EnvRemove []string          `json:"env_remove"`
}

// Usage is the token accounting a provider reports.
type Usage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

// IsZero reports whether every counter is zero.
func (u Usage) IsZero() bool { return u == Usage{} }

// Minus is u - base, each counter floored at 0.
func (u Usage) Minus(base Usage) Usage {
	floor := func(n int64) int64 { return max(n, 0) }
	return Usage{
		InputTokens:           floor(u.InputTokens - base.InputTokens),
		CachedInputTokens:     floor(u.CachedInputTokens - base.CachedInputTokens),
		CacheWriteInputTokens: floor(u.CacheWriteInputTokens - base.CacheWriteInputTokens),
		OutputTokens:          floor(u.OutputTokens - base.OutputTokens),
		ReasoningOutputTokens: floor(u.ReasoningOutputTokens - base.ReasoningOutputTokens),
	}
}

// NormalizeUsage returns the usage to record for a command: a review
// whose usage is all zeros reports nothing, so it records null.
func NormalizeUsage(command string, u *Usage) *Usage {
	if command == "review" && u != nil && u.IsZero() {
		return nil
	}
	return u
}

// Event is what one provider output line reveals.
type Event struct {
	SessionID string    // non-empty when this event reveals the provider session
	Usage     *Usage    // non-nil on usage-bearing events
	ErrorMsg  string    // non-empty on error-bearing events
	Progress  *Progress // non-nil on events that show what the provider is doing
}

// Progress is one thing the provider did while it worked, as a person
// following the run would read it.
type Progress struct {
	Kind string // command (a command it started), message (text it wrote) or reasoning
	Text string
}

// ModelReporter is a provider that can tell, once a run is over, which model
// and effort it actually ran with, from its own records. The runner records
// them beside the requested ones. "" with a nil error means there is no
// record to read; an error means one could not be read.
type ModelReporter interface {
	UsedModel(env []string, sessionID string) (model, effort string, err error)
}

// SessionReader is implemented by a provider that can read one of its own
// sessions by id, including one the Engine did not start.
type SessionReader interface {
	// ReadSession reports what the Engine needs to run a turn on the session
	// id. ok is false when id names no session of this provider (a shape the
	// provider does not use for session ids, or no such session).
	ReadSession(env []string, id string) (s Session, ok bool, err error)
}

// Session is what a provider reports about one of its sessions.
type Session struct {
	Cwd    string  // the last turn's directory, else the session's own
	Model  string  // the last turn's model; empty when the session has no turn or it names none
	Effort string  // the last turn's effort; empty when none
	Parent *Parent // set only when the provider reaches this session through its parent
}

// Parent links a Child session to the session that spawned it.
type Parent struct {
	SessionID string // the parent's session id
	Address   string // the name the parent uses for this child
}

// Relayer is implemented by a provider whose sessions can pass a prompt to a
// Child session they spawned.
type Relayer interface {
	// RelayPrompt is the prompt that asks a parent session to pass prompt to
	// its child at address, wait for the child's final answer and reply with
	// that answer verbatim.
	RelayPrompt(address string, prompt []byte) []byte
}

// LiveUsageReporter is a provider that can tell what a run has used so far,
// from its own session records: the usage since the run started and the model
// in use. ok is false when there is nothing to report yet.
type LiveUsageReporter interface {
	LiveUsage(env []string, sessionID string, since time.Time) (u Usage, model string, ok bool, err error)
}

// ThreadUsageReporter is a provider whose turn usage is the session's running
// total rather than the turn's own. ThreadUsageBefore is the session's total
// up to the instant before, from its own session records; ok is false when
// the records hold none.
type ThreadUsageReporter interface {
	ThreadUsageBefore(env []string, sessionID string, before time.Time) (total Usage, ok bool, err error)
}

// ModelCatalog is a provider that can list the models it offers and names the
// namespace (the price list's litellm_provider) their entries use.
// CatalogModels and CatalogEntries run with the caller's environment; an error
// means the catalog could not be read, which leaves the models seen in the
// telemetry. CatalogModels lists the distinct non-empty ids; CatalogEntries
// lists every entry in the order the provider gives it.
type ModelCatalog interface {
	PriceNamespace() string
	CatalogModels(env []string) ([]string, error)
	CatalogEntries(env []string) ([]CatalogEntry, error)
}

// ErrBinaryMissing is what a catalog read wraps when the provider's binary is
// not on the PATH it was given, so a caller can tell a missing install from a
// catalog it could not read.
var ErrBinaryMissing = errors.New("not found on PATH")

// CatalogEntry is one model a provider's catalog lists, as the provider
// reports it.
type CatalogEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

// ImplicitCaching is a provider that caches prompts without being asked and
// reports no cache writes: the uncached input of a run is then taken as
// written when pricing it.
type ImplicitCaching interface {
	ImplicitCacheWrites() bool
}

// HasImplicitCacheWrites reports whether the named provider is registered and
// caches prompts implicitly. An unregistered provider, such as a model-call
// provider, or one without the capability, does not.
func HasImplicitCacheWrites(name string) bool {
	p, ok := Get(name)
	if !ok {
		return false
	}
	caching, ok := p.(ImplicitCaching)
	return ok && caching.ImplicitCacheWrites()
}

// HarnessReserver is a provider whose lean and bare levels set native config
// keys. HarnessReservedFlag reports whether a native passthrough token, in the
// form ReservedFlag receives, sets one of those keys at level; a caller must not
// pass it, because it would undo the level.
type HarnessReserver interface {
	HarnessReservedFlag(level, arg string) bool
}

// Provider adapts one agent CLI.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	ReservedFlag(arg string) (reserved bool)
	BuildPlan(req Request) (Plan, error)
	ParseEvent(line []byte) (Event, bool) // false: unparseable line
	VersionArgs() []string
}

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

// Register adds a provider to the registry. It panics on duplicate names.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[p.Name()]; dup {
		panic(fmt.Sprintf("provider %q registered twice", p.Name()))
	}
	registry[p.Name()] = p
}

// Get returns the named provider.
func Get(name string) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Names lists registered providers, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
