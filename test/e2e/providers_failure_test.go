package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failureText is a recorded Codex failure message, as Codex prints it.
func failureText(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "codex", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data)) + "\n"
}

// failingSandbox is a health sandbox whose Codex fails with the recorded
// message named, and whose cache already holds a fresh, available entry for
// each provider, so no reader starts a refresh of its own.
func failingSandbox(t *testing.T, message string) (*sandbox, string) {
	t.Helper()
	s, log := healthSandbox(t)
	s.set("FAKECODEX_EXIT", "1").set("FAKECODEX_STDERR", failureText(t, message))
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(true, "", "check", 10*time.Second, nil),
		"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
	})
	return s, log
}

// wantCodexMarked fails the test unless `providers --json` shows codex as
// marked unavailable for reason, with no end time, and started no refresh.
func wantCodexMarked(t *testing.T, s *sandbox, log, reason string) {
	t.Helper()
	got := listedProviders(t, s.run("providers", "--json"))
	c := got["codex"]
	if c["available"] != false || c["reason"] != reason || c["source"] != "mark" || c["resets_at"] != nil {
		t.Errorf("codex = %v, want unavailable, reason %s, source mark, no resets_at", c, reason)
	}
	if a := got["anthropic"]; a["available"] != true || a["source"] != "check" {
		t.Errorf("anthropic = %v, want it left as it was", a)
	}
	if n := logCount(log, "spawned"); n != 0 {
		t.Errorf("refreshes started = %d, want 0", n)
	}
}

// wantCodexUntouched fails the test unless codex is still the available,
// checked entry the cache held before the run.
func wantCodexUntouched(t *testing.T, s *sandbox, log string) {
	t.Helper()
	c := listedProviders(t, s.run("providers", "--json"))["codex"]
	if c["available"] != true || c["reason"] != "" || c["source"] != "check" {
		t.Errorf("codex = %v, want the available check entry it was", c)
	}
	if n := logCount(log, "spawned"); n != 0 {
		t.Errorf("refreshes started = %d, want 0", n)
	}
}

func TestAFailedRunMarksCodexDownForTheReasonOfItsError(t *testing.T) {
	cases := []struct{ message, reason string }{
		{"failure-rate-limit-retry-429", "rate-limited"},
		{"failure-rate-limit-usage-limit-plus", "rate-limited"},
		{"failure-quota-out-of-credits", "quota-exhausted"},
		{"failure-quota-exceeded", "quota-exhausted"},
		{"failure-auth-unauthorized", "not-logged-in"},
		{"failure-auth-token-refresh", "not-logged-in"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			s, log := failingSandbox(t, tc.message)

			r := s.run("exec", "--run-id", "f1", "q")

			if r.code != 1 {
				t.Errorf("exit %d, want the provider's 1; stderr %q", r.code, r.stderr)
			}
			wantCodexMarked(t, s, log, tc.reason)
		})
	}
}

func TestAnOrdinaryFailureMarksNothing(t *testing.T) {
	s, log := healthSandbox(t)
	s.set("FAKECODEX_FIXTURE", fixture("error-400")).set("FAKECODEX_EXIT", "1")
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(true, "", "check", 10*time.Second, nil),
		"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
	})

	r := s.run("exec", "--run-id", "o1", "q")

	if r.code != 1 {
		t.Errorf("exit %d, want 1; stderr %q", r.code, r.stderr)
	}
	wantCodexUntouched(t, s, log)
}

func TestARunThatDidNotFailMarksNothing(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		args []string
		code int
	}{
		"a success that printed the text": {map[string]string{"FAKECODEX_EXIT": "0"}, []string{"exec", "--run-id", "n1", "q"}, 0},
		"a timeout":                       {map[string]string{"FAKECODEX_SLEEP_MS": "5000"}, []string{"exec", "--timeout", "1", "--run-id", "n2", "q"}, 124},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, log := failingSandbox(t, "failure-rate-limit-usage-limit")
			for k, v := range tc.env {
				s.set(k, v)
			}

			r := s.run(tc.args...)

			if r.code != tc.code {
				t.Errorf("exit %d, want %d; stderr %q", r.code, tc.code, r.stderr)
			}
			wantCodexUntouched(t, s, log)
		})
	}
}

func TestAFailedBackgroundRunMarksCodexDown(t *testing.T) {
	s, log := failingSandbox(t, "failure-rate-limit-retry-429")

	if r := s.run("exec", "--background", "--run-id", "b1", "q"); r.code != 0 {
		t.Fatalf("exec: exit %d, stderr %q", r.code, r.stderr)
	}
	if w := s.run("wait", "b1"); w.code != 1 {
		t.Fatalf("wait: exit %d, want the run's 1; stderr %q", w.code, w.stderr)
	}

	wantCodexMarked(t, s, log, "rate-limited")
}

func TestAFailedSendMarksCodexDown(t *testing.T) {
	s, log := failingSandbox(t, "failure-auth-token-logged-out")
	s.set("FAKECODEX_EXIT", "0").set("FAKECODEX_STDERR", "").set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "s1")
	s.set("FAKECODEX_EXIT", "1").set("FAKECODEX_STDERR", failureText(t, "failure-auth-token-logged-out"))

	r := s.run("send", conv, "--run-id", "s2", "again")

	if r.code != 1 {
		t.Errorf("exit %d, want the provider's 1; stderr %q", r.code, r.stderr)
	}
	wantCodexMarked(t, s, log, "not-logged-in")
}

func TestAFailedReviewMarksCodexDown(t *testing.T) {
	s, log := failingSandbox(t, "failure-quota-spend-cap")

	r := s.run("review", "--uncommitted", "--cwd", gitRepo(t), "--run-id", "rv1")

	if r.code != 1 {
		t.Errorf("exit %d, want the provider's 1; stderr %q", r.code, r.stderr)
	}
	wantCodexMarked(t, s, log, "quota-exhausted")
}

func TestAMarkThatCannotBeWrittenOnlyWarns(t *testing.T) {
	s, _ := failingSandbox(t, "failure-rate-limit-usage-limit")
	if err := os.MkdirAll(filepath.Join(s.home, "providers.write.lock"), 0o700); err != nil {
		t.Fatal(err)
	}

	r := s.run("exec", "--run-id", "w1", "q")

	if r.code != 1 {
		t.Errorf("exit %d, want the provider's 1; stderr %q", r.code, r.stderr)
	}
	if w := warningLines(r.stderr); len(w) != 1 {
		t.Errorf("warnings = %q, want exactly one", w)
	}
	if st := stateOf(t, s, "w1"); st["state"] != "failed" || st["outcome"] != "error" {
		t.Errorf("state = %v, outcome = %v, want failed and error", st["state"], st["outcome"])
	}
}
