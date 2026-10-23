package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// failingWriter is a stdout that cannot be written to.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// finishedCall runs finishComplete on a call whose provider exited zero and
// reported usage, with outputPath as the file of the final message.
func finishedCall(t *testing.T, stdout *bytes.Buffer, asJSON bool, outputPath string) (code int, stderr string, home string) {
	t.Helper()
	home = t.TempDir()
	var errBuf bytes.Buffer
	ctx := &Context{Stdout: stdout, Stderr: &errBuf, Now: time.Now, JSON: asJSON}
	res := runner.OneShotResult{SDKStatus: runner.SDKStatusOK, Usage: &provider.Usage{InputTokens: 7, OutputTokens: 3}}
	code = finishComplete(ctx, store.Open(home), harnessProvider{}, completeFlags{asJSON: asJSON}, res, outputPath)
	return code, errBuf.String(), home
}

// wroteModelCall reports whether a model call was recorded under home.
func wroteModelCall(t *testing.T, home string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		found = found || (readErr == nil && strings.Contains(string(data), `"model_call"`))
		return readErr
	})
	if err != nil {
		t.Fatalf("walking %s: %v", home, err)
	}
	return found
}

func TestCompleteAnswerFileThatCannotBeReadFailsTheCommand(t *testing.T) {
	var stdout bytes.Buffer
	asDirectory := t.TempDir() // reading a directory fails, and not with "not found"

	code, stderr, home := finishedCall(t, &stdout, true, asDirectory)

	if code != ExitInternal {
		t.Errorf("exit %d, want %d; stderr %q", code, ExitInternal, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", stdout.String(), err)
	}
	if got["sdk_status"] != "internal_error" || got["outcome"] != nil {
		t.Errorf("json = %v, want the internal_error object and no outcome", got)
	}
	if !wroteModelCall(t, home) {
		t.Error("the model call was not recorded although the provider spent the tokens")
	}
}

func TestCompleteAnswerFileThatIsMissingIsAnEmptyReply(t *testing.T) {
	var stdout bytes.Buffer
	missing := filepath.Join(t.TempDir(), "output.md")

	code, stderr, _ := finishedCall(t, &stdout, true, missing)

	if code != ExitOK {
		t.Fatalf("exit %d, want 0; stderr %q", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", stdout.String(), err)
	}
	if got["outcome"] != "empty-reply" || got["text"] != "" {
		t.Errorf("json = %v, want an empty-reply with no text", got)
	}
}

func TestCompleteAnswerThatCannotBePrintedFailsTheCommand(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output.md")
	if err := os.WriteFile(output, []byte("pong"), 0o600); err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	ctx := &Context{Stdout: failingWriter{}, Stderr: &errBuf, Now: time.Now}
	res := runner.OneShotResult{SDKStatus: runner.SDKStatusOK}

	code := finishComplete(ctx, store.Open(t.TempDir()), harnessProvider{}, completeFlags{}, res, output)

	if code != ExitInternal {
		t.Errorf("exit %d, want %d; stderr %q", code, ExitInternal, errBuf.String())
	}
}
