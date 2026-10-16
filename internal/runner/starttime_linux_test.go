//go:build linux

package runner_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/runner"
)

func startTicks(t *testing.T, pid int) uint64 {
	t.Helper()
	s, err := runner.StartTime(pid)
	if err != nil {
		t.Fatalf("StartTime(%d): %v", pid, err)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatalf("StartTime(%d) = %q, want a decimal number of clock ticks", pid, s)
	}
	return n
}

func uptimeTicks(t *testing.T) uint64 {
	t.Helper()
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		t.Fatal(err)
	}
	secs, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(secs*100) + 100 // clock ticks are 100 Hz; allow a second of skew
}

func TestStartTimeOfThisProcessIsStable(t *testing.T) {
	a, b := startTicks(t, os.Getpid()), startTicks(t, os.Getpid())
	if a != b {
		t.Errorf("start time changed between reads: %d then %d", a, b)
	}
	if a > uptimeTicks(t) {
		t.Errorf("start time %d is later than the machine's uptime", a)
	}
}

// A process name may contain spaces and parentheses; the start time must come
// from the right field regardless.
func TestStartTimeSurvivesAnAwkwardProcessName(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	awkward := filepath.Join(t.TempDir(), "x) 1 y")
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(awkward, data, 0o755); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(awkward, "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	got := startTicks(t, child.Process.Pid)
	if self := startTicks(t, os.Getpid()); got < self || got > uptimeTicks(t) {
		t.Errorf("child start time %d outside [%d, uptime %d]", got, self, uptimeTicks(t))
	}
}

func TestStartTimeOfAGoneProcessIsAnError(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if s, err := runner.StartTime(cmd.Process.Pid); err == nil {
		t.Errorf("StartTime of a reaped process = %q, want an error", s)
	}
}
