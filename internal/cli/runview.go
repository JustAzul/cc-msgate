package cli

import (
	"errors"
	"os"

	"github.com/JustAzul/cc-msgate/internal/store"
)

// isTerminalState reports whether a run in this state will not change again.
func isTerminalState(state string) bool { return store.IsTerminal(state) }

// loadRun resolves a run id to its observed state. An id that names no run
// (including one that is not a valid run id) exits 4. The ids that exist are
// added to the JSON error object.
func loadRun(ctx *Context, st *store.Store, runID string) (store.State, int) {
	if store.ValidateRunID(runID) != nil {
		return store.State{}, ctx.Fail(ExitNotFound, "no run %q", runID)
	}
	ctx.IDs["run_id"] = runID
	state, err := observeRun(ctx, st, runID)
	if errors.Is(err, os.ErrNotExist) {
		delete(ctx.IDs, "run_id")
		return store.State{}, ctx.Fail(ExitNotFound, "no run %q", runID)
	}
	if err != nil {
		return store.State{}, ctx.Fail(ExitInternal, "reading run %s: %v", runID, err)
	}
	ctx.IDs["conversation_id"] = state.ConversationID
	readerGate(ctx)
	return state, 0
}

// sdkStatusOfRun is the sdk_status of a finished run: the recorded one, or for
// a state written before it was recorded, derived from the state. A run still
// in progress has none. A provider that itself exits 127 or 70 reads as the
// SDK code of the same number.
func sdkStatusOfRun(s store.State) *string {
	if s.SDKStatus != nil {
		return s.SDKStatus
	}
	var v string
	switch s.State {
	case "done":
		v = "ok"
	case "timeout", "cancelled", "lost":
		v = s.State
	case "failed":
		v = failedSDKStatus(s.ExitCode)
	default:
		return nil
	}
	return &v
}

func failedSDKStatus(exit *int) string {
	if exit != nil {
		switch *exit {
		case ExitProviderMissing:
			return "provider_missing"
		case ExitInternal:
			return "internal_error"
		}
	}
	return "ok"
}

// providerExitOfRun is the provider's own exit status: the recorded one, or
// for a state written before it was recorded, the exit of a run that ended on
// its own. A timed out, cancelled, lost or never-started run has none then.
func providerExitOfRun(s store.State) *int {
	if s.SDKStatus != nil {
		return s.ProviderExit
	}
	switch sdk := sdkStatusOfRun(s); {
	case sdk == nil || *sdk != "ok":
		return nil
	}
	return s.ExitCode
}

// runResult is the object a finished run prints with --json.
type runResult struct {
	ConversationID string  `json:"conversation_id"`
	RunID          string  `json:"run_id"`
	State          string  `json:"state"`
	Outcome        *string `json:"outcome"`
	SDKStatus      *string `json:"sdk_status"`
	ProviderExit   *int    `json:"provider_exit"`
	ExitCode       *int    `json:"exit_code"`
	OutputPath     string  `json:"output_path"`
	RunDir         string  `json:"run_dir"`
}

func resultOf(s store.State) runResult {
	return runResult{
		ConversationID: s.ConversationID, RunID: s.RunID, State: s.State, Outcome: s.Outcome,
		SDKStatus: sdkStatusOfRun(s), ProviderExit: providerExitOfRun(s), ExitCode: s.ExitCode,
		OutputPath: s.OutputPath, RunDir: s.RunDir,
	}
}

// runView is one run as status reports it: the result fields plus what the
// run was and when it ran.
type runView struct {
	runResult
	Turn       int     `json:"turn"`
	Background bool    `json:"background"`
	Command    string  `json:"command"`
	Scenario   string  `json:"scenario"`
	Source     string  `json:"source"`
	SessionID  *string `json:"session_id"`
	// AgentFeedback asks the run's Claude Code session to show it while it
	// works; the msgate plugin reads it here.
	AgentFeedback bool    `json:"agent_feedback"`
	AdmittedAt    string  `json:"admitted_at"`
	StartedAt     *string `json:"started_at"`
	EndedAt       *string `json:"ended_at"`
	ErrorExcerpt  *string `json:"error_excerpt"`
}

func viewOf(s store.State, req requestRecord) runView {
	return runView{
		runResult: resultOf(s), Turn: s.Turn, Background: s.Background,
		Command: req.Command, Scenario: req.Scenario, Source: req.Source, SessionID: nullable(req.SessionID),
		AgentFeedback: req.AgentFeedback, AdmittedAt: s.AdmittedAt, StartedAt: s.StartedAt, EndedAt: s.EndedAt, ErrorExcerpt: s.ErrorExcerpt,
	}
}

// kind says whether the run was a job or a foreground run.
func (v runView) kind() string {
	if v.Background {
		return "job"
	}
	return "foreground"
}
