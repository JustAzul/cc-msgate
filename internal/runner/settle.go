package runner

import (
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

// Exit codes of the runs another process settles on a worker's behalf.
const (
	exitLost      = 125
	exitCancelled = 130
)

// SDKStatusLost is the sdk_status of a run whose worker vanished.
const SDKStatusLost = "lost"

// settleUnfinished finalizes a run that no worker is finishing, in the order a
// finished run uses. The telemetry timestamp is when the run started, or when
// it was admitted if it never started. The caller holds the run lock.
func settleUnfinished(job Job, o outcome) Result {
	j := &job
	if j.Warn == nil {
		j.Warn = func(string) {}
	}
	if j.Now == nil {
		j.Now = time.Now
	}
	j.startedAt = j.Now()
	for _, recorded := range []string{j.State.AdmittedAt, deref(j.State.StartedAt)} {
		if at, err := time.Parse(time.RFC3339, recorded); err == nil {
			j.startedAt = at
		}
	}
	return j.finish(j.State, o)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CancelQueued finalizes a run that was cancelled before its worker took it:
// the state turns cancelled (exit 130), the conversation's marker is cleared
// and the telemetry record is appended. The caller holds the run lock.
func CancelQueued(job Job) Result {
	return settleUnfinished(job, outcome{
		state: "cancelled", exitCode: exitCancelled, label: "cancelled",
		excerpt: "cancelled before it started", sdkStatus: SDKStatusCancelled,
	})
}

// MarkLost finalizes a run whose worker is gone: the provider's process group
// is killed, but only when its leader still exists with the start time the
// state recorded, and the state turns lost (exit 125). The conversation's
// marker is cleared and the telemetry record is appended. The caller holds the
// run lock.
func MarkLost(job Job) Result {
	killRecordedGroup(job.State)
	return settleUnfinished(job, outcome{
		state: "lost", exitCode: exitLost, label: "lost",
		excerpt: "the run's worker is gone and left no final state", sdkStatus: SDKStatusLost,
	})
}

// killRecordedGroup SIGKILLs the provider group the state recorded when the
// group's leader is still the process that was recorded. A leader that is gone
// or has another start time means the group id belongs to something else, and
// nothing is signalled.
func killRecordedGroup(st store.State) {
	if st.ProviderPGID <= 1 || st.ProviderStartTime == nil {
		return
	}
	now, err := StartTime(st.ProviderPGID)
	if err != nil || now != *st.ProviderStartTime {
		return
	}
	syscall.Kill(-st.ProviderPGID, syscall.SIGKILL)
}
