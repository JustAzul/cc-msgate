//go:build darwin

package runner

import "golang.org/x/sys/unix"

// szomb is the zombie value of the process state (SZOMB in <sys/proc.h>);
// x/sys does not export it.
const szomb = 5

// probeLeader reports whether the process pid has exited: a zombie has, and so
// has a pid that now belongs to a process with another start time than
// startTime (empty skips that check). A failed lookup, which is also what a
// pid that no longer exists produces, is unknown.
func probeLeader(pid int, startTime string) leaderStatus {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return leaderUnknown
	}
	switch {
	case startTime != "" && startTimeOf(kp) != startTime:
		return leaderExited
	case kp.Proc.P_stat == szomb:
		return leaderExited
	}
	return leaderRunning
}
