package codex

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// uuidPattern is the one shape codex gives a session id.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ReadSession reads one codex session from its own file under the run's codex
// home: the directory and, when it has a turn, the model and effort of the
// last turn_context, with the directory of session_meta standing in for a
// session without one. Anything but a canonical UUID, and a UUID with no such
// file, is not a session of codex.
func (adapter) ReadSession(env []string, id string) (provider.Session, bool, error) {
	if !uuidPattern.MatchString(id) {
		return provider.Session{}, false, nil
	}
	path, err := sessionFile(env, id)
	if err != nil || path == "" {
		return provider.Session{}, false, err
	}
	s, err := readSessionFile(path)
	if err != nil {
		return provider.Session{}, false, err
	}
	return s, true, nil
}

// readSessionFile keeps the session_meta directory and the last turn_context
// of the file.
func readSessionFile(path string) (provider.Session, error) {
	var s provider.Session
	metaCwd := ""
	meta, turn := []byte(`"session_meta"`), []byte(`"turn_context"`)
	err := eachLine(path, func(line []byte) {
		switch {
		case metaCwd == "" && bytes.Contains(line, meta):
			metaCwd = sessionMetaCwd(line)
		case bytes.Contains(line, turn):
			if cwd, model, effort, ok := turnContextOf(line); ok {
				s.Cwd, s.Model, s.Effort = cwd, model, effort
			}
		}
	})
	if err != nil {
		return provider.Session{}, err
	}
	if s.Cwd == "" {
		s.Cwd = metaCwd
	}
	return s, nil
}

func sessionMetaCwd(line []byte) string {
	var item struct {
		Type    string `json:"type"`
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &item) != nil || item.Type != "session_meta" {
		return ""
	}
	return item.Payload.Cwd
}

func turnContextOf(line []byte) (cwd, model, effort string, ok bool) {
	var item struct {
		Type    string `json:"type"`
		Payload struct {
			Cwd    string `json:"cwd"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &item) != nil || item.Type != "turn_context" {
		return "", "", "", false
	}
	return item.Payload.Cwd, item.Payload.Model, item.Payload.Effort, true
}
