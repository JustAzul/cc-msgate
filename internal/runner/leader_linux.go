//go:build linux

package runner

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// probeLeader reports whether the process pid has exited, from
// /proc/<pid>/stat: a zombie or a missing entry has exited, and so has a pid
// that now belongs to a process with another start time than startTime (empty
// skips that check).
func probeLeader(pid int, startTime string) leaderStatus {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return leaderExited
	}
	if err != nil {
		return leaderUnknown
	}
	state, start, ok := parseProcStat(string(data))
	switch {
	case !ok:
		return leaderUnknown
	case startTime != "" && start != startTime:
		return leaderExited
	case state == "Z" || state == "X":
		return leaderExited
	}
	return leaderRunning
}

// parseProcStat returns the state (field 3) and the start time (field 22) of a
// /proc/<pid>/stat line. The process name (field 2) may hold spaces and
// parentheses, so the fields are counted from the last closing parenthesis.
func parseProcStat(text string) (state, start string, ok bool) {
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return "", "", false
	}
	fields := strings.Fields(text[end+1:])
	const stateField, startTimeField = 3 - 3, 22 - 3
	if len(fields) <= startTimeField {
		return "", "", false
	}
	return fields[stateField], fields[startTimeField], true
}
