// Package store owns the on-disk layout: home resolution, run directories,
// state files and conversation records.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// ErrRunExists is returned when a run directory already exists.
var ErrRunExists = errors.New("run already exists")

// ResolveHome applies MSGATE_HOME, then $XDG_STATE_HOME/msgate, then
// ~/.local/state/msgate.
func ResolveHome(getenv func(string) string) (string, error) {
	if h := getenv("MSGATE_HOME"); h != "" {
		return h, nil
	}
	if x := getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "msgate"), nil
	}
	if h := getenv("HOME"); h != "" {
		return filepath.Join(h, ".local", "state", "msgate"), nil
	}
	return "", errors.New("cannot resolve the msgate home: set MSGATE_HOME")
}

// Store is the msgate home directory.
type Store struct{ Home string }

// Open returns a store rooted at home. It touches nothing on disk.
func Open(home string) *Store { return &Store{Home: home} }

// RunDir is the directory of one run.
func (s *Store) RunDir(runID string) string { return filepath.Join(s.Home, "runs", runID) }

// OutputPath is the provider's final-message file for a run.
func (s *Store) OutputPath(runID string) string { return filepath.Join(s.RunDir(runID), "output.md") }

// HarnessInstructionsPath is the file holding the instructions a provider runs
// on in place of its own at the lean and bare harness levels.
func (s *Store) HarnessInstructionsPath() string {
	return filepath.Join(s.Home, "harness", "instructions.md")
}

// EnsureHarnessInstructions leaves HarnessInstructionsPath holding text,
// writing it only when the file is missing or different. The write replaces the
// file whole, so a run reading it concurrently sees the old text or the new,
// never a part.
func (s *Store) EnsureHarnessInstructions(text string) error {
	path := s.HarnessInstructionsPath()
	if current, err := os.ReadFile(path); err == nil && string(current) == text {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(text))
}

// RunExists reports whether a run directory exists.
func (s *Store) RunExists(runID string) bool {
	_, err := os.Lstat(s.RunDir(runID))
	return err == nil
}

// CreateRunDir creates runs/<id> and fails with ErrRunExists if it is present.
func (s *Store) CreateRunDir(runID string) error {
	if err := os.MkdirAll(filepath.Join(s.Home, "runs"), dirMode); err != nil {
		return err
	}
	if err := os.Mkdir(s.RunDir(runID), dirMode); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrRunExists
		}
		return err
	}
	return nil
}

func (s *Store) writeRunFile(runID, name string, data []byte) error {
	return writeFileAtomic(filepath.Join(s.RunDir(runID), name), data)
}

// WritePrompt stores the full prompt.
func (s *Store) WritePrompt(runID string, prompt []byte) error {
	return s.writeRunFile(runID, "prompt.md", prompt)
}

// ReadPrompt returns the full prompt stored for the run.
func (s *Store) ReadPrompt(runID string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.RunDir(runID), "prompt.md"))
}

// WriteRequest stores the resolved request.
func (s *Store) WriteRequest(runID string, request any) error {
	return s.writeJSON(filepath.Join(s.RunDir(runID), "request.json"), request)
}

// WriteStderrTail stores the tail of the provider's stderr.
func (s *Store) WriteStderrTail(runID string, tail []byte) error {
	return s.writeRunFile(runID, "stderr.tail", tail)
}

// ReadRequest decodes the run's request.json into v. Numbers decoded into an
// interface keep their exact digits (json.Number).
func (s *Store) ReadRequest(runID string, v any) error {
	return readJSONExact(filepath.Join(s.RunDir(runID), "request.json"), v)
}

// RunIDs lists the ids of the run directories under the home, in no
// particular order. A home with no runs yet lists none. A directory whose name
// starts with a dot is never a run (no run id may), so it is not listed.
func (s *Store) RunIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Home, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// ConversationIDs lists the ids of the stored conversations, in no
// particular order.
func (s *Store) ConversationIDs() ([]string, error) {
	entries, err := os.ReadDir(s.conversationsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// FindByProviderSession returns the conversation of the provider that holds
// the provider session id. The error wraps os.ErrNotExist when none does.
func (s *Store) FindByProviderSession(providerName, sessionID string) (Conversation, error) {
	ids, err := s.ConversationIDs()
	if err != nil {
		return Conversation{}, err
	}
	for _, id := range ids {
		c, err := s.ReadConversation(id)
		if errors.Is(err, os.ErrNotExist) {
			continue // released since the listing
		}
		if err != nil {
			return Conversation{}, err
		}
		if c.Provider == providerName && c.ProviderSessionID != nil && *c.ProviderSessionID == sessionID {
			return c, nil
		}
	}
	return Conversation{}, fmt.Errorf("no conversation of %s holds provider session %s: %w", providerName, sessionID, os.ErrNotExist)
}

// WriteState atomically replaces the run's state file.
func (s *Store) WriteState(runID string, st State) error {
	return s.writeJSON(filepath.Join(s.RunDir(runID), "state.json"), st)
}

// ReadState loads the run's state file. The run's own directory and output
// file are located from the home and the run id, never from the absolute
// paths the file recorded: those name the home the run was admitted in, which
// a migration may have renamed since.
func (s *Store) ReadState(runID string) (State, error) {
	var st State
	if err := readJSON(filepath.Join(s.RunDir(runID), "state.json"), &st); err != nil {
		return st, err
	}
	st.RunDir, st.OutputPath = s.RunDir(runID), s.OutputPath(runID)
	return st, nil
}

func (s *Store) conversationsDir() string { return filepath.Join(s.Home, "conversations") }

func (s *Store) conversationPath(id string) string {
	return filepath.Join(s.conversationsDir(), id+".json")
}

func (s *Store) conversationLockPath(id string) string {
	return filepath.Join(s.conversationsDir(), id+".lock")
}

// adoptionLockPath is the lock file of adopting the provider's session. It
// ends in .lock, so ConversationIDs does not list it.
func (s *Store) adoptionLockPath(providerName, sessionID string) string {
	return filepath.Join(s.conversationsDir(), "adopt-"+providerName+"-"+sessionID+".lock")
}

// CreateConversation writes a new conversation record.
func (s *Store) CreateConversation(c Conversation) error {
	if err := os.MkdirAll(filepath.Join(s.Home, "conversations"), dirMode); err != nil {
		return err
	}
	return s.writeJSON(s.conversationPath(c.ConversationID), c)
}

// ReadConversation loads a conversation record.
func (s *Store) ReadConversation(id string) (Conversation, error) {
	var c Conversation
	err := readJSON(s.conversationPath(id), &c)
	return c, err
}

// UpdateConversation read-modify-writes a conversation record under the
// conversation lock, so it never interleaves with an admission.
func (s *Store) UpdateConversation(id string, mutate func(*Conversation)) error {
	unlock, err := s.lockConversation(id)
	if err != nil {
		return err
	}
	defer unlock()
	c, err := s.ReadConversation(id)
	if err != nil {
		return err
	}
	mutate(&c)
	return s.writeJSON(s.conversationPath(id), c)
}

func (s *Store) writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// readJSONExact is readJSON with numbers kept as json.Number when they land
// in an interface, so a large integer survives the round trip.
func readJSONExact(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// writeFileAtomic writes via a temp file in the same directory plus rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Chmod(fileMode); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
