package cli

import (
	"strconv"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

// buildJob assembles the runner job of an admitted run from its resolved
// request. Foreground runs and background workers build it the same way, so
// a job behaves exactly like a foreground run.
func buildJob(ctx *Context, st *store.Store, prov provider.Provider, req requestRecord, state store.State, prompt []byte) runner.Job {
	var timeout time.Duration
	if req.TimeoutS != nil {
		timeout = time.Duration(*req.TimeoutS) * time.Second
	}
	return runner.Job{
		Store: st, Provider: prov, Plan: req.Plan, Prompt: prompt, Env: ctx.Env, State: state,
		CleanSentinel: req.CleanSentinel, MaterialLabel: req.MaterialLabel,
		Timeout:  timeout,
		Shutdown: runner.ShutdownFromEnv(ctx.Getenv),
		Warn:     func(msg string) { ctx.Warnf("%s", msg) }, Now: ctx.Now,
		Record: telemetryRecord(req, state.Background),
	}
}

// telemetryRecord holds the run-record fields known once a run is admitted.
func telemetryRecord(req requestRecord, background bool) telemetry.Record {
	return telemetry.Record{
		Provider: req.Provider, Command: req.Command, Scenario: req.Scenario,
		Model: req.Model, ModelSource: req.ModelSource,
		Effort: req.Effort, EffortSource: req.EffortSource,
		Sandbox: req.Sandbox, Source: req.Source, SessionID: nullable(req.SessionID),
		TimeoutS: req.TimeoutS, Cwd: req.Cwd, Background: background, Attrs: req.Attrs,
	}
}

// envMillis reads a test-only duration override: positive whole milliseconds;
// anything else is ignored.
func envMillis(getenv func(string) string, key string) (time.Duration, bool) {
	n, err := strconv.Atoi(getenv(key))
	if err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}
