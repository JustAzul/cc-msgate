package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runWithPrompt runs one foreground exec whose prompt is stdin and returns
// the sandbox, so a test can read the run back.
func runWithPrompt(t *testing.T, runID, prompt string) *sandbox {
	t.Helper()
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	s.stdin = prompt
	if r := s.run("exec", "--run-id", runID, "-"); r.code != 0 {
		t.Fatalf("exec exit %d %s", r.code, r.stderr)
	}
	s.stdin = ""
	return s
}

func TestPromptPrintsTheStoredBytesExactly(t *testing.T) {
	const prompt = "first line\n\nsegundo parágrafo com acentuação e 日本語 🙂\nno trailing newline"
	s := runWithPrompt(t, "pr1", prompt)

	r := s.run("prompt", "pr1")

	if r.code != 0 || r.stdout != prompt || r.stderr != "" {
		t.Errorf("exit %d stdout %q stderr %q, want exit 0 and stdout %q", r.code, r.stdout, r.stderr, prompt)
	}
}

func TestPromptOfAKnownRunWithoutAPromptFileExitsInternal(t *testing.T) {
	s := runWithPrompt(t, "pr2", "q")
	if err := os.Remove(filepath.Join(s.home, "runs", "pr2", "prompt.md")); err != nil {
		t.Fatal(err)
	}

	r := s.run("prompt", "pr2")

	if r.code != 70 || r.stdout != "" || !strings.Contains(r.stderr, "reading the prompt of run pr2") {
		t.Errorf("exit %d stdout %q stderr %q, want exit 70, empty stdout and the reading error", r.code, r.stdout, r.stderr)
	}
}

func TestPromptNeedsExactlyOneRunID(t *testing.T) {
	s := newSandbox(t)
	for _, args := range [][]string{{"prompt"}, {"prompt", "a", "b"}} {
		r := s.run(args...)

		if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "usage: msgate prompt <run_id>") {
			t.Errorf("%v: exit %d stdout %q stderr %q, want exit 2 and the usage line", args, r.code, r.stdout, r.stderr)
		}
	}
}

func TestPromptCommandIsListedInHelp(t *testing.T) {
	r := newSandbox(t).run("help")

	if r.code != 0 || !strings.Contains(r.stdout, "\n  prompt ") {
		t.Errorf("exit %d, help does not list prompt:\n%s", r.code, r.stdout)
	}
}

func TestPromptOfAnUnknownRunExitsNotFound(t *testing.T) {
	s := newSandbox(t)

	r := s.run("prompt", "r-missing")

	oneLine := r.stderr != "" && !strings.Contains(strings.TrimSuffix(r.stderr, "\n"), "\n")
	if r.code != 4 || r.stdout != "" || !oneLine {
		t.Errorf("exit %d stdout %q stderr %q, want exit 4, empty stdout and one stderr line", r.code, r.stdout, r.stderr)
	}
}
