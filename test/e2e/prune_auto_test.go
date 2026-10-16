package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stampPath(s *sandbox) string { return filepath.Join(s.home, "prune.stamp") }

func writeStamp(t *testing.T, s *sandbox, text string) {
	t.Helper()
	if err := os.WriteFile(stampPath(s), []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stampAgo(d time.Duration) string { return time.Now().UTC().Add(-d).Format(time.RFC3339) }

// seedOldRuns writes n finished runs that ended 40 days ago.
func seedOldRuns(t *testing.T, s *sandbox, prefix string, n int) {
	t.Helper()
	old := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339)
	for i := 0; i < n; i++ {
		seedRunFilesUnindexed(t, s, fmt.Sprintf("%s-%02d", prefix, i), map[string]any{"ended_at": old}, nil, nil)
	}
}

func countRunDirs(t *testing.T, s *sandbox) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.home, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestAutoPruneRemovesOldRunsSilentlyAndStampsTheHome(t *testing.T) {
	s := twoRuns(t)
	r := s.run("prune", "--auto")
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("exit %d stdout %q stderr %q, want a silent success", r.code, r.stdout, r.stderr)
	}
	if runDirExists(s, "pr-old") || !runDirExists(s, "pr-new") {
		t.Error("expected the old run removed and the fresh one kept")
	}
	stamped, err := time.Parse(time.RFC3339, strings.TrimSpace(readFile(t, stampPath(s))))
	if err != nil || time.Since(stamped) > time.Minute {
		t.Errorf("stamp = %v, %v; want the time of this pass", stamped, err)
	}
}

func TestAutoPruneRunsAtMostOncePerDay(t *testing.T) {
	cases := []struct {
		name  string
		stamp string
		prune bool
	}{
		{"stamped an hour ago", stampAgo(time.Hour), false},
		{"stamped just under a day ago", stampAgo(23 * time.Hour), false},
		{"stamped over a day ago", stampAgo(25 * time.Hour), true},
		{"stamp in the future", time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339), true},
		{"stamp unreadable", "yesterday-ish", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := twoRuns(t)
			writeStamp(t, s, c.stamp)
			if r := s.run("prune", "--auto"); r.code != 0 || r.stdout != "" {
				t.Fatalf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
			}
			if pruned := !runDirExists(s, "pr-old"); pruned != c.prune {
				t.Errorf("old run pruned = %v, want %v", pruned, c.prune)
			}
			if got := strings.TrimSpace(readFile(t, stampPath(s))); (got != c.stamp) != c.prune {
				t.Errorf("stamp %q after the pass, started as %q; rewritten only when a pass ran", got, c.stamp)
			}
		})
	}
}

func TestAutoPruneIsOffWithTheOptOut(t *testing.T) {
	s := twoRuns(t).set("MSGATE_NO_PRUNE", "1")
	r := s.run("prune", "--auto")
	if r.code != 0 || r.stdout != "" || r.stderr != "" || !runDirExists(s, "pr-old") || exists(stampPath(s)) {
		t.Errorf("exit %d stdout %q stderr %q old run kept %v stamp %v; want a no-op",
			r.code, r.stdout, r.stderr, runDirExists(s, "pr-old"), exists(stampPath(s)))
	}
}

func TestAutoPruneLeavesAnEmptyHomeAlone(t *testing.T) {
	s := newSandbox(t)
	if r := s.run("prune", "--auto"); r.code != 0 || r.stdout != "" || r.stderr != "" || exists(s.home) {
		t.Errorf("exit %d stdout %q stderr %q home created %v; want nothing done", r.code, r.stdout, r.stderr, exists(s.home))
	}
}

func TestAutoPruneYieldsToAPassAlreadyRunning(t *testing.T) {
	s := twoRuns(t)
	defer holdLock(t, filepath.Join(s.home, "prune.lock"))()
	r := s.run("prune", "--auto")
	if r.code != 0 || r.stderr != "" || !runDirExists(s, "pr-old") || exists(stampPath(s)) {
		t.Errorf("exit %d stderr %q old run kept %v stamp %v; want a quiet no-op", r.code, r.stderr, runDirExists(s, "pr-old"), exists(stampPath(s)))
	}
}

func TestAutoPruneDryRunTouchesNothing(t *testing.T) {
	s := twoRuns(t)
	r := s.run("prune", "--auto", "--dry-run", "--json")
	checkFields(t, "auto dry-run json", r.json(t), map[string]any{"dry_run": true, "ran": true, "removed": float64(1)})
	if !runDirExists(s, "pr-old") || exists(stampPath(s)) {
		t.Error("a dry run removed a run or wrote the stamp")
	}
}

func TestAutoPruneJSONReportsANoOp(t *testing.T) {
	s := twoRuns(t)
	writeStamp(t, s, stampAgo(time.Hour))
	r := s.run("prune", "--auto", "--json")
	checkFields(t, "auto json", r.json(t), map[string]any{"sdk_status": "ok", "ran": false, "removed": float64(0)})
}

func assertSilentSuccess(t *testing.T, r result) {
	t.Helper()
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("exit %d stdout %q stderr %q, want a silent success", r.code, r.stdout, r.stderr)
	}
}

// The pass stops cleanly when its time budget is spent and the next day's
// pass picks up the rest.
func TestAutoPruneStopsAtItsTimeBudgetAndResumesLater(t *testing.T) {
	s := newSandbox(t).set("MSGATE_TEST_PRUNE_BUDGET_MS", "150").set("MSGATE_TEST_PRUNE_STEP_MS", "60")
	seedOldRuns(t, s, "bud", 12)
	assertSilentSuccess(t, s.run("prune", "--auto"))
	left := countRunDirs(t, s)
	if left == 0 || left == 12 {
		t.Fatalf("%d of 12 runs left, want the pass cut short with some removed", left)
	}
	assertSilentSuccess(t, s.run("prune", "--auto"))
	if got := countRunDirs(t, s); got != left {
		t.Errorf("a second pass on the same day changed %d -> %d runs", left, got)
	}
	writeStamp(t, s, stampAgo(25*time.Hour))
	s.set("MSGATE_TEST_PRUNE_BUDGET_MS", "60000").set("MSGATE_TEST_PRUNE_STEP_MS", "1")
	assertSilentSuccess(t, s.run("prune", "--auto"))
	if got := countRunDirs(t, s); got != 0 {
		t.Errorf("%d runs left, want the next day's pass to finish", got)
	}
}

func TestAutoPruneSaysSoWhenItFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind root")
	}
	s := twoRuns(t)
	runs := filepath.Join(s.home, "runs")
	if err := os.Chmod(runs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(runs, 0o700) })
	r := s.run("prune", "--auto")
	if r.code != 70 || r.stderr == "" {
		t.Errorf("exit %d stderr %q, want exit 70 and a message", r.code, r.stderr)
	}
}
