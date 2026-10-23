package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// statusPage serves a Statuspage summary with the given components.
func statusPage(t *testing.T, status int, components string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(`{"components":[` + components + `]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAMarkWrittenByOneProcessIsReadByAnother(t *testing.T) {
	s, log := healthSandbox(t)
	seedProviders(t, s, map[string]map[string]any{"codex": cacheEntry(true, "", "check", 10*time.Second, nil)})
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Second)

	mark := s.run("providers", "mark", "anthropic", "--reason", "rate-limited", "--until", until.Format(time.RFC3339))
	got := listedProviders(t, s.run("providers", "--json"))

	if mark.code != 0 {
		t.Fatalf("mark: exit %d, stderr %q", mark.code, mark.stderr)
	}
	a := got["anthropic"]
	if a["available"] != false || a["reason"] != "rate-limited" || a["source"] != "mark" || a["resets_at"] != until.Format(time.RFC3339) {
		t.Errorf("anthropic = %v, want unavailable, rate-limited, source mark, resets_at %s", a, until.Format(time.RFC3339))
	}
	if n := logCount(log, "spawned"); n != 0 {
		t.Errorf("refreshes started = %d, want 0: a mark that has not reset is not stale", n)
	}
}

func TestAMarkWithoutUntilHasNoReset(t *testing.T) {
	s, _ := healthSandbox(t)

	if r := s.run("providers", "mark", "codex", "--reason", "quota-exhausted"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}

	c := cachedProviders(t, s)["codex"]
	if c["available"] != false || c["reason"] != "quota-exhausted" || c["source"] != "mark" || c["resets_at"] != nil || c["checked_at"] == "" {
		t.Errorf("codex = %v, want an unavailable quota-exhausted mark with no resets_at", c)
	}
}

func TestMarkRefusesWhatItCannotWrite(t *testing.T) {
	cases := map[string][]string{
		"unknown provider": {"providers", "mark", "nope", "--reason", "rate-limited"},
		"unknown reason":   {"providers", "mark", "codex", "--reason", "tired"},
		"reason unknown":   {"providers", "mark", "codex", "--reason", "unknown"},
		"no reason":        {"providers", "mark", "codex"},
		"no provider":      {"providers", "mark", "--reason", "rate-limited"},
		"two providers":    {"providers", "mark", "codex", "anthropic", "--reason", "rate-limited"},
		"bad until":        {"providers", "mark", "codex", "--reason", "rate-limited", "--until", "tomorrow"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := healthSandbox(t)
			if r := s.run(args...); r.code != 2 {
				t.Errorf("exit %d, want 2; stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if exists(filepath.Join(s.home, "providers.json")) {
				t.Error("providers.json was written")
			}
		})
	}
}

func TestRefreshWritesWhatTheChecksFound(t *testing.T) {
	s, _ := healthSandbox(t)
	openai := statusPage(t, http.StatusInternalServerError, "")
	anthropic := statusPage(t, http.StatusOK, `{"name":"Claude Code","status":"major_outage"},{"name":"Claude API (api.anthropic.com)","status":"operational"}`)
	s.set("MSGATE_STATUS_URL_OPENAI", openai).set("MSGATE_STATUS_URL_ANTHROPIC", anthropic)
	s.set("FAKECODEX_RATE_LIMITS", filepath.Join(repoRoot, "testdata", "codex", "rate-limits-ok.json"))

	r := s.run("providers", "refresh")

	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	got := cachedProviders(t, s)
	if len(got) != 2 {
		t.Fatalf("providers = %v, want codex and anthropic", got)
	}
	wantCodex := map[string]any{"available": true, "reason": "", "resets_at": nil, "source": "check", "skipped": []any{"status page: unreachable"}}
	wantAnthropic := map[string]any{"available": false, "reason": "provider-outage", "resets_at": nil, "source": "check"}
	for name, want := range map[string]map[string]any{"codex": wantCodex, "anthropic": wantAnthropic} {
		e := got[name]
		at, err := time.Parse(time.RFC3339, e["checked_at"].(string))
		if err != nil || time.Since(at) > time.Minute {
			t.Errorf("%s checked_at = %v, want a time within the last minute", name, e["checked_at"])
		}
		delete(e, "checked_at")
		if !reflect.DeepEqual(e, want) {
			t.Errorf("%s = %v, want %v", name, e, want)
		}
	}
}

func TestRefreshWithOnlyTheBinaryMissingMarksCodexNotInstalled(t *testing.T) {
	s, _ := healthSandbox(t)
	s.withoutProvider()

	if r := s.run("providers", "refresh"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}

	got := cachedProviders(t, s)
	if got["codex"]["available"] != false || got["codex"]["reason"] != "not-installed" {
		t.Errorf("codex = %v, want unavailable, not-installed", got["codex"])
	}
	if got["anthropic"]["available"] != true || got["anthropic"]["reason"] != "unknown" {
		t.Errorf("anthropic = %v, want available with the reason unknown (its page is off)", got["anthropic"])
	}
}

func TestRefreshKeepsAMarkThatHasNotResetAndReplacesTheOthers(t *testing.T) {
	cases := []struct {
		name       string
		mark       map[string]any
		wantSource string
	}{
		{"reset in the future", cacheEntry(false, "rate-limited", "mark", time.Minute, inFuture(time.Hour)), "mark"},
		{"reset in the past", cacheEntry(false, "rate-limited", "mark", time.Hour, inFuture(-time.Minute)), "check"},
		{"no reset", cacheEntry(false, "rate-limited", "mark", time.Hour, nil), "check"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := healthSandbox(t)
			seedProviders(t, s, map[string]map[string]any{"anthropic": c.mark})

			if r := s.run("providers", "refresh"); r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}

			got := cachedProviders(t, s)
			if got["anthropic"]["source"] != c.wantSource {
				t.Fatalf("anthropic = %v, want source %s", got["anthropic"], c.wantSource)
			}
			if c.wantSource == "mark" && !reflect.DeepEqual(got["anthropic"], c.mark) {
				t.Errorf("anthropic = %v, want the mark untouched: %v", got["anthropic"], c.mark)
			}
			if c.wantSource == "check" && got["anthropic"]["available"] != true {
				t.Errorf("anthropic = %v, want the check's available entry", got["anthropic"])
			}
			if got["codex"]["source"] != "check" {
				t.Errorf("codex = %v, want a fresh check", got["codex"])
			}
		})
	}
}

func TestTwoReadersOfAStaleEntryRunOneRefresh(t *testing.T) {
	s, log := healthSandbox(t)
	gate := filepath.Join(t.TempDir(), "gate")
	s.set("MSGATE_TEST_REFRESH_GATE", gate)

	first := s.run("providers", "--json")
	if !waitFor(func() bool { return exists(gate + ".reached") }) {
		t.Fatal("the first refresh never took the lock")
	}
	second := s.run("providers", "--json")
	if !waitFor(func() bool { return logCount(log, "busy") == 1 }) {
		t.Fatalf("the second refresh never found the lock held; log:\n%s", logText(log))
	}
	if err := os.WriteFile(gate+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return logCount(log, "ran") == 1 }) {
		t.Fatalf("the first refresh never finished; log:\n%s", logText(log))
	}

	if first.code != 0 || second.code != 0 {
		t.Errorf("readers exited %d and %d, want 0 and 0", first.code, second.code)
	}
	if spawned, ran, busy := logCount(log, "spawned"), logCount(log, "ran"), logCount(log, "busy"); spawned != 2 || ran != 1 || busy != 1 {
		t.Errorf("spawned %d, ran %d, busy %d; want 2, 1, 1", spawned, ran, busy)
	}
}
