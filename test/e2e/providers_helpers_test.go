package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// healthSandbox is a sandbox whose status pages are switched off and whose
// refreshes are logged: the reader writes "spawned" when it starts one, the
// refresh writes "ran" once it has written the cache, or "busy" when another
// refresh holds the lock. Before the test's home goes, it waits for every
// refresh it started to log its end, so none writes into a removed home.
func healthSandbox(t *testing.T) (*sandbox, string) {
	t.Helper()
	s := newSandbox(t).set("MSGATE_STATUS_URL_OPENAI", "off").set("MSGATE_STATUS_URL_ANTHROPIC", "off")
	log := filepath.Join(t.TempDir(), "refreshes.log")
	s.set("MSGATE_TEST_REFRESH_LOG", log)
	t.Cleanup(func() {
		if !waitFor(func() bool { return logCount(log, "spawned") <= logCount(log, "ran")+logCount(log, "busy") }) {
			t.Errorf("a refresh started by a reader never finished; log:\n%s", logText(log))
		}
	})
	return s, log
}

// waitFor polls cond until it holds or ten seconds pass.
func waitFor(cond func() bool) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func logText(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// logCount is how many lines of the refresh log are word.
func logCount(path, word string) int {
	n := 0
	for _, line := range strings.Split(logText(path), "\n") {
		if line == word {
			n++
		}
	}
	return n
}

// cacheEntry is one provider entry of providers.json as the CLI writes it.
func cacheEntry(available bool, reason, source string, checkedAgo time.Duration, resetsIn *time.Duration) map[string]any {
	e := map[string]any{
		"available":  available,
		"reason":     reason,
		"checked_at": time.Now().UTC().Add(-checkedAgo).Format(time.RFC3339),
		"resets_at":  nil,
		"source":     source,
	}
	if resetsIn != nil {
		e["resets_at"] = time.Now().UTC().Add(*resetsIn).Truncate(time.Second).Format(time.RFC3339)
	}
	return e
}

func inFuture(d time.Duration) *time.Duration { return &d }

// seedProviders writes providers.json into the sandbox home.
func seedProviders(t *testing.T, s *sandbox, entries map[string]map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": 1, "providers": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.home, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// cachedProviders reads the provider entries of providers.json.
func cachedProviders(t *testing.T, s *sandbox) map[string]map[string]any {
	t.Helper()
	file := readJSONFile(t, filepath.Join(s.home, "providers.json"))
	if file["version"] != float64(1) {
		t.Errorf("providers.json version = %v, want 1", file["version"])
	}
	out := map[string]map[string]any{}
	for name, e := range file["providers"].(map[string]any) {
		out[name] = e.(map[string]any)
	}
	return out
}

// listedProviders is the providers array of `providers --json`, by name.
func listedProviders(t *testing.T, r result) map[string]map[string]any {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	out := map[string]map[string]any{}
	for _, e := range r.json(t)["providers"].([]any) {
		m := e.(map[string]any)
		out[m["provider"].(string)] = m
	}
	return out
}
