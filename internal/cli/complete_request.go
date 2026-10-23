package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Roles a message of a completion request can have.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// completeMessage is one turn of the conversation a completion answers.
type completeMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// completeRequest is what `complete` reads on standard input: the instructions
// that replace the harness's own for the call, when set, and the messages the
// answer follows.
type completeRequest struct {
	System   string            `json:"system"`
	Messages []completeMessage `json:"messages"`
}

// parseCompleteRequest reads one completion request. It refuses anything that
// is not one JSON object, a message whose text is not a non-empty string, a
// role other than user and assistant, and a conversation that does not end on
// a user message, which leaves nothing to answer.
func parseCompleteRequest(raw []byte) (completeRequest, error) {
	var req completeRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&req); err != nil {
		return completeRequest{}, fmt.Errorf("the request is not a JSON object with system and messages: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return completeRequest{}, errors.New("the request holds more than one JSON value")
	}
	if len(req.Messages) == 0 {
		return completeRequest{}, errors.New("the request needs at least one message")
	}
	for i, msg := range req.Messages {
		if msg.Role != roleUser && msg.Role != roleAssistant {
			return completeRequest{}, fmt.Errorf("message %d: role must be %q or %q, got %q", i, roleUser, roleAssistant, msg.Role)
		}
		if msg.Text == "" {
			return completeRequest{}, fmt.Errorf("message %d: needs a non-empty text", i)
		}
	}
	if req.Messages[len(req.Messages)-1].Role != roleUser {
		return completeRequest{}, errors.New("the last message must be a user message")
	}
	return req, nil
}

// prompt is what Codex receives: a lone user message as it is, several messages
// as a transcript.
func (r completeRequest) prompt() string {
	if len(r.Messages) == 1 {
		return r.Messages[0].Text
	}
	turns := make([]string, len(r.Messages))
	for i, msg := range r.Messages {
		turns[i] = strings.ToUpper(msg.Role[:1]) + msg.Role[1:] + ": " + msg.Text
	}
	return strings.Join(turns, "\n\n")
}
