package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/cli"
	"github.com/JustAzul/cc-msgate/internal/provider"
)

// noReviewProvider declares exec support only.
type noReviewProvider struct{}

func (noReviewProvider) Name() string { return "testprov" }
func (noReviewProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Commands: []string{"exec"}, Sandboxes: []string{"read-only"}}
}
func (noReviewProvider) ReservedFlag(string) bool { return false }
func (noReviewProvider) BuildPlan(provider.Request) (provider.Plan, error) {
	return provider.Plan{Argv: []string{"testprov"}}, nil
}
func (noReviewProvider) ParseEvent([]byte) (provider.Event, bool) { return provider.Event{}, true }
func (noReviewProvider) VersionArgs() []string                    { return nil }

func TestReviewRefusedWhenProviderCannotReview(t *testing.T) {
	provider.Register(noReviewProvider{})
	home := t.TempDir()
	var out, errb bytes.Buffer
	code := cli.Run([]string{"msgate", "review", "--provider", "testprov", "--base", "main"},
		[]string{"MSGATE_HOME=" + home}, strings.NewReader(""), &out, &errb)
	if code != cli.ExitUsage {
		t.Fatalf("exit %d, want %d (stderr %q)", code, cli.ExitUsage, errb.String())
	}
	if !strings.Contains(errb.String(), "does not support the review command") {
		t.Errorf("stderr does not name the missing capability: %q", errb.String())
	}
}
