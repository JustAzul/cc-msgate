package runner

import "time"

// leaderStatus is what the kernel says about the provider's group leader.
type leaderStatus int

const (
	leaderUnknown leaderStatus = iota // the platform cannot tell, or the probe failed
	leaderRunning                     // the leader is alive
	leaderExited                      // the leader has exited, reaped or not
)

// exitedWait bounds how long await waits for the delivery of an exit the
// kernel has already recorded.
const exitedWait = 5 * time.Second

// watchLeader makes j able to ask the kernel about the group leader pid. The
// start time guards against the pid having been reused after the leader was
// reaped; empty means the start time is not known.
func (j *Job) watchLeader(pid int, start *string) {
	startTime := ""
	if start != nil {
		startTime = *start
	}
	j.leaderProbe = func() leaderStatus { return probeLeader(pid, startTime) }
}

// exitedFirst reports the provider's own exit when the kernel shows the group
// leader gone while waitCh has not delivered yet: the signal or timeout that
// woke await came after the exit, so the provider's result stands. It reports
// false when the leader is not known to have exited, or when its exit is not
// delivered within exitedWait.
func (j *Job) exitedFirst(waitCh <-chan error) (ended, bool) {
	if j.leaderProbe == nil || j.leaderProbe() != leaderExited {
		return ended{}, false
	}
	t := time.NewTimer(exitedWait)
	defer t.Stop()
	select {
	case err := <-waitCh:
		return ended{waitErr: err, reaped: true}, true
	case <-t.C:
		return ended{}, false
	}
}
