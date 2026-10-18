package settings_test

import (
	"testing"

	"github.com/JustAzul/cc-msgate/internal/settings"
)

func TestResolveWithNothingPassed(t *testing.T) {
	got := settings.Resolve(settings.Flags{}, settings.SourceFlag)

	want := settings.Resolved{
		ModelSource: "provider-default", EffortSource: "provider-default",
		Sandbox: "read-only", SandboxSource: "default",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveRecordsPassedValuesWithTheGivenSource(t *testing.T) {
	for _, source := range []string{settings.SourceFlag, settings.SourceProfile} {
		got := settings.Resolve(settings.Flags{Model: "m", Effort: "high", Sandbox: "workspace-write"}, source)

		want := settings.Resolved{
			Model: "m", ModelSource: source, Effort: "high", EffortSource: source,
			Sandbox: "workspace-write", SandboxSource: source,
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", source, got, want)
		}
	}
}

func TestResolveMixesPassedAndUnpassed(t *testing.T) {
	got := settings.Resolve(settings.Flags{Model: "m"}, settings.SourceProfile)

	if got.ModelSource != "profile" || got.EffortSource != "provider-default" || got.SandboxSource != "default" {
		t.Errorf("sources = %s/%s/%s", got.ModelSource, got.EffortSource, got.SandboxSource)
	}
}

func TestResolveRecordsTheEffortWithItsOwnSource(t *testing.T) {
	got := settings.Resolve(settings.Flags{Model: "m", Effort: "low", Sandbox: "workspace-write", EffortSource: settings.SourceFlag}, settings.SourceProfile)

	if got.ModelSource != "profile" || got.EffortSource != "flag" || got.SandboxSource != "profile" {
		t.Errorf("sources = %s/%s/%s", got.ModelSource, got.EffortSource, got.SandboxSource)
	}
}

func TestResolveLeavesAnUnpassedEffortWithTheProviderDefault(t *testing.T) {
	got := settings.Resolve(settings.Flags{Model: "m", EffortSource: settings.SourceFlag}, settings.SourceProfile)

	if got.Effort != "" || got.EffortSource != "provider-default" {
		t.Errorf("effort = %q from %s", got.Effort, got.EffortSource)
	}
}

func TestValidSource(t *testing.T) {
	for source, want := range map[string]bool{"flag": true, "profile": true, "default": false, "provider-default": false, "": false} {
		if got := settings.ValidSource(source); got != want {
			t.Errorf("ValidSource(%q) = %v, want %v", source, got, want)
		}
	}
}
