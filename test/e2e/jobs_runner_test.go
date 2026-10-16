package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestJobCarriesItsRequestIntoTelemetry(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	admitJob(t, s, "jp1", "--purpose", "cross-check", "--source", "mod",
		"--session-id", "S", "--attr", "k=v", "--attr-json", "big=12345678901234567890",
		"--timeout", "30", "--clean-sentinel", "PONG", "q")
	waitOK(t, s, "jp1")
	rec := oneRecord(t, s.home)
	checkFields(t, "record", rec, map[string]any{
		"outcome": "clean", "purpose": "cross-check", "source": "mod", "session_id": "S",
		"timeout_s": float64(30), "background": true, "turn": float64(1),
	})
	if attrs, _ := rec["attrs"].(map[string]any); attrs["k"] != "v" {
		t.Errorf("attrs = %v", rec["attrs"])
	}
	if raw := readFile(t, currentMonthFile(s.home)); !strings.Contains(raw, `"big":12345678901234567890`) {
		t.Errorf("the large integer attribute lost digits in the worker's record:\n%s", raw)
	}
}

func TestJobTimeoutEndsTheProviderGroup(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 700*time.Millisecond, 700*time.Millisecond).
		set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_SPAWN_CHILD", "1").set("FAKECODEX_IGNORE_TERM", "1")
	admitJob(t, s, "jt1", "--timeout", "1", "q")
	w := s.run("wait", "--json", "jt1")
	if w.code != 124 {
		t.Fatalf("wait exit %d, want 124; stderr %s", w.code, w.stderr)
	}
	checkFields(t, "json", w.json(t), map[string]any{"state": "timeout", "sdk_status": "timeout", "outcome": "timeout"})
	assertGroupGone(t, int(stateOf(t, s, "jt1")["provider_pgid"].(float64)))
	if rec := oneRecord(t, s.home); rec["outcome"] != "timeout" || rec["background"] != true {
		t.Errorf("record = %v", rec)
	}
}

func TestJobHandlesTerminationLikeAForegroundRun(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 500*time.Millisecond, 500*time.Millisecond).set("FAKECODEX_SLEEP_MS", "60000")
	admitJob(t, s, "jk1", "q")
	st := waitForState(t, s, "jk1", "a running state with the provider group", hasProviderGroup)
	if err := syscall.Kill(int(st["worker_pid"].(float64)), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if w := s.run("wait", "jk1"); w.code != 143 {
		t.Fatalf("wait exit %d, want 143; stderr %s", w.code, w.stderr)
	}
	checkFields(t, "state", stateOf(t, s, "jk1"), map[string]any{"state": "cancelled", "outcome": "cancelled"})
	assertGroupGone(t, int(st["provider_pgid"].(float64)))
}

func TestJobWithoutAProviderFailsWith127(t *testing.T) {
	s := newSandbox(t).withoutProvider()
	cleanupJob(t, s, "jm1")
	r := s.run("exec", "--background", "--run-id", "jm1", "q")
	if r.code != 0 {
		t.Fatalf("admission exit %d, want 0: the worker did reach running; stderr %s", r.code, r.stderr)
	}
	w := s.run("wait", "--json", "jm1")
	if w.code != 127 {
		t.Fatalf("wait exit %d, want 127; stderr %s", w.code, w.stderr)
	}
	checkFields(t, "json", w.json(t), map[string]any{"state": "failed", "sdk_status": "provider_missing", "provider_exit": nil, "outcome": "error"})
}

func TestSendAndReviewRunAsJobs(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "jc1")
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	cleanupJob(t, s, "jc2")
	sent := s.run("send", conv, "--background", "--run-id", "jc2", "again")
	waitOK(t, s, "jc2")
	s.set("FAKECODEX_FIXTURE", fixture("review-ok"))
	cleanupJob(t, s, "jc3")
	reviewed := s.run("review", "--uncommitted", "--background", "--cwd", gitRepo(t), "--run-id", "jc3")
	waitOK(t, s, "jc3")

	if sent.stdout != "jc2\n" || reviewed.stdout != "jc3\n" {
		t.Errorf("admissions printed %q and %q, want the run ids", sent.stdout, reviewed.stdout)
	}
	byRun := map[any]map[string]any{}
	for _, rec := range telemetryRecords(t, s.home) {
		byRun[rec["run_id"]] = rec
	}
	checkFields(t, "send record", byRun["jc2"], map[string]any{"command": "send", "turn": float64(2), "background": true, "conversation_id": conv})
	checkFields(t, "review record", byRun["jc3"], map[string]any{"command": "review", "background": true})
	checkFields(t, "conversation", conversationRecord(t, s.home, conv), map[string]any{"active_run_id": nil})
}

// Sending to a conversation whose job is still running is refused, and
// finishing the job frees the conversation again.
func TestSendIsBusyWhileAJobRuns(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "jb1")
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2")).set("FAKECODEX_SLEEP_MS", "1500")
	cleanupJob(t, s, "jb2")
	if r := s.run("send", conv, "--background", "--run-id", "jb2", "again"); r.code != 0 {
		t.Fatalf("send --background: exit %d, stderr %s", r.code, r.stderr)
	}
	r := s.run("send", conv, "--run-id", "jb3", "third")
	if r.code != 3 || !strings.Contains(r.stderr, "jb2") {
		t.Errorf("send while the job runs: exit %d stderr %q, want 3 naming jb2", r.code, r.stderr)
	}
	waitOK(t, s, "jb2")
	if _, err := os.Stat(filepath.Join(s.home, "runs", "jb3")); err == nil {
		t.Error("the refused send left a run directory")
	}
}
