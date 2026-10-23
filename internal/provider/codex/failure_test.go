package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

// failureFixtures are the recorded Codex failure messages under
// testdata/codex, each with the class that message belongs to.
var failureFixtures = map[string]provider.FailureClass{
	"failure-rate-limit-retry-429.txt":           "rate-limit",
	"failure-rate-limit-usage-limit.txt":         "rate-limit",
	"failure-rate-limit-usage-limit-plus.txt":    "rate-limit",
	"failure-rate-limit-usage-limit-pro.txt":     "rate-limit",
	"failure-rate-limit-usage-limit-credits.txt": "rate-limit",
	"failure-rate-limit-usage-limit-admin.txt":   "rate-limit",
	"failure-quota-exceeded.txt":                 "quota",
	"failure-quota-plan-upgrade.txt":             "quota",
	"failure-quota-out-of-credits.txt":           "quota",
	"failure-quota-out-of-credits-owner.txt":     "quota",
	"failure-quota-spend-cap.txt":                "quota",
	"failure-quota-spend-cap-owner.txt":          "quota",
	"failure-auth-unauthorized.txt":              "auth",
	"failure-auth-token-logged-out.txt":          "auth",
	"failure-auth-token-refresh.txt":             "auth",
	"failure-auth-account-id.txt":                "auth",
}

func classifier(t *testing.T) provider.FailureClassifier {
	t.Helper()
	c, ok := codex.New().(provider.FailureClassifier)
	if !ok {
		t.Fatal("the codex adapter does not declare the failure port")
	}
	return c
}

func TestCodexClassifiesEachRecordedFailureMessage(t *testing.T) {
	c := classifier(t)
	for file, want := range failureFixtures {
		t.Run(file, func(t *testing.T) {
			text, err := os.ReadFile(filepath.Join("../../../testdata/codex", file))
			if err != nil {
				t.Fatal(err)
			}

			if got := c.ClassifyFailure(strings.TrimSpace(string(text))); got != want {
				t.Errorf("class = %q, want %q", got, want)
			}
		})
	}
}

func TestEveryRecordedFailureMessageIsListed(t *testing.T) {
	files, err := filepath.Glob("../../../testdata/codex/failure-*.txt")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob = %v, %v", files, err)
	}
	for _, f := range files {
		if _, ok := failureFixtures[filepath.Base(f)]; !ok {
			t.Errorf("%s has no expected class", filepath.Base(f))
		}
	}
}

func TestCodexClassifiesOrdinaryFailuresAsOther(t *testing.T) {
	cases := map[string]string{
		"a model refused for the account": "{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'no-such-model-xyz' model is not supported when using Codex with a ChatGPT account.\"}}",
		"a timeout":                       "the provider timed out after 5m0s",
		"a sandbox denial":                "sandbox denied exec error, exit code: 1, stdout: , stderr: Permission denied",
		"a missing binary":                "provider binary \"codex\" not found on PATH",
		"an empty text":                   "",
		"a server error after retries":    "Reconnecting... 5/5 (exceeded retry limit, last status: 500 Internal Server Error)",
	}
	c := classifier(t)
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.ClassifyFailure(text); got != "other" {
				t.Errorf("class = %q, want other", got)
			}
		})
	}
}
