package e2e

import (
	"reflect"
	"testing"
	"time"
)

func TestProvidersWithoutACacheReadsAllAvailableAndStartsOneRefresh(t *testing.T) {
	s, log := healthSandbox(t)

	r := s.run("providers", "--json")

	got := listedProviders(t, r)
	for _, name := range []string{"anthropic", "codex"} {
		want := map[string]any{"provider": name, "available": true, "reason": "", "checked_at": nil, "resets_at": nil, "source": "none"}
		if !reflect.DeepEqual(got[name], want) {
			t.Errorf("%s = %v, want %v", name, got[name], want)
		}
	}
	if len(got) != 2 {
		t.Errorf("providers = %v, want exactly anthropic and codex", got)
	}
	if models, ok := r.json(t)["summary_models"].([]any); !ok || len(models) != 0 {
		t.Errorf("summary_models = %v, want []", r.json(t)["summary_models"])
	}
	if n := logCount(log, "spawned"); n != 1 {
		t.Errorf("refreshes started = %d, want 1", n)
	}
}

func TestProvidersAnswersFromTheCacheAndStartsARefreshOnlyForAStaleEntry(t *testing.T) {
	cases := []struct {
		name      string
		codex     map[string]any
		wantAvail bool
		wantSpawn int
	}{
		{"available, 4 minutes old", cacheEntry(true, "", "check", 4*time.Minute, nil), true, 0},
		{"available, 6 minutes old", cacheEntry(true, "", "check", 6*time.Minute, nil), true, 1},
		{"unavailable, 30 seconds old", cacheEntry(false, "rate-limited", "check", 30*time.Second, nil), false, 0},
		{"unavailable, 2 minutes old", cacheEntry(false, "rate-limited", "check", 2*time.Minute, nil), false, 1},
		{"mark that has not reset, 10 minutes old", cacheEntry(false, "rate-limited", "mark", 10*time.Minute, inFuture(time.Hour)), false, 0},
		{"mark that has reset, 2 minutes old", cacheEntry(false, "rate-limited", "mark", 2*time.Minute, inFuture(-time.Minute)), false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, log := healthSandbox(t)
			seedProviders(t, s, map[string]map[string]any{
				"codex":     c.codex,
				"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
			})

			got := listedProviders(t, s.run("providers", "--json"))

			if got["codex"]["available"] != c.wantAvail || got["codex"]["reason"] != c.codex["reason"] || got["codex"]["source"] != c.codex["source"] {
				t.Errorf("codex = %v, want the cached %v", got["codex"], c.codex)
			}
			if got["codex"]["checked_at"] != c.codex["checked_at"] || got["codex"]["resets_at"] != c.codex["resets_at"] {
				t.Errorf("codex times = %v / %v, want the cached %v / %v", got["codex"]["checked_at"], got["codex"]["resets_at"], c.codex["checked_at"], c.codex["resets_at"])
			}
			if n := logCount(log, "spawned"); n != c.wantSpawn {
				t.Errorf("refreshes started = %d, want %d", n, c.wantSpawn)
			}
		})
	}
}

func TestProvidersReadsAMissingEntryAsAvailableAndStartsARefresh(t *testing.T) {
	s, log := healthSandbox(t)
	seedProviders(t, s, map[string]map[string]any{"codex": cacheEntry(false, "not-logged-in", "check", 10*time.Second, nil)})

	got := listedProviders(t, s.run("providers", "--json"))

	if got["codex"]["available"] != false || got["codex"]["reason"] != "not-logged-in" {
		t.Errorf("codex = %v, want the cached not-logged-in", got["codex"])
	}
	if got["anthropic"]["available"] != true || got["anthropic"]["source"] != "none" {
		t.Errorf("anthropic = %v, want available with source none", got["anthropic"])
	}
	if n := logCount(log, "spawned"); n != 1 {
		t.Errorf("refreshes started = %d, want 1", n)
	}
}

func TestProvidersTextPrintsOneLinePerProvider(t *testing.T) {
	s, _ := healthSandbox(t)
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(false, "rate-limited", "check", 10*time.Second, nil),
		"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
	})

	r := s.run("providers")

	if r.code != 0 || len(r.lines()) != 2 {
		t.Fatalf("exit %d, lines %q; want exit 0 and 2 lines", r.code, r.lines())
	}
}

func TestProvidersRefusesArgumentsItDoesNotTake(t *testing.T) {
	s, log := healthSandbox(t)
	for _, args := range [][]string{{"providers", "extra"}, {"providers", "--nope"}, {"providers", "refresh", "extra"}, {"providers", "refresh", "--json"}} {
		if r := s.run(args...); r.code != 2 {
			t.Errorf("%v: exit %d, want 2; stderr %q", args, r.code, r.stderr)
		}
	}
	if n := logCount(log, "spawned"); n != 0 {
		t.Errorf("refreshes started = %d, want 0", n)
	}
}

func TestProvidersCarriesSkippedOfAnEntryThatHasIt(t *testing.T) {
	s, _ := healthSandbox(t)
	withSkipped := cacheEntry(true, "", "check", 10*time.Second, nil)
	withSkipped["skipped"] = []string{"app-server: timed out", "status page: unreachable"}
	seedProviders(t, s, map[string]map[string]any{
		"codex":     withSkipped,
		"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
	})

	got := listedProviders(t, s.run("providers", "--json"))

	if want := []any{"app-server: timed out", "status page: unreachable"}; !reflect.DeepEqual(got["codex"]["skipped"], want) {
		t.Errorf("codex skipped = %v, want %v", got["codex"]["skipped"], want)
	}
	if _, present := got["anthropic"]["skipped"]; present {
		t.Errorf("anthropic = %v, want no skipped key", got["anthropic"])
	}
}
