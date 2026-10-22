package codex

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UsedModel reads the model and effort codex ran a session with from its own
// session file: the last turn_context of
// $CODEX_HOME/sessions/<y>/<m>/<d>/rollout-<time>-<session id>.jsonl, which is
// the current turn's on a resumed session. It depends on that file layout: no
// such file yields "" for both, and one that cannot be read an error.
func (adapter) UsedModel(env []string, sessionID string) (model, effort string, err error) {
	path, err := sessionFile(env, sessionID)
	if err != nil || path == "" {
		return "", "", err
	}
	return lastTurnContext(path)
}

// sessionFile is the path of the session's file under the run's codex home,
// or "" when the id is empty or holds glob syntax, or no such file exists.
func sessionFile(env []string, sessionID string) (string, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `*?[]\/`) {
		return "", nil
	}
	home := codexHome(env)
	if home == "" {
		return "", nil
	}
	matches, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	return matches[len(matches)-1], nil
}

// codexHome is where codex keeps its state for the run's environment.
func codexHome(env []string) string {
	if home := lookupEnv(env, "CODEX_HOME"); home != "" {
		return home
	}
	if home := lookupEnv(env, "HOME"); home != "" {
		return filepath.Join(home, ".codex")
	}
	return ""
}

func lookupEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

// eachLine calls fn with each line of the file, whole however long (a large
// tool output) and the last one even without a newline.
func eachLine(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
		fn(line)
		if readErr != nil {
			return nil
		}
	}
}

// lastTurnContext keeps the model and effort of the file's last turn_context.
func lastTurnContext(path string) (model, effort string, err error) {
	marker := []byte(`"turn_context"`)
	err = eachLine(path, func(line []byte) {
		if !bytes.Contains(line, marker) {
			return
		}
		if m, e, ok := turnContext(line); ok {
			model, effort = m, e
		}
	})
	if err != nil {
		return "", "", err
	}
	return model, effort, nil
}

// turnContext is turnContextOf without the directory, and only for a turn that
// names its model.
func turnContext(line []byte) (model, effort string, ok bool) {
	_, model, effort, ok = turnContextOf(line)
	return model, effort, ok && model != ""
}
