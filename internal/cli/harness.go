package cli

import (
	"fmt"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// harnessInstructions replaces the provider's own base instructions at the
// lean and bare levels.
const harnessInstructions = "Follow the user's instructions. Be concise.\n"

// harnessFlag parses --harness: one of the three harness levels.
type harnessFlag struct{ target *string }

func (f harnessFlag) String() string {
	if f.target == nil {
		return ""
	}
	return *f.target
}

func (f harnessFlag) Set(raw string) error {
	if !provider.ValidHarness(raw) {
		return fmt.Errorf("--harness needs %q, %q or %q, got %q", provider.HarnessFull, provider.HarnessLean, provider.HarnessBare, raw)
	}
	*f.target = raw
	return nil
}

// harnessReserved reports whether a native token sets a key the provider sets
// itself at level. Full sets none, so every token passes through there.
func harnessReserved(prov provider.Provider, level, arg string) bool {
	if level == "" || level == provider.HarnessFull {
		return false
	}
	guard, ok := prov.(provider.HarnessReserver)
	return ok && guard.HarnessReservedFlag(level, arg)
}

// withHarnessInstructions points the request at the instructions file when its
// level asks for one. A dry run only names the file: it plans and writes
// nothing.
func (t *turn) withHarnessInstructions() error {
	if t.req.Harness == "" || t.req.Harness == provider.HarnessFull {
		return nil
	}
	t.req.HarnessInstructions = t.st.HarnessInstructionsPath()
	if t.flags.dryRun {
		return nil
	}
	return t.st.EnsureHarnessInstructions(harnessInstructions)
}
