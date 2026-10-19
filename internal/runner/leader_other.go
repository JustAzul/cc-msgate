//go:build !linux && !darwin

package runner

// probeLeader cannot tell on this platform.
func probeLeader(pid int, startTime string) leaderStatus { return leaderUnknown }
