package codex_test

import (
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
