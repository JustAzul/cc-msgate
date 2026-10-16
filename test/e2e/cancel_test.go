package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func cancelRequestPath(s *sandbox, runID string) string {
	return filepath.Join(s.home, "runs", runID, "cancel.request")
}

// assertCancelled checks the state, conversation and telemetry of a cancelled
// run; providerExit is what the state records for the provider.
func assertCancelled(t *testing.T, s *sandbox, runID string, providerExit any) {
	t.Helper()
	st := stateOf(t, s, runID)
	checkFields(t, "state", st, map[string]any{
		"state": "cancelled", "exit_code": float64(130), "outcome": "cancelled", "sdk_status": "cancelled", "provider_exit": providerExit,
	})
	if st["ended_at"] == nil {
		t.Error("state.ended_at is not set")
	}
	if conv := conversationRecord(t, s.home, st["conversation_id"].(string)); conv["active_run_id"] != nil {
		t.Errorf("the conversation is still busy: %v", conv["active_run_id"])
	}
	checkFields(t, "telemetry", oneRecord(t, s.home), map[string]any{"run_id": runID, "outcome": "cancelled", "exit_code": float64(130)})
}

func TestCancelRunningJob(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 500*time.Millisecond, 500*time.Millisecond).set("FAKECODEX_SLEEP_MS", "60000")
	admitJob(t, s, "ca1", "q")
	st := waitForState(t, s, "ca1", "a running state with the provider group", hasProviderGroup)

	c := s.run("cancel", "--json", "ca1")
	if c.code != 0 {
		t.Fatalf("cancel exit %d, want 0; stderr %s", c.code, c.stderr)
	}
	if got := c.json(t)["run_id"]; got != "ca1" {
		t.Errorf("cancel json run_id = %v", got)
	}
	if info, err := os.Stat(cancelRequestPath(s, "ca1")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("cancel.request: %v, %v; want a 0600 file", info, err)
	}
	w := s.run("wait", "--json", "--timeout", "15", "ca1")
	if w.code != 130 {
		t.Fatalf("wait exit %d, want 130; stderr %s", w.code, w.stderr)
	}
	checkFields(t, "wait json", w.json(t), map[string]any{"state": "cancelled", "sdk_status": "cancelled", "provider_exit": float64(143)})
	assertCancelled(t, s, "ca1", float64(143))
	assertGroupGone(t, int(st["provider_pgid"].(float64)))
}

func TestCancelAForegroundRun(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 500*time.Millisecond, 500*time.Millisecond).set("FAKECODEX_SLEEP_MS", "60000")
	p := s.start("ca2", "exec", "--json", "--run-id", "ca2", "q")
	st := waitForState(t, s, "ca2", "a running state with the provider group", hasProviderGroup)

	if c := s.run("cancel", "ca2"); c.code != 0 {
		t.Fatalf("cancel exit %d, want 0; stderr %s", c.code, c.stderr)
	}
	r := p.wait(15 * time.Second)
	if r.code != 130 {
		t.Fatalf("the foreground run exited %d, want 130; stderr %s", r.code, r.stderr)
	}
	checkFields(t, "foreground json", r.json(t), map[string]any{"state": "cancelled", "outcome": "cancelled", "sdk_status": "cancelled", "exit_code": float64(130)})
	assertCancelled(t, s, "ca2", float64(143))
	assertGroupGone(t, int(st["provider_pgid"].(float64)))
}

// stalledJob admits a job whose worker waits before taking the run, so the run
// stays queued; the admission process stays up behind it.
func stalledJob(t *testing.T, runID string, stall time.Duration) (*sandbox, string, *proc) {
	t.Helper()
	s := newSandbox(t).set("MSGATE_TEST_WORKER_STALL_MS", strconv.Itoa(int(stall.Milliseconds())))
	rec := s.recordTo()
	cleanupJob(t, s, runID)
	admission := s.start(runID, "exec", "--background", "--run-id", runID, "q")
	waitForState(t, s, runID, "a queued state", func(m map[string]any) bool { return m["state"] == "queued" })
	return s, rec, admission
}

func TestCancelQueuedJobAndTheLateWorker(t *testing.T) {
	s, rec, _ := stalledJob(t, "ca3", 2000*time.Millisecond)

	c := s.run("cancel", "ca3")
	if c.code != 0 {
		t.Fatalf("cancel exit %d, want 0; stderr %s", c.code, c.stderr)
	}
	assertCancelled(t, s, "ca3", nil)
	if w := s.run("wait", "--timeout", "5", "ca3"); w.code != 130 {
		t.Errorf("wait exit %d, want 130", w.code)
	}
	settled := readFile(t, filepath.Join(s.home, "runs", "ca3", "state.json"))

	time.Sleep(3 * time.Second) // the stalled worker wakes up
	if got := readFile(t, filepath.Join(s.home, "runs", "ca3", "state.json")); got != settled {
		t.Errorf("the late worker changed the state:\n%s\nwas\n%s", got, settled)
	}
	if exists(rec) {
		t.Error("the late worker ran the provider")
	}
	oneRecord(t, s.home)
}

// A worker that finds a cancel request on a run it has not started leaves the
// run exactly as it is and does not run the provider.
func TestWorkerLeavesAQueuedRunWithACancelRequestAlone(t *testing.T) {
	s := newSandbox(t)
	rec := s.recordTo()
	seedRunFiles(t, s, "ca4", map[string]any{
		"state": "queued", "background": true, "started_at": nil, "ended_at": nil, "exit_code": nil, "outcome": nil,
	}, nil, nil)
	runDir := filepath.Join(s.home, "runs", "ca4")
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte("q"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cancelRequestPath(s, "ca4"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(runDir, "state.json"))

	if r := s.run("_worker", "ca4"); r.code != 0 {
		t.Fatalf("worker exit %d, want 0; stderr %s", r.code, r.stderr)
	}
	if after := readFile(t, filepath.Join(runDir, "state.json")); after != before {
		t.Errorf("the worker changed the state:\n%s\nwas\n%s", after, before)
	}
	if exists(rec) {
		t.Error("the worker ran the provider despite the cancel request")
	}
	if lockHeldElsewhere(t, filepath.Join(runDir, "lock")) {
		t.Error("the worker left the run lock held")
	}
}

// A cancel request that a starting worker honoured by leaving the run queued
// ends the run as cancelled, not as a failed admission.
func TestCancelRequestOnAQueuedJobEndsItCancelled(t *testing.T) {
	s, rec, admission := stalledJob(t, "ca9", 1000*time.Millisecond)
	if err := os.WriteFile(cancelRequestPath(s, "ca9"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := admission.wait(15 * time.Second); r.code != 0 {
		t.Fatalf("admission exit %d, want 0; stderr %s", r.code, r.stderr)
	}
	assertCancelled(t, s, "ca9", nil)
	if exists(rec) {
		t.Error("the provider ran despite the cancel request")
	}
}

func TestCancelOnATerminalRunChangesNothing(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	if r := s.run("exec", "--run-id", "ca5", "q"); r.code != 0 {
		t.Fatalf("exec exit %d", r.code)
	}
	before := readFile(t, filepath.Join(s.home, "runs", "ca5", "state.json"))
	c := s.run("cancel", "--json", "ca5")
	if c.code != 0 {
		t.Fatalf("cancel exit %d, want 0; stderr %s", c.code, c.stderr)
	}
	checkFields(t, "cancel json", c.json(t), map[string]any{"state": "done", "sdk_status": "ok", "exit_code": float64(0)})
	if after := readFile(t, filepath.Join(s.home, "runs", "ca5", "state.json")); after != before {
		t.Errorf("the state changed:\n%s\nwas\n%s", after, before)
	}
	if exists(cancelRequestPath(s, "ca5")) {
		t.Error("a cancel request was written for a terminal run")
	}
}

// A request that lands after the provider has exited, before the worker has
// finalized, has no effect.
func TestCancelAfterTheProviderExitedKeepsItsResult(t *testing.T) {
	s := pipeHolder(shutdownEnv(newSandbox(t), 500*time.Millisecond, 3*time.Second)).set("FAKECODEX_OUTPUT", "answer")
	admitJob(t, s, "ca6", "q")
	st := waitForState(t, s, "ca6", "a running state with the provider group", hasProviderGroup)
	pgid := int(st["provider_pgid"].(float64))
	untilTrue(t, "the provider to exit", func() bool { return len(liveInGroup(t, pgid)) == 0 })
	if cur := stateOf(t, s, "ca6"); cur["state"] != "running" {
		t.Skipf("the worker finalized before the cancel could land (state %v)", cur["state"])
	}

	if c := s.run("cancel", "ca6"); c.code != 0 {
		t.Fatalf("cancel exit %d; stderr %s", c.code, c.stderr)
	}
	w := s.run("wait", "--json", "--timeout", "15", "ca6")
	if w.code != 0 {
		t.Fatalf("wait exit %d, want the provider's 0; stderr %s", w.code, w.stderr)
	}
	checkFields(t, "wait json", w.json(t), map[string]any{"state": "done", "sdk_status": "ok", "provider_exit": float64(0), "outcome": "ok"})
}

func TestCancelUnknownRunsAndBadUsage(t *testing.T) {
	s := newSandbox(t)
	for _, id := range []string{"nope", "../x", "r-20260101T000000Z-deadbeef"} {
		if r := s.run("cancel", id); r.code != 4 {
			t.Errorf("cancel %s: exit %d, want 4 (stderr %q)", id, r.code, r.stderr)
		}
	}
	checkFields(t, "json", s.run("cancel", "--json", "nope").json(t), map[string]any{"sdk_status": "not_found", "exit_code": float64(4)})
	if r := s.run("cancel"); r.code != 2 {
		t.Errorf("cancel with no id: exit %d, want 2", r.code)
	}
	if r := s.run("cancel", "a", "b"); r.code != 2 {
		t.Errorf("cancel with two ids: exit %d, want 2", r.code)
	}
}

// A queued run whose lock a worker holds is cancelled once that worker reaches
// running, by signalling it; a worker that never gets there is reported.
func TestCancelWaitsForAStartingWorker(t *testing.T) {
	s := newSandbox(t).set("MSGATE_TEST_ADMISSION_WAIT_MS", "5000")
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- sleeper.Wait() }()
	t.Cleanup(func() { sleeper.Process.Kill() })

	now := time.Now().UTC().Format(time.RFC3339)
	seedConversation(t, s, "c-seed-ca7", "ca7")
	seedRunFiles(t, s, "ca7", map[string]any{
		"state": "queued", "background": true, "admitted_at": now, "started_at": nil, "ended_at": nil,
		"exit_code": nil, "outcome": nil,
	}, nil, nil)
	release := holdLock(t, filepath.Join(s.home, "runs", "ca7", "lock"))
	defer release()
	c := s.start("", "cancel", "ca7")
	time.Sleep(400 * time.Millisecond)
	if !isQueued(stateOf(t, s, "ca7")) {
		t.Fatal("the run was settled while a worker held its lock")
	}

	// The worker reaches running.
	b := stateOf(t, s, "ca7")
	b["state"], b["worker_pid"] = "running", sleeper.Process.Pid
	writeStateFile(t, s, "ca7", b)
	if r := c.wait(10 * time.Second); r.code != 0 {
		t.Fatalf("cancel exit %d; stderr %s", r.code, r.stderr)
	}
	select {
	case err := <-exited:
		if ee, ok := err.(*exec.ExitError); !ok || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Errorf("the worker ended with %v, want SIGTERM", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("the worker was not signalled")
	}
}

func TestCancelGivesUpOnAWorkerThatNeverStarts(t *testing.T) {
	s := newSandbox(t).set("MSGATE_TEST_ADMISSION_WAIT_MS", "600")
	seedConversation(t, s, "c-seed-ca8", "ca8")
	seedRunFiles(t, s, "ca8", map[string]any{
		"state": "queued", "admitted_at": time.Now().UTC().Format(time.RFC3339), "started_at": nil, "ended_at": nil,
		"exit_code": nil, "outcome": nil,
	}, nil, nil)
	holdLock(t, filepath.Join(s.home, "runs", "ca8", "lock"))
	if r := s.run("cancel", "--json", "ca8"); r.code != 70 {
		t.Errorf("cancel exit %d, want 70; stdout %s stderr %s", r.code, r.stdout, r.stderr)
	}
	if st := stateOf(t, s, "ca8"); st["state"] != "queued" {
		t.Errorf("state = %v, want it untouched", st["state"])
	}
}

func isQueued(m map[string]any) bool { return m["state"] == "queued" }

func writeStateFile(t *testing.T, s *sandbox, runID string, m map[string]any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.home, "runs", runID, "state.json")
	if err := os.WriteFile(path+".tmp", b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}
