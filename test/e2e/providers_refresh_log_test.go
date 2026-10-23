package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// unwritableProviders makes every write of the providers cache fail: the path
// of its write lock is a directory.
func unwritableProviders(t *testing.T, s *sandbox) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(s.home, "providers.write.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// A refresh started in the background keeps what it reports in a log in the
// home, so one that could not write the cache can be told from one that never
// ran.
func TestABackgroundRefreshThatFailsLeavesItsReportInTheLog(t *testing.T) {
	s, log := healthSandbox(t)
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(true, "", "check", 10*time.Minute, nil),
		"anthropic": cacheEntry(true, "", "check", 10*time.Minute, nil),
	})
	unwritableProviders(t, s)
	if r := s.run("providers", "--json"); r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	if !waitFor(func() bool { return logCount(log, "failed") == 1 }) {
		t.Fatalf("the refresh did not fail as set up; log:\n%s", logText(log))
	}
	if info, err := os.Stat(filepath.Join(s.home, "providers.refresh.log")); err != nil || info.Size() == 0 {
		t.Errorf("providers.refresh.log: %v, want a file holding the refresh's report", err)
	}
}

// A refresh run by hand reports on its own stderr and leaves no log.
func TestARefreshRunByHandReportsOnItsStderr(t *testing.T) {
	s, _ := healthSandbox(t)
	unwritableProviders(t, s)
	r := s.run("providers", "refresh")
	if r.code != 70 || r.stderr == "" {
		t.Errorf("exit %d, stderr %q; want exit 70 and a report on stderr", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(s.home, "providers.refresh.log")); !os.IsNotExist(err) {
		t.Errorf("providers.refresh.log: %v, want none", err)
	}
}
