//go:build linux

package runner

import (
	"syscall"
	"testing"
	"time"
)

func untilLeader(t *testing.T, pid int, startTime string, want leaderStatus) {
	t.Helper()
	got := probeLeader(pid, startTime)
	for deadline := time.Now().Add(2 * time.Second); got != want && time.Now().Before(deadline); got = probeLeader(pid, startTime) {
		time.Sleep(5 * time.Millisecond)
	}
	if got != want {
		t.Errorf("probeLeader(%d) = %v, want %v", pid, got, want)
	}
}

func TestProbeLeaderOfARunningProcess(t *testing.T) {
	cmd := sleeper(t)
	start, err := StartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	untilLeader(t, cmd.Process.Pid, start, leaderRunning)
	untilLeader(t, cmd.Process.Pid, "", leaderRunning)
}

func TestProbeLeaderOfAZombieAndOfAReapedProcess(t *testing.T) {
	cmd := sleeper(t)
	pid := cmd.Process.Pid
	start, err := StartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Kill(pid, syscall.SIGKILL)

	untilLeader(t, pid, start, leaderExited) // not reaped yet
	cmd.Wait()
	untilLeader(t, pid, start, leaderExited)
}

func TestProbeLeaderOfAPidWithAnotherStartTime(t *testing.T) {
	cmd := sleeper(t)
	untilLeader(t, cmd.Process.Pid, "1", leaderExited)
}

func TestParseProcStat(t *testing.T) {
	const line = "42 (a (b) c) S 1 42 42 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 98765 1000 100 18446744073709551615"
	state, start, ok := parseProcStat(line)
	if !ok || state != "S" || start != "98765" {
		t.Errorf("parseProcStat = %q, %q, %v; want S, 98765, true", state, start, ok)
	}
	if _, _, ok := parseProcStat("42 (short) S 1"); ok {
		t.Error("a truncated line parsed")
	}
	if _, _, ok := parseProcStat("no parenthesis"); ok {
		t.Error("a line without a name parsed")
	}
}
