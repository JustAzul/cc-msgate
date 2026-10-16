package runner_test

import (
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/runner"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestShutdownDefaultsAreFiveSeconds(t *testing.T) {
	want := runner.Shutdown{Grace: 5 * time.Second, Drain: 5 * time.Second}
	if runner.DefaultShutdown != want {
		t.Errorf("DefaultShutdown = %+v, want %+v", runner.DefaultShutdown, want)
	}
	if got := runner.ShutdownFromEnv(envOf(nil)); got != want {
		t.Errorf("ShutdownFromEnv(empty) = %+v, want the defaults %+v", got, want)
	}
}

func TestShutdownTestOverrides(t *testing.T) {
	got := runner.ShutdownFromEnv(envOf(map[string]string{
		"MSGATE_TEST_SHUTDOWN_GRACE_MS": "250", "MSGATE_TEST_SHUTDOWN_DRAIN_MS": "120",
	}))
	if want := (runner.Shutdown{Grace: 250 * time.Millisecond, Drain: 120 * time.Millisecond}); got != want {
		t.Errorf("overrides = %+v, want %+v", got, want)
	}
}

func TestShutdownIgnoresUnusableOverrides(t *testing.T) {
	for _, bad := range []string{"abc", "0", "-5", ""} {
		got := runner.ShutdownFromEnv(envOf(map[string]string{
			"MSGATE_TEST_SHUTDOWN_GRACE_MS": bad, "MSGATE_TEST_SHUTDOWN_DRAIN_MS": bad,
		}))
		if got != runner.DefaultShutdown {
			t.Errorf("override %q gave %+v, want the defaults", bad, got)
		}
	}
}
