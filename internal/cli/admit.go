package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/settings"
	"github.com/JustAzul/cc-msgate/internal/store"
)

type admission struct {
	command        string // exec, review or send
	runID          string
	conversationID string
	existing       *store.Conversation // nil: this turn starts a new conversation
	flags          turnFlags
	effective      settings.Resolved
	cwd            string
	prompt         []byte
	passthrough    []string
	plan           provider.Plan
	background     bool // the run is a job executed by a detached worker
	now            time.Time

	// providerSessionID is the session a new conversation holds from its
	// creation; empty when the first turn's run reports it.
	providerSessionID string

	// relay is set when the turn passes the prompt on to a child session.
	relay *relayRecord
}

// requestRecord is runs/<run_id>/request.json: the resolved request and plan.
type requestRecord struct {
	Command       string         `json:"command"`
	Provider      string         `json:"provider"`
	Purpose       string         `json:"purpose"`
	Model         *string        `json:"model"`
	ModelSource   string         `json:"model_source"`
	Effort        *string        `json:"effort"`
	EffortSource  string         `json:"effort_source"`
	Sandbox       *string        `json:"sandbox"`
	SandboxSource string         `json:"sandbox_source"`
	Harness       string         `json:"harness"`
	Cwd           string         `json:"cwd"`
	Source        string         `json:"source"`
	SessionID     string         `json:"session_id"`
	AgentFeedback bool           `json:"agent_feedback"`
	TimeoutS      *int           `json:"timeout_s"`
	CleanSentinel string         `json:"clean_sentinel"`
	MaterialLabel string         `json:"material_label"`
	Attrs         map[string]any `json:"attrs"`
	Passthrough   []string       `json:"passthrough"`
	Plan          provider.Plan  `json:"plan"`
	Relay         *relayRecord   `json:"relay,omitempty"`
}

// UnmarshalJSON reads a request written before the purpose had its name: its
// scenario is the purpose when no purpose is recorded.
func (r *requestRecord) UnmarshalJSON(data []byte) error {
	type plain requestRecord
	aux := struct {
		*plain
		LegacyScenario string `json:"scenario"`
	}{plain: (*plain)(r)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // attrs keep integers beyond float precision
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	if r.Purpose == "" {
		r.Purpose = aux.LegacyScenario
	}
	return nil
}

// admit reserves the turn on its conversation (creating the conversation for
// a first turn), then creates the run directory and writes prompt.md,
// request.json and the queued state. A conversation with a turn still queued
// or running yields a *store.BusyError and nothing is written. If the run
// cannot be created, the reservation is released. A first turn that adopts a
// provider session some conversation holds by then creates nothing and yields
// a *heldError: the turn is for the holder to run.
func admit(st *store.Store, a admission) (store.State, error) {
	turn, err := reserve(st, a)
	if err != nil {
		return store.State{}, err
	}
	state, err := writeRun(st, a, turn)
	if err != nil {
		_ = st.Release(a.conversationID, a.runID)
		return store.State{}, err
	}
	return state, nil
}

func reserve(st *store.Store, a admission) (int, error) {
	if a.existing != nil {
		return st.Reserve(a.conversationID, a.runID, a.now)
	}
	f, e := a.flags, a.effective
	ts := a.now.Format(time.RFC3339)
	c := store.Conversation{
		ConversationID: a.conversationID, Provider: f.provider, ProviderSessionID: nullable(a.providerSessionID), Cwd: a.cwd,
		Defaults: store.Defaults{
			Purpose: f.purpose, Model: e.Model, Effort: e.Effort, Sandbox: e.Sandbox,
			ModelSource: e.ModelSource, EffortSource: e.EffortSource, SandboxSource: e.SandboxSource,
			Harness: f.harness,
		},
		CreatedAt: ts, UpdatedAt: ts,
	}
	if a.providerSessionID == "" {
		return 1, st.ReserveNew(c, a.runID, a.now)
	}
	holder, turn, err := reserveAdopting(st, c, a.runID, a.now, adoptionPatience)
	if err == nil && holder.ConversationID != "" {
		return 0, &heldError{holder: holder}
	}
	return turn, err
}

// heldError means the provider session a first turn was to adopt is held, by
// the time of the reservation, by a conversation that already exists.
type heldError struct{ holder store.Conversation }

func (e *heldError) Error() string {
	return "the provider session is held by conversation " + e.holder.ConversationID
}

// request is the resolved request the run's request.json holds.
func (a admission) request() requestRecord {
	f, e := a.flags, a.effective
	passthrough := a.passthrough
	if passthrough == nil {
		passthrough = []string{}
	}
	return requestRecord{
		Command: a.command, Provider: f.provider, Purpose: f.purpose,
		Model: nullable(e.Model), ModelSource: e.ModelSource,
		Effort: nullable(e.Effort), EffortSource: e.EffortSource,
		Sandbox: nullable(e.Sandbox), SandboxSource: e.SandboxSource, Harness: f.harness,
		Cwd: a.cwd, Source: f.source, SessionID: f.sessionID, AgentFeedback: f.agentFeedback, TimeoutS: timeoutSeconds(f.timeoutS),
		CleanSentinel: f.cleanSentinel, MaterialLabel: f.materialLabel,
		Attrs: f.attrs.object(), Passthrough: passthrough, Plan: a.plan, Relay: a.relay,
	}
}

func writeRun(st *store.Store, a admission, turn int) (store.State, error) {
	if err := st.CreateRunDir(a.runID); err != nil {
		return store.State{}, err
	}
	if err := st.WritePrompt(a.runID, a.prompt); err != nil {
		return store.State{}, err
	}
	if err := st.WriteRequest(a.runID, a.request()); err != nil {
		return store.State{}, err
	}
	state := store.State{
		RunID: a.runID, ConversationID: a.conversationID, Turn: turn, State: "queued", Background: a.background,
		AdmittedAt: a.now.Format(time.RFC3339), WorkerPID: os.Getpid(),
		OutputPath: st.OutputPath(a.runID), RunDir: st.RunDir(a.runID),
	}
	if err := st.WriteState(a.runID, state); err != nil {
		return store.State{}, err
	}
	return state, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timeoutSeconds is the recorded timeout: null when the run has none.
func timeoutSeconds(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// adoptionWait bounds how long an adoption waits for a turn that has not
// reported its provider session, and how often it looks again.
type adoptionWait struct {
	deadline, poll time.Duration
	// onPending, when set, is called each time an attempt finds such a turn.
	onPending func()
}

// adoptionPatience is how long an adoption waits. A session whose rollout
// exists was announced by the Codex that started it, so a turn still silent
// after this long is not that Codex.
var adoptionPatience = adoptionWait{deadline: 5 * time.Second, poll: 50 * time.Millisecond}

// reserveAdopting is st.ReserveAdopted, retried while a turn that has not
// reported its session may turn out to hold the one being adopted. It never
// sleeps with the adoption lock held: each attempt takes and drops it. Once the
// wait is spent the adoption goes ahead whatever is pending.
func reserveAdopting(st *store.Store, c store.Conversation, runID string, at time.Time, w adoptionWait) (store.Conversation, int, error) {
	give := time.Now().Add(w.deadline)
	for {
		if !time.Now().Before(give) {
			return st.ReserveAdoptedRegardless(c, runID, at)
		}
		holder, turn, err := st.ReserveAdopted(c, runID, at)
		if !errors.Is(err, store.ErrSessionPending) {
			return holder, turn, err
		}
		if w.onPending != nil {
			w.onPending()
		}
		time.Sleep(w.poll)
	}
}
