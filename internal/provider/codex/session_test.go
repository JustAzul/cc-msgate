package codex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

func readSession(t *testing.T, home, id string) (provider.Session, bool) {
	t.Helper()
	s, ok, err := codex.New().(provider.SessionReader).ReadSession([]string{"CODEX_HOME=" + home}, id)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	return s, ok
}

func TestReadSessionIsTheLastTurnContextAsItIs(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home,
		`{"type":"session_meta","payload":{"cwd":"/meta"}}`,
		`{"type":"turn_context","payload":{"cwd":"/a","model":"m1","effort":"high"}}`,
		`{"type":"turn_context","payload":{"cwd":"/b","model":"","effort":"low"}}`,
	)

	s, ok := readSession(t, home, sessionID)

	if !ok || s.Cwd != "/b" || s.Model != "" || s.Effort != "low" {
		t.Errorf("got %+v ok=%v, want cwd /b, no model, effort low", s, ok)
	}
}

func TestReadSessionWithoutATurnTakesTheSessionMetaDirectory(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, `{"type":"session_meta","payload":{"cwd":"/meta"}}`)

	s, ok := readSession(t, home, sessionID)

	if !ok || s.Cwd != "/meta" || s.Model != "" || s.Effort != "" {
		t.Errorf("got %+v ok=%v, want cwd /meta and nothing else", s, ok)
	}
}

func TestReadSessionIsNotASessionOfCodex(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, `{"type":"session_meta","payload":{"cwd":"/meta"}}`)

	cases := map[string]string{
		"not a uuid":              "not-a-uuid",
		"a uuid with no file":     "00000000-0000-4000-8000-0000000000ff",
		"a glob in the id":        "00000000-0000-4000-8000-00000000000*",
		"a run id":                "r-20261022T000000Z-deadbeef",
		"the uuid and a trailing": sessionID + "x",
	}
	for name, id := range cases {
		if s, ok := readSession(t, home, id); ok {
			t.Errorf("%s: ok with %+v, want not a session", name, s)
		}
	}
}

const parentID = "00000000-0000-4000-8000-0000000000a1"

func sessionMetaWithSource(source string) string {
	return `{"type":"session_meta","payload":{"cwd":"/meta","source":` + source + `}}`
}

func TestReadSessionOfASpawnedChildNamesItsParent(t *testing.T) {
	cases := map[string]struct {
		agentPath string
		address   string
	}{
		"one segment below the root":  {"/root/child", "child"},
		"two segments below the root": {"/root/a/b", "b"},
	}
	for name, c := range cases {
		home := t.TempDir()
		writeRollout(t, home, sessionMetaWithSource(
			`{"subagent":{"thread_spawn":{"parent_thread_id":"`+parentID+`","depth":1,"agent_path":"`+c.agentPath+`"}}}`))

		s, ok := readSession(t, home, sessionID)

		want := &provider.Parent{SessionID: parentID, Address: c.address}
		if !ok || s.Parent == nil || *s.Parent != *want {
			t.Errorf("%s: got %+v ok=%v, want parent %+v", name, s.Parent, ok, want)
		}
	}
}

func TestReadSessionWithoutASpawnedParentHasNone(t *testing.T) {
	cases := map[string]string{
		"exec":                   `"exec"`,
		"cli":                    `"cli"`,
		"vscode":                 `"vscode"`,
		"a review thread":        `{"subagent":"review"}`,
		"an unknown object":      `{"other":{"thread_spawn":{"parent_thread_id":"` + parentID + `","agent_path":"/root/child"}}}`,
		"an empty agent path":    `{"subagent":{"thread_spawn":{"parent_thread_id":"` + parentID + `","agent_path":""}}}`,
		"no agent path":          `{"subagent":{"thread_spawn":{"parent_thread_id":"` + parentID + `"}}}`,
		"no parent thread id":    `{"subagent":{"thread_spawn":{"agent_path":"/root/child"}}}`,
		"an empty parent thread": `{"subagent":{"thread_spawn":{"parent_thread_id":"","agent_path":"/root/child"}}}`,
	}
	for name, source := range cases {
		home := t.TempDir()
		writeRollout(t, home, sessionMetaWithSource(source))

		s, ok := readSession(t, home, sessionID)

		if !ok || s.Parent != nil || s.Cwd != "/meta" {
			t.Errorf("%s: got %+v ok=%v, want the session with cwd /meta and no parent", name, s, ok)
		}
	}
}

func TestReadSessionWithoutASourceHasNoParent(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, `{"type":"session_meta","payload":{"cwd":"/meta"}}`)

	s, ok := readSession(t, home, sessionID)

	if !ok || s.Parent != nil {
		t.Errorf("got %+v ok=%v, want the session with no parent", s, ok)
	}
}

func TestReadSessionOfAnUnreadableRolloutIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	home := t.TempDir()
	writeRollout(t, home, `{"type":"session_meta","payload":{"cwd":"/meta"}}`)
	rollouts, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatalf("rollouts %v, err %v, want exactly one", rollouts, err)
	}
	if err := os.Chmod(rollouts[0], 0o000); err != nil {
		t.Fatal(err)
	}

	s, ok, err := codex.New().(provider.SessionReader).ReadSession([]string{"CODEX_HOME=" + home}, sessionID)

	if err == nil || ok {
		t.Errorf("got %+v ok=%v err=%v, want an error and not ok", s, ok, err)
	}
}
