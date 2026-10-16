package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/runner"
)

func TestForegroundRunIsVisibleToOtherShells(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "pong").set("FAKECODEX_SLEEP_MS", "2500")
	p := s.start("vis1", "exec", "--run-id", "vis1", "q")
	st := waitForState(t, s, "vis1", "a running state with the provider group", hasProviderGroup)

	if got := int(st["worker_pid"].(float64)); got != p.pid() {
		t.Errorf("worker_pid = %d, want the msgate pid %d", got, p.pid())
	}
	pgid := int(st["provider_pgid"].(float64))
	if own, err := syscall.Getpgid(p.pid()); err != nil || pgid == own {
		t.Errorf("provider_pgid = %d, msgate group = %d (%v); the provider needs a group of its own", pgid, own, err)
	}
	if got, err := syscall.Getpgid(pgid); err != nil || got != pgid {
		t.Errorf("pid %d is not the leader of group %d (getpgid = %d, %v)", pgid, pgid, got, err)
	}
	if want, err := runner.StartTime(pgid); err != nil || st["provider_start_time"] != want {
		t.Errorf("provider_start_time = %v, want the OS-reported %q (%v)", st["provider_start_time"], want, err)
	}
	lock := filepath.Join(s.home, "runs", "vis1", "lock")
	if !lockHeldElsewhere(t, lock) {
		t.Error("the run lock is free while the foreground run is in progress")
	}
	if r := s.run("send", st["conversation_id"].(string), "x"); r.code != 3 {
		t.Errorf("send while the run is in progress exit %d, want 3 (busy); stderr %s", r.code, r.stderr)
	}

	r := p.wait(15 * time.Second)
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	if lockHeldElsewhere(t, lock) {
		t.Error("the run lock is still held after the run finished")
	}
	if fin := stateOf(t, s, "vis1"); fin["state"] != "done" || int(fin["provider_pgid"].(float64)) != pgid {
		t.Errorf("final state = %v", fin)
	}
}

// shutdownEnv shortens the grace and drain waits so lifecycle tests run in
// seconds; the defaults are asserted separately.
func shutdownEnv(s *sandbox, grace, drain time.Duration) *sandbox {
	return s.set("MSGATE_TEST_SHUTDOWN_GRACE_MS", strconv.Itoa(int(grace.Milliseconds()))).
		set("MSGATE_TEST_SHUTDOWN_DRAIN_MS", strconv.Itoa(int(drain.Milliseconds())))
}

func TestTimeoutKillsTheWholeGroup(t *testing.T) {
	const grace, drain = 700 * time.Millisecond, 700 * time.Millisecond
	s := shutdownEnv(newSandbox(t), grace, drain).
		set("FAKECODEX_OUTPUT", "partial").set("FAKECODEX_SLEEP_MS", "60000").
		set("FAKECODEX_SPAWN_CHILD", "1").set("FAKECODEX_IGNORE_TERM", "1")
	p := s.start("to1", "exec", "--run-id", "to1", "--timeout", "2", "q")
	r := p.wait(12 * time.Second)
	elapsed := time.Since(p.start)

	out := filepath.Join(s.home, "runs", "to1", "output.md")
	if r.code != 124 {
		t.Fatalf("exit %d, want 124; stderr %s", r.code, r.stderr)
	}
	if r.stdout != out+"\n" {
		t.Errorf("stdout = %q, want the output path", r.stdout)
	}
	// The provider ignores SIGTERM, so the group is only killed after the grace.
	if lo, hi := 2*time.Second+grace, 2*time.Second+grace+drain+3*time.Second; elapsed < lo || elapsed > hi {
		t.Errorf("took %v, want within [%v, %v]", elapsed, lo, hi)
	}
	st := stateOf(t, s, "to1")
	for k, want := range map[string]any{"state": "timeout", "outcome": "timeout", "exit_code": float64(124)} {
		if st[k] != want {
			t.Errorf("state.%s = %v, want %v", k, st[k], want)
		}
	}
	assertGroupGone(t, int(st["provider_pgid"].(float64)))

	rec := oneRecord(t, s.home)
	for k, want := range map[string]any{"outcome": "timeout", "exit_code": float64(124), "timeout_s": float64(2)} {
		if rec[k] != want {
			t.Errorf("telemetry %s = %v, want %v", k, rec[k], want)
		}
	}
	if req := readJSONFile(t, filepath.Join(s.home, "runs", "to1", "request.json")); req["timeout_s"] != float64(2) {
		t.Errorf("request.json timeout_s = %v, want 2", req["timeout_s"])
	}
	conv := readJSONFile(t, filepath.Join(s.home, "conversations", st["conversation_id"].(string)+".json"))
	if conv["active_run_id"] != nil {
		t.Errorf("the conversation is still busy: %v", conv["active_run_id"])
	}
	if got := readFile(t, out); got != "partial" {
		t.Errorf("output.md = %q", got)
	}
}

func TestTimeoutOfACooperativeProviderDoesNotWaitForTheGrace(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 5*time.Second, time.Second).
		set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_SPAWN_CHILD", "1")
	p := s.start("to2", "exec", "--run-id", "to2", "--json", "--timeout", "1", "q")
	r := p.wait(12 * time.Second)
	if r.code != 124 {
		t.Fatalf("exit %d, want 124; stderr %s", r.code, r.stderr)
	}
	if elapsed := time.Since(p.start); elapsed > 4*time.Second {
		t.Errorf("took %v: a provider that exits on SIGTERM must not be held for the 5 s grace", elapsed)
	}
	j := r.json(t)
	for k, want := range map[string]any{"state": "timeout", "outcome": "timeout", "sdk_status": "timeout", "exit_code": float64(124)} {
		if j[k] != want {
			t.Errorf("json.%s = %v, want %v", k, j[k], want)
		}
	}
	// The grandchild shares the group and must be swept even though the leader
	// left on its own.
	assertGroupGone(t, int(stateOf(t, s, "to2")["provider_pgid"].(float64)))
}

// The provider leaves on SIGTERM but a descendant ignores it: the group is
// still swept, without waiting out the grace period.
func TestTimeoutSweepsADescendantThatIgnoresTerm(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 5*time.Second, time.Second).
		set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_SPAWN_CHILD", "1").set("FAKECODEX_CHILD_IGNORE_TERM", "1")
	p := s.start("to3", "exec", "--run-id", "to3", "--timeout", "1", "q")
	r := p.wait(12 * time.Second)
	if r.code != 124 {
		t.Fatalf("exit %d, want 124; stderr %s", r.code, r.stderr)
	}
	if elapsed := time.Since(p.start); elapsed > 4*time.Second {
		t.Errorf("took %v: the sweep must not wait for the grace period", elapsed)
	}
	assertGroupGone(t, int(stateOf(t, s, "to3")["provider_pgid"].(float64)))
}

func TestTimeoutFlagValidation(t *testing.T) {
	for _, bad := range []string{"abc", "0", "-3", "1.5", ""} {
		s := newSandbox(t).set("FAKECODEX_OUTPUT", "x")
		r := s.run("exec", "--run-id", "bad1", "--timeout", bad, "q")
		if r.code != 2 {
			t.Errorf("--timeout %q: exit %d, want 2", bad, r.code)
		}
		if exists(filepath.Join(s.home, "runs", "bad1")) {
			t.Errorf("--timeout %q: a run directory was created", bad)
		}
	}
}

// pipeHolder arranges for the fake provider to leave behind a process, outside
// its group, that keeps its stdout and stderr open. The process is killed at
// cleanup.
func pipeHolder(s *sandbox) *sandbox {
	pidFile := filepath.Join(s.t.TempDir(), "holder.pid")
	s.set("FAKECODEX_PIPE_HOLDER", pidFile)
	s.t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return s
}

func TestTimeoutDoesNotWaitForADescendantHoldingThePipes(t *testing.T) {
	const grace, drain = 300 * time.Millisecond, 600 * time.Millisecond
	s := pipeHolder(shutdownEnv(newSandbox(t), grace, drain)).
		set("FAKECODEX_OUTPUT", "partial").set("FAKECODEX_SLEEP_MS", "60000")
	p := s.start("hold1", "exec", "--run-id", "hold1", "--timeout", "1", "q")
	r := p.wait(15 * time.Second)
	if r.code != 124 {
		t.Fatalf("exit %d, want 124; stderr %s", r.code, r.stderr)
	}
	if limit := time.Second + grace + drain + 3*time.Second; time.Since(p.start) > limit {
		t.Errorf("took %v, want at most %v: the pipe drain is bounded", time.Since(p.start), limit)
	}
	if want := filepath.Join(s.home, "runs", "hold1", "output.md") + "\n"; r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
	if st := stateOf(t, s, "hold1"); st["state"] != "timeout" {
		t.Errorf("state = %v", st["state"])
	}
}

func TestExitDoesNotWaitForADescendantHoldingThePipes(t *testing.T) {
	const drain = 500 * time.Millisecond
	s := pipeHolder(shutdownEnv(newSandbox(t), time.Second, drain)).set("FAKECODEX_OUTPUT", "pong")
	p := s.start("hold2", "exec", "--run-id", "hold2", "q")
	r := p.wait(15 * time.Second)
	if r.code != 0 {
		t.Fatalf("exit %d, want the provider's 0; stderr %s", r.code, r.stderr)
	}
	if limit := drain + 3*time.Second; time.Since(p.start) > limit {
		t.Errorf("took %v, want at most %v", time.Since(p.start), limit)
	}
	if st := stateOf(t, s, "hold2"); st["state"] != "done" || st["outcome"] != "ok" {
		t.Errorf("state = %v / %v", st["state"], st["outcome"])
	}
}

// The command's wall time is the timeout plus the grace period plus the pipe
// drain, however badly the provider behaves.
func TestHookBudgetIsTimeoutPlusGracePlusDrain(t *testing.T) {
	const grace, drain = 800 * time.Millisecond, 800 * time.Millisecond
	s := pipeHolder(shutdownEnv(newSandbox(t), grace, drain)).
		set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_IGNORE_TERM", "1").set("FAKECODEX_SPAWN_CHILD", "1")
	p := s.start("budget1", "exec", "--run-id", "budget1", "--timeout", "2", "q")
	r := p.wait(15 * time.Second)
	elapsed := time.Since(p.start)
	if r.code != 124 {
		t.Fatalf("exit %d, want 124; stderr %s", r.code, r.stderr)
	}
	if lo, hi := 2*time.Second+grace, 2*time.Second+grace+drain+2*time.Second; elapsed < lo || elapsed > hi {
		t.Errorf("took %v, want within [%v, %v]", elapsed, lo, hi)
	}
	assertGroupGone(t, int(stateOf(t, s, "budget1")["provider_pgid"].(float64)))
}

func TestInterruptCancelsTheRun(t *testing.T) {
	cases := []struct {
		name          string
		sig           syscall.Signal
		cancelRequest bool
		want          int
	}{
		{"SIGTERM", syscall.SIGTERM, false, 143},
		{"SIGINT", syscall.SIGINT, false, 130},
		{"SIGTERM with a cancel request", syscall.SIGTERM, true, 130},
		{"SIGINT with a cancel request", syscall.SIGINT, true, 130},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := "int" + strconv.Itoa(i)
			s := shutdownEnv(newSandbox(t), 500*time.Millisecond, 500*time.Millisecond).
				set("FAKECODEX_OUTPUT", "partial").set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_SPAWN_CHILD", "1")
			p := s.start(id, "exec", "--run-id", id, "q")
			st := waitForState(t, s, id, "a running state with the provider group", hasProviderGroup)
			if c.cancelRequest {
				if err := os.WriteFile(filepath.Join(s.home, "runs", id, "cancel.request"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p.signal(c.sig)
			r := p.wait(10 * time.Second)
			if r.code != c.want {
				t.Fatalf("exit %d, want %d; stderr %s", r.code, c.want, r.stderr)
			}
			if want := filepath.Join(s.home, "runs", id, "output.md") + "\n"; r.stdout != want {
				t.Errorf("stdout = %q, want %q", r.stdout, want)
			}
			fin := stateOf(t, s, id)
			for k, want := range map[string]any{"state": "cancelled", "outcome": "cancelled", "exit_code": float64(c.want)} {
				if fin[k] != want {
					t.Errorf("state.%s = %v, want %v", k, fin[k], want)
				}
			}
			assertGroupGone(t, int(st["provider_pgid"].(float64)))
			rec := oneRecord(t, s.home)
			if rec["outcome"] != "cancelled" || rec["exit_code"] != float64(c.want) {
				t.Errorf("telemetry = %v / %v", rec["outcome"], rec["exit_code"])
			}
			conv := readJSONFile(t, filepath.Join(s.home, "conversations", st["conversation_id"].(string)+".json"))
			if conv["active_run_id"] != nil {
				t.Errorf("the conversation is still busy: %v", conv["active_run_id"])
			}
		})
	}
}

func TestInterruptEscalatesToKillWhenTheProviderIgnoresTerm(t *testing.T) {
	const grace = 600 * time.Millisecond
	s := shutdownEnv(newSandbox(t), grace, 500*time.Millisecond).
		set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_IGNORE_TERM", "1").set("FAKECODEX_SPAWN_CHILD", "1")
	p := s.start("esc1", "exec", "--run-id", "esc1", "--json", "q")
	st := waitForState(t, s, "esc1", "a running state with the provider group", hasProviderGroup)
	// The provider ignores SIGTERM only once it is up; its grandchild starts after that.
	untilTrue(t, "the provider's grandchild", func() bool { return len(liveInGroup(t, int(st["provider_pgid"].(float64)))) >= 2 })
	signalled := time.Now()
	p.signal(syscall.SIGTERM)
	r := p.wait(10 * time.Second)
	if r.code != 143 {
		t.Fatalf("exit %d, want 143; stderr %s", r.code, r.stderr)
	}
	if took := time.Since(signalled); took < grace || took > grace+3*time.Second {
		t.Errorf("shutdown took %v, want the %v grace plus a small margin", took, grace)
	}
	if j := r.json(t); j["sdk_status"] != "cancelled" || j["outcome"] != "cancelled" || j["state"] != "cancelled" {
		t.Errorf("json = %v", j)
	}
	assertGroupGone(t, int(st["provider_pgid"].(float64)))
}

// holdLock takes a flock on path for the test and returns the release.
func holdLock(t *testing.T, path string) (release func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			f.Close()
		}
	}
	t.Cleanup(release)
	return release
}

func untilTrue(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func isTerminal(m map[string]any) bool {
	switch m["state"] {
	case "done", "failed", "cancelled", "timeout", "lost":
		return true
	}
	return false
}

// The foreground run finishes in a fixed order: output, terminal state, the
// conversation's marker, the telemetry record. Holding the conversation and
// telemetry locks stretches the gaps between those steps so each can be seen.
func TestForegroundTerminalOrdering(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "final").set("FAKECODEX_SLEEP_MS", "1000")
	p := s.start("ord1", "exec", "--run-id", "ord1", "q")
	st := waitForState(t, s, "ord1", "a running state with the provider group", hasProviderGroup)
	convID := st["conversation_id"].(string)
	releaseConv := holdLock(t, filepath.Join(s.home, "conversations", convID+".lock"))
	releaseTel := holdLock(t, filepath.Join(s.home, "telemetry", ".lock"))
	convFile := filepath.Join(s.home, "conversations", convID+".json")
	telFiles := func() []string { m, _ := filepath.Glob(filepath.Join(s.home, "telemetry", "*.jsonl")); return m }

	fin := waitForState(t, s, "ord1", "a terminal state", isTerminal)
	if got := readFile(t, fin["output_path"].(string)); got != "final" {
		t.Errorf("output.md = %q when the state turned terminal, want the final bytes", got)
	}
	time.Sleep(150 * time.Millisecond)
	if conv := readJSONFile(t, convFile); conv["active_run_id"] != "ord1" {
		t.Errorf("the marker was cleared before the conversation lock was released: %v", conv["active_run_id"])
	}
	if f := telFiles(); len(f) != 0 {
		t.Errorf("telemetry written before the marker was cleared: %v", f)
	}

	releaseConv()
	untilTrue(t, "the marker to be cleared", func() bool { return readJSONFile(t, convFile)["active_run_id"] == nil })
	time.Sleep(150 * time.Millisecond)
	if !isTerminal(stateOf(t, s, "ord1")) {
		t.Error("the marker was cleared while the state was not terminal")
	}
	if f := telFiles(); len(f) != 0 {
		t.Errorf("telemetry written while its lock was held: %v", f)
	}

	releaseTel()
	if r := p.wait(10 * time.Second); r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	if rec := oneRecord(t, s.home); rec["run_id"] != "ord1" || rec["outcome"] != "ok" {
		t.Errorf("record = %v", rec)
	}
}

func TestMarkerIsClearedOnlyByTheRunItNames(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "pong").set("FAKECODEX_SLEEP_MS", "1000")
	p := s.start("mk1", "exec", "--run-id", "mk1", "q")
	st := waitForState(t, s, "mk1", "a running state with the provider group", hasProviderGroup)
	convFile := filepath.Join(s.home, "conversations", st["conversation_id"].(string)+".json")

	// Another run has since taken over the conversation.
	release := holdLock(t, convFile[:len(convFile)-len(".json")]+".lock")
	conv := readJSONFile(t, convFile)
	conv["active_run_id"] = "r-other"
	data, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(convFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	release()

	if r := p.wait(10 * time.Second); r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	if got := readJSONFile(t, convFile)["active_run_id"]; got != "r-other" {
		t.Errorf("active_run_id = %v, want r-other: a run clears only its own marker", got)
	}
	if st := stateOf(t, s, "mk1"); st["state"] != "done" {
		t.Errorf("state = %v", st["state"])
	}
}

// Replacing state.json with a directory makes the terminal state write fail
// whoever runs the test.
func breakStateFile(t *testing.T, s *sandbox, runID string) {
	t.Helper()
	path := filepath.Join(s.home, "runs", runID, "state.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStateWriteFailureOnlyWarns(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "pong").set("FAKECODEX_SLEEP_MS", "1000").set("FAKECODEX_EXIT", "3")
	p := s.start("sf1", "exec", "--run-id", "sf1", "q")
	waitForState(t, s, "sf1", "a running state with the provider group", hasProviderGroup)
	breakStateFile(t, s, "sf1")

	r := p.wait(10 * time.Second)
	if r.code != 3 {
		t.Fatalf("exit %d, want the provider's 3; stderr %s", r.code, r.stderr)
	}
	if w := warningLines(r.stderr); len(w) != 1 || !strings.Contains(w[0], "terminal state") {
		t.Errorf("warnings = %q, want exactly one about the terminal state", w)
	}
	out := filepath.Join(s.home, "runs", "sf1", "output.md")
	if r.stdout != out+"\n" {
		t.Errorf("stdout = %q", r.stdout)
	}
	if lockHeldElsewhere(t, filepath.Join(s.home, "runs", "sf1", "lock")) {
		t.Error("the run lock is still held, so the run could not be reconciled as lost")
	}
	if left, _ := filepath.Glob(filepath.Join(s.home, "runs", "sf1", ".state.json.tmp-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
	if rec := oneRecord(t, s.home); rec["exit_code"] != float64(3) {
		t.Errorf("telemetry = %v", rec)
	}
}

func TestStderrTailWriteFailureOnlyWarns(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "pong").set("FAKECODEX_SLEEP_MS", "1000").set("FAKECODEX_STDERR", "boom\n")
	p := s.start("st1", "exec", "--run-id", "st1", "q")
	waitForState(t, s, "st1", "a running state with the provider group", hasProviderGroup)
	if err := os.MkdirAll(filepath.Join(s.home, "runs", "st1", "stderr.tail", "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := p.wait(10 * time.Second)
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	if w := warningLines(r.stderr); len(w) != 1 || !strings.Contains(w[0], "stderr.tail") {
		t.Errorf("warnings = %q, want exactly one about stderr.tail", w)
	}
	if st := stateOf(t, s, "st1"); st["state"] != "done" {
		t.Errorf("state = %v", st["state"])
	}
}

// Without the test overrides a provider that ignores SIGTERM is given the
// production 5-second grace before the group is killed.
func TestProductionGraceIsFiveSeconds(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_SLEEP_MS", "60000").set("FAKECODEX_IGNORE_TERM", "1").set("FAKECODEX_SPAWN_CHILD", "1")
	p := s.start("prod1", "exec", "--run-id", "prod1", "--timeout", "1", "q")
	r := p.wait(20 * time.Second)
	if r.code != 124 {
		t.Fatalf("exit %d, want 124; stderr %s", r.code, r.stderr)
	}
	if elapsed := time.Since(p.start); elapsed < 6*time.Second || elapsed > 6*time.Second+5*time.Second+2*time.Second {
		t.Errorf("took %v, want the 1 s timeout plus the 5 s grace (and at most the 5 s drain more)", elapsed)
	}
	assertGroupGone(t, int(stateOf(t, s, "prod1")["provider_pgid"].(float64)))
}
