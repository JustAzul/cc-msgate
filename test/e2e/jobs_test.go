package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// cleanupJob kills what a test's job leaves behind: a worker that is still
// running and the provider group its state recorded. It then waits for the run
// lock to be free, so a worker that was finishing has stopped writing before
// the sandbox's directory is removed.
func cleanupJob(t *testing.T, s *sandbox, runID string) {
	t.Helper()
	t.Cleanup(func() {
		st, err := os.ReadFile(filepath.Join(s.home, "runs", runID, "state.json"))
		if err == nil {
			var m map[string]any
			// A queued state still names the admitting process, which may be gone.
			if jsonUnmarshal(string(st), &m) == nil && m["state"] == "running" {
				if pid, _ := m["worker_pid"].(float64); pid > 1 {
					syscall.Kill(int(pid), syscall.SIGKILL)
				}
			}
		}
		killRecordedGroup(s.home, runID)
		lock := filepath.Join(s.home, "runs", runID, "lock")
		for deadline := time.Now().Add(10 * time.Second); lockHeld(t, lock); time.Sleep(10 * time.Millisecond) {
			if !time.Now().Before(deadline) {
				t.Errorf("run %s: the run lock is still held after cleanup", runID)
				return
			}
		}
	})
}

// processGone reports whether pid no longer names a live process.
func processGone(pid int) bool {
	err := syscall.Kill(pid, 0)
	return errors.Is(err, syscall.ESRCH)
}

// admitJob starts a job and fails the test unless the admission succeeds.
func admitJob(t *testing.T, s *sandbox, runID string, args ...string) result {
	t.Helper()
	cleanupJob(t, s, runID)
	r := s.run(append([]string{"exec", "--background", "--run-id", runID}, args...)...)
	if r.code != 0 {
		t.Fatalf("admission of %s: exit %d, stderr %s", runID, r.code, r.stderr)
	}
	return r
}

// waitOK waits for the run and fails the test unless wait exits 0.
func waitOK(t *testing.T, s *sandbox, runID string) result {
	t.Helper()
	w := s.run("wait", runID)
	if w.code != 0 {
		t.Fatalf("wait %s: exit %d, stderr %s", runID, w.code, w.stderr)
	}
	return w
}

// assertRunningJob checks the state file and run lock of a job whose worker
// is up.
func assertRunningJob(t *testing.T, s *sandbox, runID string) {
	t.Helper()
	st := stateOf(t, s, runID)
	checkFields(t, "state", st, map[string]any{"state": "running", "background": true})
	if pid, _ := st["worker_pid"].(float64); pid <= 1 || processGone(int(pid)) {
		t.Errorf("worker_pid = %v does not name a live worker", st["worker_pid"])
	}
	if !lockHeldElsewhere(t, filepath.Join(s.home, "runs", runID, "lock")) {
		t.Error("the worker does not hold the run lock")
	}
}

func TestJobAdmissionThenWait(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "3000")
	began := time.Now()
	r := s.run("exec", "--background", "--json", "--cwd", gitRepo(t), "--run-id", "job1", "q")
	elapsed := time.Since(began)
	cleanupJob(t, s, "job1")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	// The worker holds no end of the caller's pipes, so the admission returns
	// while the provider is still sleeping.
	if elapsed > 2500*time.Millisecond {
		t.Errorf("admission took %v with a provider that sleeps 3 s", elapsed)
	}
	j := r.json(t)
	runDir := filepath.Join(s.home, "runs", "job1")
	checkFields(t, "json", j, map[string]any{
		"run_id": "job1", "state": "running", "sdk_status": "ok", "exit_code": float64(0),
		"run_dir": runDir, "output_path": filepath.Join(runDir, "output.md"),
	})
	if conv, _ := j["conversation_id"].(string); !strings.HasPrefix(conv, "c-") {
		t.Errorf("conversation_id = %v", j["conversation_id"])
	}
	assertRunningJob(t, s, "job1")

	if w := waitOK(t, s, "job1"); w.lastLine() != j["output_path"] {
		t.Errorf("wait last line = %q, want the output path %q", w.lastLine(), j["output_path"])
	}
	checkFields(t, "record", oneRecord(t, s.home), map[string]any{"background": true, "run_id": "job1", "outcome": "ok", "command": "exec"})
	checkFields(t, "final state", stateOf(t, s, "job1"), map[string]any{"state": "done", "background": true})
}

// A worker that finishes before the parent's next poll has still reached
// running; the parent reports the state it observed.
func TestJobAdmissionAcceptsAWorkerThatAlreadyFinished(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	r := admitJob(t, s, "jobfast", "--json", "q")
	if got := r.json(t)["state"]; got != "running" && got != "done" {
		t.Errorf("state = %v, want running or done", got)
	}
	waitOK(t, s, "jobfast")
}

func TestJobLastLineIsTheRunID(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "500")
	r := admitJob(t, s, "job2", "q")
	if r.stdout != "job2\n" {
		t.Errorf("stdout = %q, want the run id alone", r.stdout)
	}
	waitOK(t, s, "job2")
}

func TestWorkerCommandIsNotListedInHelp(t *testing.T) {
	r := newSandbox(t).run("help")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if strings.Contains(r.stdout, "_worker") {
		t.Errorf("help lists the internal worker command:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "exec") {
		t.Errorf("help does not list exec:\n%s", r.stdout)
	}
}

func TestJobPromptIsOnDiskBeforeTheWorkerStarts(t *testing.T) {
	const prompt = "a long prompt\nover two lines"
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("MSGATE_TEST_WORKER_STALL_MS", "1500")
	rec := s.recordTo()
	s.stdin = prompt
	cleanupJob(t, s, "bg-stdin")
	p := s.start("bg-stdin", "exec", "--background", "--run-id", "bg-stdin", "-")

	runDir := filepath.Join(s.home, "runs", "bg-stdin")
	waitForState(t, s, "bg-stdin", "a queued state", func(m map[string]any) bool { return m["state"] == "queued" })
	if got := readFile(t, filepath.Join(runDir, "prompt.md")); got != prompt {
		t.Errorf("prompt.md = %q while the worker has not started, want %q", got, prompt)
	}
	if !exists(filepath.Join(runDir, "request.json")) {
		t.Error("request.json is missing while the worker has not started")
	}
	if r := p.wait(15 * time.Second); r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	waitOK(t, s, "bg-stdin")
	if got := readRecord(t, rec).Stdin; got != prompt {
		t.Errorf("the fake received %q, want %q", got, prompt)
	}
}

func TestJobLargePromptIsDeliveredIntact(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()
	s.stdin = strings.Repeat("0123456789abcdef", 128*1024) // 2 MiB
	admitJob(t, s, "bg-big", "-")
	waitOK(t, s, "bg-big")
	if got := readRecord(t, rec).Stdin; got != s.stdin {
		t.Errorf("fake received %d bytes, want %d", len(got), len(s.stdin))
	}
	if got := readFile(t, filepath.Join(s.home, "runs", "bg-big", "prompt.md")); got != s.stdin {
		t.Errorf("prompt.md holds %d bytes, want %d", len(got), len(s.stdin))
	}
}

func TestJobThatNeverReachesRunningIsFailedAndRecorded(t *testing.T) {
	s := newSandbox(t).set("MSGATE_TEST_WORKER_STALL_MS", "60000").set("MSGATE_TEST_ADMISSION_WAIT_MS", "800")
	cleanupJob(t, s, "stall1")
	began := time.Now()
	r := s.run("exec", "--background", "--json", "--run-id", "stall1", "q")
	if r.code != 70 {
		t.Fatalf("exit %d, want 70; stderr %s", r.code, r.stderr)
	}
	if took := time.Since(began); took > 8*time.Second {
		t.Errorf("admission took %v with a window of 800 ms", took)
	}
	checkFields(t, "json", r.json(t), map[string]any{"sdk_status": "internal_error", "exit_code": float64(70), "run_id": "stall1"})

	st := stateOf(t, s, "stall1")
	checkFields(t, "state", st, map[string]any{"state": "failed", "outcome": "error", "exit_code": float64(70), "background": true})
	if excerpt, _ := st["error_excerpt"].(string); !strings.Contains(excerpt, "running") {
		t.Errorf("error_excerpt = %v, want it to say the worker did not reach running", st["error_excerpt"])
	}
	assertFailedAdmissionCleanedUp(t, s, st)
	rec := oneRecord(t, s.home)
	checkFields(t, "telemetry", rec, map[string]any{"run_id": "stall1", "outcome": "error", "exit_code": float64(70), "background": true, "command": "exec"})
	if ts, _ := rec["ts"].(string); !strings.HasPrefix(ts, time.Now().UTC().Format("2006-01-02")) {
		t.Errorf("telemetry ts = %v, want the admission time", rec["ts"])
	}
}

// assertFailedAdmissionCleanedUp checks that a failed admission freed the
// conversation and the run lock and left no live worker.
func assertFailedAdmissionCleanedUp(t *testing.T, s *sandbox, st map[string]any) {
	t.Helper()
	conv := conversationRecord(t, s.home, st["conversation_id"].(string))
	if conv["active_run_id"] != nil {
		t.Errorf("the conversation is still busy: %v", conv["active_run_id"])
	}
	if pid, _ := st["worker_pid"].(float64); pid <= 1 || !processGone(int(pid)) {
		t.Errorf("worker_pid = %v: the stalled worker is still alive", st["worker_pid"])
	}
	if lockHeldElsewhere(t, filepath.Join(s.home, "runs", st["run_id"].(string), "lock")) {
		t.Error("the run lock is still held")
	}
}

// startLauncherShell runs a job admission inside a shell of its own process
// group, which then stays alive, and returns the group id. The admission's
// stdout lands in outFile.
func startLauncherShell(t *testing.T, s *sandbox, outFile string, args string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", `"$0" `+args+` >"$1"; sleep 60`, msgateBin, outFile)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + s.path, "HOME=" + t.TempDir(), "MSGATE_HOME=" + s.home}
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	group := cmd.Process.Pid
	t.Cleanup(func() { syscall.Kill(-group, syscall.SIGKILL); cmd.Wait() })
	return group
}

func TestJobSurvivesItsLauncher(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "2000")
	cleanupJob(t, s, "surv1")
	outFile := filepath.Join(t.TempDir(), "launcher.out")
	launcherGroup := startLauncherShell(t, s, outFile, "exec --background --run-id surv1 q")

	untilTrue(t, "the admission to print the run id", func() bool {
		b, _ := os.ReadFile(outFile)
		return string(b) == "surv1\n"
	})
	workerPID := int(stateOf(t, s, "surv1")["worker_pid"].(float64))
	if g, err := syscall.Getpgid(workerPID); err != nil || g == launcherGroup {
		t.Fatalf("worker group = %d (%v), the launcher's group is %d: the worker must lead a session of its own", g, err, launcherGroup)
	}

	if err := syscall.Kill(-launcherGroup, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitOK(t, s, "surv1")
	if fin := stateOf(t, s, "surv1"); fin["state"] != "done" {
		t.Errorf("state = %v, want done after its launcher was killed", fin["state"])
	}
}

// A job finishes in the same order as a foreground run: output, terminal
// state, the conversation's marker, the telemetry record.
func TestJobTerminalOrdering(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "final").set("FAKECODEX_SLEEP_MS", "1500")
	r := admitJob(t, s, "jord1", "--json", "q")
	convID := r.json(t)["conversation_id"].(string)
	releaseConv := holdLock(t, filepath.Join(s.home, "conversations", convID+".lock"))
	releaseTel := holdLock(t, filepath.Join(s.home, "telemetry", ".lock"))
	convFile := filepath.Join(s.home, "conversations", convID+".json")
	marker := func() any { return readJSONFile(t, convFile)["active_run_id"] }
	telFiles := func() []string { m, _ := filepath.Glob(filepath.Join(s.home, "telemetry", "*.jsonl")); return m }

	fin := waitForState(t, s, "jord1", "a terminal state", isTerminal)
	if got := readFile(t, fin["output_path"].(string)); got != "final" {
		t.Errorf("output.md = %q when the state turned terminal, want the final bytes", got)
	}
	time.Sleep(150 * time.Millisecond)
	if marker() != "jord1" || len(telFiles()) != 0 {
		t.Errorf("marker = %v, telemetry files = %v: neither may move while the conversation lock is held", marker(), telFiles())
	}

	releaseConv()
	untilTrue(t, "the marker to be cleared", func() bool { return marker() == nil })
	time.Sleep(150 * time.Millisecond)
	if !isTerminal(stateOf(t, s, "jord1")) || len(telFiles()) != 0 {
		t.Errorf("state terminal = %v, telemetry files = %v: the marker is cleared after the state and before telemetry", isTerminal(stateOf(t, s, "jord1")), telFiles())
	}

	releaseTel()
	// The month file exists, empty, before its line is written: wait for a whole line.
	untilTrue(t, "the telemetry record", func() bool {
		files := telFiles()
		if len(files) != 1 {
			return false
		}
		b, err := os.ReadFile(files[0])
		return err == nil && len(b) > 0 && b[len(b)-1] == '\n'
	})
	checkFields(t, "record", oneRecord(t, s.home), map[string]any{"run_id": "jord1", "outcome": "ok", "background": true})
}

// wait returns once the run is finalized, not merely terminal: the telemetry
// record is the last thing the worker writes, so it is there when wait returns
// even though the state turned terminal well before.
func TestWaitReturnsAfterTheRunIsFinalized(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "final").set("FAKECODEX_SLEEP_MS", "800")
	admitJob(t, s, "wfin1", "q")
	releaseTel := holdLock(t, filepath.Join(s.home, "telemetry", ".lock"))
	telFiles := func() []string { m, _ := filepath.Glob(filepath.Join(s.home, "telemetry", "*.jsonl")); return m }

	type waited struct {
		r        result
		telAtEnd int
	}
	done := make(chan waited, 1)
	go func() {
		r := s.run("wait", "wfin1")
		done <- waited{r, len(telFiles())}
	}()
	waitForState(t, s, "wfin1", "a terminal state", isTerminal)
	time.Sleep(400 * time.Millisecond) // the append is held behind the telemetry lock
	releaseTel()

	w := <-done
	if w.r.code != 0 {
		t.Fatalf("wait: exit %d, stderr %s", w.r.code, w.r.stderr)
	}
	if w.telAtEnd != 1 {
		t.Errorf("telemetry files when wait returned = %d, want the run's record already written", w.telAtEnd)
	}
	if held := lockHeld(t, filepath.Join(s.home, "runs", "wfin1", "lock")); held {
		t.Error("the run lock is still held when wait returned")
	}
}

// A finalization that does not finish does not hold wait for ever: past the
// bound wait prints one warning and returns the run it saw.
func TestWaitGivesUpOnFinalizationAfterTheBound(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "final").set("FAKECODEX_SLEEP_MS", "500").
		set("MSGATE_TEST_FINALIZE_WAIT_MS", "600")
	admitJob(t, s, "wfin2", "q")
	holdLock(t, filepath.Join(s.home, "telemetry", ".lock"))

	start := time.Now()
	w := s.run("wait", "wfin2")
	elapsed := time.Since(start)
	if w.code != 0 {
		t.Fatalf("wait: exit %d, stderr %s", w.code, w.stderr)
	}
	if got := warningLines(w.stderr); len(got) != 1 || !strings.Contains(got[0], "wfin2") {
		t.Errorf("warnings = %q, want one line naming the run", got)
	}
	if elapsed > 4*time.Second {
		t.Errorf("wait took %v, want it to give up shortly after the bound", elapsed)
	}
	if w.lastLine() != filepath.Join(s.home, "runs", "wfin2", "output.md") {
		t.Errorf("wait last line = %q, want the output path", w.lastLine())
	}
}

// --timeout covers the whole wait, finalization included.
func TestWaitTimeoutCoversFinalization(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "final").set("FAKECODEX_SLEEP_MS", "300")
	admitJob(t, s, "wfin3", "q")
	holdLock(t, filepath.Join(s.home, "telemetry", ".lock"))

	w := s.run("wait", "wfin3", "--timeout", "2")
	if w.code != 5 {
		t.Errorf("wait: exit %d, want 5 (stderr %s)", w.code, w.stderr)
	}
}

// lockHeld reports whether another process holds the flock on path.
func lockHeld(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	return errors.Is(err, syscall.EWOULDBLOCK)
}
