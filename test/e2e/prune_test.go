package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ageRun rewrites a finished run's ended_at to the given number of days ago.
func ageRun(t *testing.T, s *sandbox, id string, days int) {
	t.Helper()
	path := filepath.Join(s.home, "runs", id, "state.json")
	st := readJSONFile(t, path)
	st["ended_at"] = time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runDirExists(s *sandbox, id string) bool { return exists(filepath.Join(s.home, "runs", id)) }

// twoRuns leaves a 40-day-old run "pr-old" and a fresh run "pr-new".
func twoRuns(t *testing.T) *sandbox {
	t.Helper()
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	seedRun(t, s, "pr-old", "--session-id", "S")
	seedRun(t, s, "pr-new", "--session-id", "S")
	ageRun(t, s, "pr-old", 40)
	return s
}

func TestPruneRemovesOldRunsAndSummarisesJSON(t *testing.T) {
	s := twoRuns(t)
	r := s.run("prune", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	j := r.json(t)
	checkFields(t, "prune json", j, map[string]any{
		"sdk_status": "ok", "exit_code": float64(0), "dry_run": false, "older_than_days": float64(30),
		"removed": float64(1), "kept": float64(1), "skipped": float64(0),
	})
	if freed, _ := j["bytes_freed"].(float64); freed <= 0 {
		t.Errorf("bytes_freed = %v, want the size of the removed run", j["bytes_freed"])
	}
	if runDirExists(s, "pr-old") || !runDirExists(s, "pr-new") {
		t.Error("expected the old run removed and the fresh one kept")
	}
}

func TestPruneTextSummary(t *testing.T) {
	s := twoRuns(t)
	r := s.run("prune")
	for _, want := range []string{"removed 1", "kept 1", "skipped 0", "freed"} {
		if r.code != 0 || !strings.Contains(r.stdout, want) {
			t.Errorf("exit %d stdout %q lacks %q", r.code, r.stdout, want)
		}
	}
}

func TestPruneDryRunRemovesNothing(t *testing.T) {
	s := twoRuns(t)
	r := s.run("prune", "--dry-run", "--json")
	checkFields(t, "dry-run json", r.json(t), map[string]any{"dry_run": true, "removed": float64(1), "kept": float64(1)})
	if !runDirExists(s, "pr-old") {
		t.Error("a dry run removed a run")
	}
	if txt := s.run("prune", "--dry-run"); !strings.Contains(txt.stdout, "would remove 1") {
		t.Errorf("dry-run text = %q", txt.stdout)
	}
}

func TestPruneKeepsTelemetryAndConversations(t *testing.T) {
	s := twoRuns(t)
	records := len(telemetryRecords(t, s.home))
	convs, _ := filepath.Glob(filepath.Join(s.home, "conversations", "*.json"))
	s.run("prune")
	after, _ := filepath.Glob(filepath.Join(s.home, "conversations", "*.json"))
	if got := len(telemetryRecords(t, s.home)); got != records || len(after) != len(convs) {
		t.Errorf("telemetry %d -> %d, conversations %d -> %d", records, got, len(convs), len(after))
	}
}

func TestPruneRetentionPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		args    []string
		removed float64
	}{
		{"default is 30 days", "", nil, 1},
		{"flag raises it", "", []string{"--older-than", "60"}, 0},
		{"flag lowers it", "", []string{"--older-than", "10"}, 1},
		{"env raises it", "60", nil, 0},
		{"flag beats env", "60", []string{"--older-than", "10"}, 1},
		{"zero removes every finished run", "", []string{"--older-than", "0"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := twoRuns(t)
			if c.env != "" {
				s.set("MSGATE_RETENTION_DAYS", c.env)
			}
			r := s.run(append([]string{"prune", "--json"}, c.args...)...)
			if got := r.json(t)["removed"]; r.code != 0 || got != c.removed {
				t.Errorf("exit %d removed %v, want %v (stderr %s)", r.code, got, c.removed, r.stderr)
			}
		})
	}
}

func TestPruneUsageErrorsExit2(t *testing.T) {
	cases := []struct {
		name string
		env  string
		args []string
	}{
		{"not a number", "", []string{"--older-than", "x"}},
		{"negative", "", []string{"--older-than", "-1"}},
		{"extra argument", "", []string{"some-id"}},
		{"unknown flag", "", []string{"--nope"}},
		{"bad environment value", "soon", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := twoRuns(t)
			if c.env != "" {
				s.set("MSGATE_RETENTION_DAYS", c.env)
			}
			r := s.run(append([]string{"prune", "--json"}, c.args...)...)
			if r.code != 2 || r.json(t)["sdk_status"] != "usage_error" {
				t.Errorf("exit %d stdout %q, want a usage error", r.code, r.stdout)
			}
			if !runDirExists(s, "pr-old") {
				t.Error("a refused prune removed a run")
			}
		})
	}
}

func TestPruneDropsTheIndexOfAWhollyPrunedSession(t *testing.T) {
	s := twoRuns(t)
	s.run("prune", "--older-than", "0")
	if exists(indexFile(s.home, "S")) {
		t.Error("the index of a session with no runs left is still there")
	}
}

func TestPrunedRunIsNotFoundForEveryReader(t *testing.T) {
	s := twoRuns(t)
	if r := s.run("prune"); r.code != 0 {
		t.Fatalf("prune exit %d", r.code)
	}
	for _, args := range [][]string{
		{"status", "pr-old"}, {"wait", "pr-old"}, {"result", "pr-old"}, {"cancel", "pr-old"},
		{"annotate", "pr-old", "--attr", "k=v"}, {"send", "pr-old", "again"},
		{"annotate", filepath.Join(s.home, "runs", "pr-old", "output.md"), "--attr", "k=v"},
	} {
		if r := s.run(args...); r.code != 4 {
			t.Errorf("%v: exit %d, want 4 (stderr %q)", args[:2], r.code, r.stderr)
		}
	}
	if ids, _ := statusRunIDs(t, s, "--session-id", "S"); len(ids) != 1 || ids[0] != "pr-new" {
		t.Errorf("status lists %v, want only pr-new", ids)
	}
}

// removeAtGate runs a reader that pauses after its first look at a run,
// removes the run directory during the pause, and returns the reader's result.
func removeAtGate(t *testing.T, s *sandbox, id string, args ...string) result {
	t.Helper()
	gate := filepath.Join(t.TempDir(), "gate")
	s.set("MSGATE_TEST_READ_GATE", gate)
	p := s.start("", args...)
	deadline := time.Now().Add(10 * time.Second)
	for !exists(gate + ".reached") {
		if time.Now().After(deadline) {
			t.Fatalf("%v never reached its gate (stderr %q)", args, p.errb.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.RemoveAll(filepath.Join(s.home, "runs", id)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p.wait(10 * time.Second)
}

func TestReaderWhoseRunVanishesMidReadExits4(t *testing.T) {
	cases := map[string][]string{
		"status":   {"status", "vm1"},
		"wait":     {"wait", "vm1"},
		"result":   {"result", "vm1"},
		"cancel":   {"cancel", "vm1"},
		"annotate": {"annotate", "vm1", "--attr", "k=v"},
		"send":     {"send", "vm1", "again"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
			seedRun(t, s, "vm1")
			s.set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
			if r := removeAtGate(t, s, "vm1", args...); r.code != 4 {
				t.Errorf("exit %d stdout %q stderr %q, want 4", r.code, r.stdout, r.stderr)
			}
		})
	}
}

func TestSendByConversationWorksAfterItsFirstTurnWasPruned(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "sp1", "--scenario", "second-opinion", "--model", "m1")
	ageRun(t, s, "sp1", 40)
	if r := s.run("prune", "--json"); r.code != 0 || r.json(t)["removed"] != float64(1) {
		t.Fatalf("prune exit %d stdout %q", r.code, r.stdout)
	}
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	if r := s.run("send", conv, "--run-id", "sp2", "again"); r.code != 0 {
		t.Fatalf("send exit %d, stderr %s", r.code, r.stderr)
	}
	checkFields(t, "turn 2 record", lastRunRecord(t, s.home), map[string]any{
		"turn": float64(2), "model": "m1", "model_source": "flag", "effort_source": "profile",
	})
	if r := s.run("send", "sp1", "again"); r.code != 4 {
		t.Errorf("send by the pruned run id: exit %d, want 4", r.code)
	}
}
