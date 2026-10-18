// Package settings resolves the effective model, effort and sandbox of a run
// from the flags the caller passed, and records where each came from.
package settings

// Source values say where an effective setting came from.
const (
	SourceFlag            = "flag"
	SourceProfile         = "profile"
	SourceProviderDefault = "provider-default"
	// SourceDefault marks the engine's own read-only sandbox.
	SourceDefault = "default"
)

// DefaultSandbox applies when the caller passes none.
const DefaultSandbox = "read-only"

// Flags are the explicit values the caller passed; empty means not passed.
type Flags struct {
	Model   string
	Effort  string
	Sandbox string
	// EffortSource, when set, is the source recorded for a passed effort in
	// place of the one Resolve is given for the others.
	EffortSource string
}

// Resolved is the effective setting for each field and where it came from.
// An empty model or effort means the provider's own default applies.
type Resolved struct {
	Model         string
	ModelSource   string
	Effort        string
	EffortSource  string
	Sandbox       string
	SandboxSource string
}

// ValidSource reports whether a --settings-source value is one a caller may
// pass: the label recorded for the settings it passes.
func ValidSource(source string) bool { return source == SourceFlag || source == SourceProfile }

// Resolve takes a flag's value, else the provider's default, and for the
// sandbox the engine's read-only default. A value the caller passed is
// recorded with passedSource, an effort with f.EffortSource when that is set.
func Resolve(f Flags, passedSource string) Resolved {
	var r Resolved
	r.Model, r.ModelSource = pick(f.Model, passedSource, SourceProviderDefault)
	r.Effort, r.EffortSource = pick(f.Effort, firstSet(f.EffortSource, passedSource), SourceProviderDefault)
	r.Sandbox, r.SandboxSource = pick(f.Sandbox, passedSource, SourceDefault)
	if r.Sandbox == "" {
		r.Sandbox = DefaultSandbox
	}
	return r
}

func pick(flagValue, passedSource, unsetSource string) (value, source string) {
	if flagValue != "" {
		return flagValue, passedSource
	}
	return "", unsetSource
}

func firstSet(source, fallback string) string {
	if source != "" {
		return source
	}
	return fallback
}
