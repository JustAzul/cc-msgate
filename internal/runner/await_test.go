package runner

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// trigger is what interrupts the wait on the provider.
type trigger struct {
	name    string
	timeout time.Duration // zero: no timeout
	signal  bool
	reason  endReason // what await reports when it terminates the provider
}

var triggers = []trigger{
	{name: "signal", signal: true, reason: endSignal},
	{name: "timeout", timeout: 10 * time.Millisecond, reason: endTimeout},
}

func (tr trigger) job(probe func() leaderStatus) (*Job, chan os.Signal) {
	j := &Job{Timeout: tr.timeout, Shutdown: Shutdown{Grace: 2 * time.Second, Drain: 2 * time.Second}, leaderProbe: probe}
	sigs := make(chan os.Signal, 2)
	if tr.signal {
		sigs <- syscall.SIGTERM
	}
	return j, sigs
}

// sleeper starts a process that leads a group of its own and is killed at cleanup.
func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	return cmd
}

func probeOf(s leaderStatus) func() leaderStatus { return func() leaderStatus { return s } }

// A leader that exited before the signal or the timeout, whose exit the wait
// has not delivered yet, keeps its own result.
func TestAwaitKeepsTheResultOfALeaderThatAlreadyExited(t *testing.T) {
	for _, tr := range triggers {
		t.Run(tr.name, func(t *testing.T) {
			cmd := sleeper(t)
			j, sigs := tr.job(probeOf(leaderExited))
			waitCh := make(chan error, 1)
			go func() {
				time.Sleep(80 * time.Millisecond)
				waitCh <- nil
			}()

			end := j.await(cmd.Process.Pid, waitCh, sigs)

			if end.reason != endExited || !end.reaped || end.waitErr != nil {
				t.Errorf("await = %+v, want the provider's own exit (endExited, reaped, no error)", end)
			}
		})
	}
}

// Without proof that the leader exited, the signal or the timeout ends the group.
func TestAwaitTerminatesALeaderNotKnownToHaveExited(t *testing.T) {
	probes := map[string]func() leaderStatus{
		"no probe": nil, "running": probeOf(leaderRunning), "unknown": probeOf(leaderUnknown),
	}
	for _, tr := range triggers {
		for name, probe := range probes {
			t.Run(tr.name+"/"+name, func(t *testing.T) {
				cmd := sleeper(t)
				j, sigs := tr.job(probe)
				waitCh := make(chan error, 1)
				go func() { waitCh <- cmd.Wait() }()

				end := j.await(cmd.Process.Pid, waitCh, sigs)

				if end.reason != tr.reason || !end.reaped {
					t.Errorf("await = %+v, want reason %v and a reaped leader", end, tr.reason)
				}
			})
		}
	}
}
