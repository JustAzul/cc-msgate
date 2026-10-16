package runner

import (
	"strconv"
	"time"
)

// Shutdown bounds how long a terminated provider group is given.
type Shutdown struct {
	Grace time.Duration // from SIGTERM to SIGKILL
	Drain time.Duration // how long to keep reading the provider's pipes afterwards
}

// DefaultShutdown is what production runs use.
var DefaultShutdown = Shutdown{Grace: 5 * time.Second, Drain: 5 * time.Second}

// ShutdownFromEnv returns DefaultShutdown unless the environment carries the
// test-only overrides MSGATE_TEST_SHUTDOWN_GRACE_MS and
// MSGATE_TEST_SHUTDOWN_DRAIN_MS (positive whole milliseconds; anything else
// is ignored).
func ShutdownFromEnv(getenv func(string) string) Shutdown {
	s := DefaultShutdown
	if d, ok := millis(getenv("MSGATE_TEST_SHUTDOWN_GRACE_MS")); ok {
		s.Grace = d
	}
	if d, ok := millis(getenv("MSGATE_TEST_SHUTDOWN_DRAIN_MS")); ok {
		s.Drain = d
	}
	return s
}

func millis(v string) (time.Duration, bool) {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}
