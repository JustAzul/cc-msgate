package profile_test

import (
	"testing"

	"github.com/JustAzul/cc-msgate/internal/profile"
)

func TestResolveCodexBuiltIns(t *testing.T) {
	cases := []struct {
		scenario, model, effort, sandbox string
	}{
		{"second-opinion", "gpt-6.1-sol", "high", "read-only"},
		{"code-review", "gpt-6.1-sol", "high", "read-only"},
		{"cross-check", "gpt-6.1-sol", "high", "read-only"},
		{"expert-persona", "gpt-6.1-sol", "xhigh", "read-only"},
		{"delegation", "gpt-6.1-sol", "medium", "workspace-write"},
	}
	for _, c := range cases {
		got := profile.Resolve("codex", c.scenario, profile.Flags{})
		want := profile.Resolved{
			Model: c.model, ModelSource: "profile", Effort: c.effort, EffortSource: "profile",
			Sandbox: c.sandbox, SandboxSource: "profile",
		}
		if got != want {
			t.Errorf("%s: got %+v want %+v", c.scenario, got, want)
		}
	}
}

func TestResolveNoProfile(t *testing.T) {
	for _, scenario := range []string{"adhoc", "claude-md-update", ""} {
		got := profile.Resolve("codex", scenario, profile.Flags{})
		want := profile.Resolved{ModelSource: "provider-default", EffortSource: "provider-default", SandboxSource: "provider-default"}
		if got != want {
			t.Errorf("%q: got %+v want %+v", scenario, got, want)
		}
	}
	// A provider without profiles never gets codex's.
	if got := profile.Resolve("other", "delegation", profile.Flags{}); got.Model != "" || got.ModelSource != "provider-default" {
		t.Errorf("other provider: %+v", got)
	}
}

func TestResolveFlagBeatsProfile(t *testing.T) {
	got := profile.Resolve("codex", "delegation", profile.Flags{Effort: "high", Model: "m"})
	want := profile.Resolved{
		Model: "m", ModelSource: "flag", Effort: "high", EffortSource: "flag",
		Sandbox: "workspace-write", SandboxSource: "profile",
	}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
}
