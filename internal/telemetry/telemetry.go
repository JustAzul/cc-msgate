// Package telemetry owns the append-only run records.
package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// Version is the record schema version.
const Version = 1

// LockWait is how long an append waits for the telemetry lock.
const LockWait = 5 * time.Second

const (
	dirMode  = 0o700
	fileMode = 0o600
	lockPoll = 20 * time.Millisecond
)

// ErrLockTimeout is returned when the telemetry lock could not be taken in time.
var ErrLockTimeout = errors.New("telemetry lock not acquired in time")

// Record is a run record. Field order is the on-disk order.
type Record struct {
	V                 int             `json:"v"`
	Kind              string          `json:"kind"`
	RunID             string          `json:"run_id"`
	TS                string          `json:"ts"`
	Provider          string          `json:"provider"`
	ProviderVersion   *string         `json:"provider_version"`
	Command           string          `json:"command"`
	Purpose           string          `json:"purpose"`
	Model             *string         `json:"model"`
	ModelSource       string          `json:"model_source"`
	Effort            *string         `json:"effort"`
	EffortSource      string          `json:"effort_source"`
	ModelUsed         *string         `json:"model_used"`
	EffortUsed        *string         `json:"effort_used"`
	Sandbox           *string         `json:"sandbox"`
	Source            string          `json:"source"`
	SessionID         *string         `json:"session_id"`
	ConversationID    string          `json:"conversation_id"`
	Turn              int             `json:"turn"`
	Cwd               string          `json:"cwd"`
	Background        bool            `json:"background"`
	ExitCode          int             `json:"exit_code"`
	Outcome           string          `json:"outcome"`
	DurationMS        int64           `json:"duration_ms"`
	TimeoutS          *int            `json:"timeout_s"`
	OutputFile        string          `json:"output_file"`
	OutputBytes       int64           `json:"output_bytes"`
	ErrorExcerpt      *string         `json:"error_excerpt"`
	Usage             *provider.Usage `json:"usage"`
	ProviderSessionID *string         `json:"provider_session_id"`
	Attrs             map[string]any  `json:"attrs"`
}

// Options tune an append.
type Options struct {
	// LockWait overrides the default lock wait; zero means LockWait.
	LockWait time.Duration
}

// Append writes rec to the month file of at's UTC month under home. It holds
// an exclusive lock on telemetry/.lock for the write, waits for it at most
// LockWait, and emits the record, preceded by a newline when the file does not
// already end in one, with a single write call.
func Append(home string, rec Record, at time.Time, opt Options) error {
	if rec.Attrs == nil {
		rec.Attrs = map[string]any{}
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encoding the record: %w", err)
	}
	return AppendLine(home, line, at, opt)
}

// endsMidLine reports whether the file is non-empty and its last byte is not a
// newline (a torn tail left by an interrupted write).
func endsMidLine(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// lockExclusive takes an exclusive flock, polling until wait has passed.
func lockExclusive(f *os.File, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			return err
		}
		if !time.Now().Before(deadline) {
			return ErrLockTimeout
		}
		time.Sleep(lockPoll)
	}
}
