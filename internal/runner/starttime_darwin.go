//go:build darwin

package runner

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// StartTime returns an opaque string identifying when the process started: the
// same pid at a later time is a different process. It is the kernel's process
// start timestamp, as seconds and microseconds since the epoch.
func StartTime(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	return startTimeOf(kp), nil
}

func startTimeOf(kp *unix.KinfoProc) string {
	tv := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", int64(tv.Sec), int64(tv.Usec))
}
