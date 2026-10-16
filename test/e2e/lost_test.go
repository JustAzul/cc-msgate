package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// crashWorker waits until the job's provider is up, then SIGKILLs only the
// worker and waits for the run lock to come free. It returns the state seen
// before the kill.
func crashWorker(t *testing.T, s *sandbox, runID string) map[string]any {
	t.Helper()
	st := waitForState(t, s, runID, "a running state with the provider group", hasProviderGroup)
	if err := syscall.Kill(int(st["worker_pid"].(float64)), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(s.home, "runs", runID, "lock")
	untilTrue(t, "the killed worker to drop the run lock", func() bool { return !lockHeldElsewhere(t, lock) })
	return st
}

// lostJob admits a job on a slow provider and crashes its worker.
func lostJob(t *testing.T, runID string) (*sandbox, map[string]any) {
	t.Helper()
	s := newSandbox(t).set("FAKECODEX_SLEEP_MS", "60000")
	admitJob(t, s, runID, "q")
	return s, crashWorker(t, s, runID)
}

// seedConversation writes a conversation record naming activeRun as its
// active turn.
func seedConversation(t *testing.T, s *sandbox, id, activeRun string) {
	t.Helper()
	dir := filepath.Join(s.home, "conversations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	conv := map[string]any{
		"conversation_id": id, "provider": "codex", "provider_session_id": nil, "cwd": t.TempDir(),
		"defaults": map[string]any{"purpose": "adhoc", "model": "", "effort": "", "sandbox": ""},
		"turns":    []string{activeRun}, "active_run_id": activeRun,
		"created_at": "2026-10-04T12:00:00Z", "updated_at": "2026-10-04T12:00:00Z",
	}
	b, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertLostOnce checks everything a reconciled lost run leaves behind.
func assertLostOnce(t *testing.T, s *sandbox, runID string) {
	t.Helper()
	st := stateOf(t, s, runID)
	checkFields(t, "state", st, map[string]any{
		"state": "lost", "exit_code": float64(125), "outcome": "lost", "sdk_status": "lost", "provider_exit": nil,
	})
	if st["ended_at"] == nil {
		t.Error("state.ended_at is not set")
	}
	conv := conversationRecord(t, s.home, st["conversation_id"].(string))
	if conv["active_run_id"] != nil {
		t.Errorf("the conversation is still busy: %v", conv["active_run_id"])
	}
	rec := oneRecord(t, s.home)
	checkFields(t, "telemetry", rec, map[string]any{"run_id": runID, "outcome": "lost", "exit_code": float64(125), "background": true})
}

func TestWorkerCrashIsReportedLostByTheNextReader(t *testing.T) {
	s, before := lostJob(t, "lo1")
	r := s.run("status", "--json", "lo1")
	if r.code != 0 {
		t.Fatalf("status exit %d, stderr %s", r.code, r.stderr)
	}
	checkFields(t, "status json", r.json(t), map[string]any{
		"state": "lost", "exit_code": float64(125), "outcome": "lost", "sdk_status": "lost", "provider_exit": nil,
	})
	assertLostOnce(t, s, "lo1")
	assertGroupGone(t, int(before["provider_pgid"].(float64)))

	if again := s.run("status", "lo1"); again.code != 0 {
		t.Fatalf("second status exit %d", again.code)
	}
	oneRecord(t, s.home)
}

func TestWaitOnALostRunExits125(t *testing.T) {
	s, _ := lostJob(t, "lo2")
	w := s.run("wait", "--json", "--timeout", "10", "lo2")
	if w.code != 125 {
		t.Fatalf("wait exit %d, want 125; stderr %s", w.code, w.stderr)
	}
	checkFields(t, "wait json", w.json(t), map[string]any{"state": "lost", "sdk_status": "lost", "exit_code": float64(125)})
}

func TestWaitSeesARunBecomeLost(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_SLEEP_MS", "60000")
	admitJob(t, s, "lo3", "q")
	waiting := s.start("", "wait", "--timeout", "12", "lo3")
	crashWorker(t, s, "lo3")
	if r := waiting.wait(15 * time.Second); r.code != 125 {
		t.Fatalf("wait exit %d, want 125; stderr %s", r.code, r.stderr)
	}
	assertLostOnce(t, s, "lo3")
}

func TestOrphanProviderDoesNotKeepTheRunLock(t *testing.T) {
	s, before := lostJob(t, "lo4")
	pgid := int(before["provider_pgid"].(float64))
	// crashWorker returned because the lock is free; the provider is still up.
	if live := liveInGroup(t, pgid); len(live) == 0 {
		t.Fatal("the provider did not outlive its worker")
	}
	if r := s.run("status", "--json", "lo4"); r.json(t)["state"] != "lost" {
		t.Errorf("status = %s", r.stdout)
	}
	assertGroupGone(t, pgid)
}

// A recorded group id that now belongs to an unrelated process, with another
// start time, is never signalled.
func TestLostWithAReusedProcessGroupKillsNothing(t *testing.T) {
	s := newSandbox(t)
	other := exec.Command("sleep", "60")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	seedConversation(t, s, "c-seed-lo5", "lo5")
	seedRunFiles(t, s, "lo5", map[string]any{
		"state": "running", "background": true, "ended_at": nil, "exit_code": nil, "outcome": nil,
		"provider_pgid": other.Process.Pid, "provider_start_time": "1",
	}, nil, nil)

	if r := s.run("status", "--json", "lo5"); r.json(t)["state"] != "lost" {
		t.Fatalf("status = %s", r.stdout)
	}
	// A killed child stays a zombie until it is reaped, so look for it alive.
	if live := liveInGroup(t, other.Process.Pid); len(live) != 1 {
		t.Errorf("live processes in the unrelated group = %v, want it untouched", live)
	}
	assertLostOnce(t, s, "lo5")
}

// A queued run is not lost while its admission may still be in progress, and
// is lost once the window has passed with no worker holding the lock; a worker
// that wakes up afterwards leaves it alone.
func TestQueuedRunIsLostOnlyAfterTheAdmissionWindow(t *testing.T) {
	s := newSandbox(t).set("MSGATE_TEST_WORKER_STALL_MS", "5500").set("MSGATE_TEST_QUEUED_GRACE_MS", "2500")
	rec := s.recordTo()
	cleanupJob(t, s, "lo6")
	s.start("lo6", "exec", "--background", "--run-id", "lo6", "q")
	waitForState(t, s, "lo6", "a queued state", func(m map[string]any) bool { return m["state"] == "queued" })

	if r := s.run("status", "--json", "lo6"); r.json(t)["state"] != "queued" {
		t.Fatalf("a fresh queued run: status = %s", r.stdout)
	}
	if recs := telemetryRecords(t, s.home); len(recs) != 0 {
		t.Fatalf("a fresh queued run was recorded: %v", recs)
	}
	time.Sleep(3500 * time.Millisecond)
	if r := s.run("status", "--json", "lo6"); r.json(t)["state"] != "lost" {
		t.Fatalf("a queued run past the window: status = %s", r.stdout)
	}
	settled := readFile(t, filepath.Join(s.home, "runs", "lo6", "state.json"))

	time.Sleep(3500 * time.Millisecond) // the stalled worker wakes up
	if got := readFile(t, filepath.Join(s.home, "runs", "lo6", "state.json")); got != settled {
		t.Errorf("the late worker changed the state:\n%s\nwas\n%s", got, settled)
	}
	if exists(rec) {
		t.Error("the late worker ran the provider")
	}
	oneRecord(t, s.home)
}

// feedFIFO answers every reader that opens the FIFO at path with body, until
// all the processes are done or the limit passes. A reader blocks in open
// until a writer arrives, so the test decides when each one goes on.
func feedFIFO(t *testing.T, path, body string, procs []*proc, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if allDone(procs) {
			return
		}
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			f.WriteString(body)
			f.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the readers did not finish")
}

func allDone(procs []*proc) bool {
	for _, p := range procs {
		select {
		case <-p.done:
		default:
			return false
		}
	}
	return true
}

// Readers racing over one lost run finalize it once. The run's request file is
// a FIFO, which holds every reader that has decided the run is lost at the
// point just before it is settled, until the test lets them go.
func TestRacingReadersFinalizeALostRunOnce(t *testing.T) {
	const readers = 4
	s := newSandbox(t)
	seedConversation(t, s, "c-seed-race", "race")
	seedRunFiles(t, s, "race", map[string]any{"state": "running", "background": true, "ended_at": nil, "exit_code": nil, "outcome": nil}, nil, nil)
	request := filepath.Join(s.home, "runs", "race", "request.json")
	if err := os.Remove(request); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(request, 0o600); err != nil {
		t.Fatal(err)
	}
	var procs []*proc
	for i := 0; i < readers; i++ {
		procs = append(procs, s.start("", "status", "--json", "race"))
	}
	time.Sleep(500 * time.Millisecond) // every reader that can has reached the FIFO
	feedFIFO(t, request, `{"command":"exec","provider":"codex","purpose":"adhoc","source":"cli","session_id":""}`, procs, 15*time.Second)

	if recs := telemetryRecords(t, s.home); len(recs) != 1 {
		t.Errorf("telemetry has %d records for the raced run, want exactly 1: %v", len(recs), recs)
	}
	if st := stateOf(t, s, "race"); st["state"] != "lost" {
		t.Errorf("state = %v, want lost", st["state"])
	}
}

func TestResultAndListingReconcileALostRun(t *testing.T) {
	s := newSandbox(t)
	seedConversation(t, s, "c-seed-lo7", "lo7")
	seedRunFiles(t, s, "lo7", map[string]any{"state": "running", "ended_at": nil, "exit_code": nil, "outcome": nil}, nil, []byte("partial"))
	if r := s.run("result", "lo7"); r.code != 0 || r.stdout != "partial" {
		t.Errorf("result exit %d stdout %q stderr %q, want the lost run's output", r.code, r.stdout, r.stderr)
	}
	if st := stateOf(t, s, "lo7"); st["state"] != "lost" {
		t.Errorf("state = %v", st["state"])
	}

	seedConversation(t, s, "c-seed-lo8", "lo8")
	seedRunFiles(t, s, "lo8", map[string]any{"state": "running", "ended_at": nil, "exit_code": nil, "outcome": nil}, nil, nil)
	r := s.run("status", "--json")
	runs, _ := r.json(t)["runs"].([]any)
	for _, v := range runs {
		if m := v.(map[string]any); m["state"] != "lost" {
			t.Errorf("listing shows %v as %v, want lost", m["run_id"], m["state"])
		}
	}
	if len(runs) != 2 {
		t.Errorf("listing = %s, want both runs", r.stdout)
	}
}

func TestSendAfterALostTurnIsNotBusy(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "lo9a")
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2")).set("FAKECODEX_SLEEP_MS", "60000")
	cleanupJob(t, s, "lo9b")
	if r := s.run("send", conv, "--background", "--run-id", "lo9b", "again"); r.code != 0 {
		t.Fatalf("send --background exit %d, stderr %s", r.code, r.stderr)
	}
	crashWorker(t, s, "lo9b")

	s.set("FAKECODEX_SLEEP_MS", "")
	r := s.run("send", conv, "--run-id", "lo9c", "third")
	if r.code != 0 {
		t.Fatalf("send after a lost turn: exit %d, stderr %s", r.code, r.stderr)
	}
	if st := stateOf(t, s, "lo9b"); st["state"] != "lost" {
		t.Errorf("lo9b = %v, want lost", st["state"])
	}
}
