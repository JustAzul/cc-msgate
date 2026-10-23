package store

import "encoding/json"

// State is runs/<run_id>/state.json (FRAME: Persisted formats).
type State struct {
	RunID             string  `json:"run_id"`
	ConversationID    string  `json:"conversation_id"`
	Turn              int     `json:"turn"`
	State             string  `json:"state"` // queued|running|done|failed|cancelled|timeout|lost
	Background        bool    `json:"background"`
	AdmittedAt        string  `json:"admitted_at"`
	StartedAt         *string `json:"started_at"`
	EndedAt           *string `json:"ended_at"`
	WorkerPID         int     `json:"worker_pid"`
	ProviderPGID      int     `json:"provider_pgid"`
	ProviderStartTime *string `json:"provider_start_time"`
	ExitCode          *int    `json:"exit_code"`
	Outcome           *string `json:"outcome"`
	// ProviderExit and SDKStatus are written at every terminal transition. A
	// state without SDKStatus predates them: readers derive both then.
	ProviderExit   *int    `json:"provider_exit"`
	SDKStatus      *string `json:"sdk_status"`
	ErrorExcerpt   *string `json:"error_excerpt"`
	UnparsedEvents int     `json:"unparsed_events"`
	OutputPath     string  `json:"output_path"`
	RunDir         string  `json:"run_dir"`
}

// Defaults are the first-turn settings a conversation keeps, with where each
// came from. A record written before the sources were kept has them empty.
type Defaults struct {
	Purpose       string `json:"purpose"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	Sandbox       string `json:"sandbox"`
	ModelSource   string `json:"model_source"`
	EffortSource  string `json:"effort_source"`
	SandboxSource string `json:"sandbox_source"`
	// Harness is the level the conversation runs at; a record written before
	// the level existed has it empty, which reads as full.
	Harness string `json:"harness,omitempty"`
}

// UnmarshalJSON reads a record written before the purpose had its name: its
// scenario is the purpose when no purpose is recorded.
func (d *Defaults) UnmarshalJSON(data []byte) error {
	type plain Defaults
	aux := struct {
		*plain
		LegacyScenario string `json:"scenario"`
	}{plain: (*plain)(d)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if d.Purpose == "" {
		d.Purpose = aux.LegacyScenario
	}
	return nil
}

// Conversation is conversations/<conversation_id>.json.
type Conversation struct {
	ConversationID    string   `json:"conversation_id"`
	Provider          string   `json:"provider"`
	ProviderSessionID *string  `json:"provider_session_id"`
	Cwd               string   `json:"cwd"`
	Defaults          Defaults `json:"defaults"`
	Turns             []string `json:"turns"`
	ActiveRunID       *string  `json:"active_run_id"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
}

// IsTerminal reports whether a run in this state will not change again.
func IsTerminal(state string) bool {
	switch state {
	case "done", "failed", "cancelled", "timeout", "lost":
		return true
	}
	return false
}
