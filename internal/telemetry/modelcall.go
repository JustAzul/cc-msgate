package telemetry

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// ModelCall is a model-call record: one completion a tool made outside a
// provider run. Field order is the on-disk order.
type ModelCall struct {
	V         int            `json:"v"`
	Kind      string         `json:"kind"`
	CallID    string         `json:"call_id"`
	TS        string         `json:"ts"`
	SessionID string         `json:"session_id"`
	Provider  string         `json:"provider"`
	Model     string         `json:"model"`
	Source    string         `json:"source"`
	RunID     *string        `json:"run_id"`
	Usage     provider.Usage `json:"usage"`
	// Attrs is what the caller said about the call. A reader that does not
	// know the field passes over it; without any the line has no such key.
	Attrs map[string]string `json:"attrs,omitempty"`
}

// AppendModelCall writes call as a model_call record to the month file of
// at's UTC month under home, under the same lock as Append. V and Kind are
// set here.
func AppendModelCall(home string, call ModelCall, at time.Time, opt Options) error {
	call.V = Version
	call.Kind = "model_call"
	line, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("encoding the model call: %w", err)
	}
	return AppendLine(home, line, at, opt)
}
