package provider_test

import (
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

func TestCapabilitiesUnsupportedNamesTheCapability(t *testing.T) {
	caps := provider.Capabilities{
		Commands:      []string{"exec"},
		ReviewTargets: []string{"base"},
		Sandboxes:     []string{"read-only"},
	}
	cases := []struct {
		name  string
		needs provider.Needs
		want  string // substring of the error; empty means supported
	}{
		{"exec ok", provider.Needs{Command: "exec", Sandbox: "read-only"}, ""},
		{"no sandbox ok", provider.Needs{Command: "exec"}, ""},
		{"review command", provider.Needs{Command: "review"}, "review"},
		{"resume command", provider.Needs{Command: "resume"}, "resume"},
		{"sandbox mode", provider.Needs{Command: "exec", Sandbox: "danger-full-access"}, "danger-full-access"},
		{"review target", provider.Needs{Command: "exec", ReviewTarget: "commit"}, "commit"},
	}
	for _, c := range cases {
		err := caps.Unsupported(c.needs)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: error %v does not name %q", c.name, err, c.want)
		}
	}
}

func TestCapabilitiesRefuseAHarnessLevelTheyDoNotList(t *testing.T) {
	caps := provider.Capabilities{Commands: []string{"exec"}, Harnesses: []string{"lean"}}
	cases := []struct {
		level   string
		refused bool
	}{
		{"", false},
		{"full", false},
		{"lean", false},
		{"bare", true},
	}
	for _, c := range cases {
		err := caps.Unsupported(provider.Needs{Command: "exec", Harness: c.level})
		if (err != nil) != c.refused {
			t.Errorf("level %q: refused = %v (err %v), want %v", c.level, err != nil, err, c.refused)
		}
	}
	if err := (provider.Capabilities{Commands: []string{"exec"}}).Unsupported(provider.Needs{Command: "exec", Harness: "lean"}); err == nil {
		t.Error("a provider that lists no harness level accepted lean")
	}
}
