package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// rewriteConversation applies edit to the stored conversation record.
func rewriteConversation(t *testing.T, home, id string, edit func(rec map[string]any)) {
	t.Helper()
	rec := conversationRecord(t, home, id)
	edit(rec)
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "conversations", id+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// lastRunRecord is the newest run record in the telemetry.
func lastRunRecord(t *testing.T, home string) map[string]any {
	t.Helper()
	recs := telemetryRecords(t, home)
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i]["kind"] == "run" {
			return recs[i]
		}
	}
	t.Fatal("no run record in the telemetry")
	return nil
}

func TestConversationKeepsTheSourcesOfItsDefaults(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "cs1", "--purpose", "second-opinion", "--model", "m1", "--effort", "high", "--settings-source", "profile")
	defaults, _ := conversationRecord(t, s.home, conv)["defaults"].(map[string]any)
	checkFields(t, "defaults", defaults, map[string]any{
		"model": "m1", "model_source": "profile",
		"effort": "high", "effort_source": "profile",
		"sandbox": "read-only", "sandbox_source": "default",
	})
}

func TestSendKeepsTheSourcesWhenTheFirstTurnIsGone(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "cs2", "--purpose", "second-opinion", "--model", "m1", "--effort", "high", "--settings-source", "profile")
	if err := os.RemoveAll(filepath.Join(s.home, "runs", "cs2")); err != nil {
		t.Fatal(err)
	}
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	if r := s.run("send", conv, "--run-id", "cs2b", "again"); r.code != 0 {
		t.Fatalf("send exit %d, stderr %s", r.code, r.stderr)
	}
	checkFields(t, "turn 2 record", lastRunRecord(t, s.home), map[string]any{
		"model": "m1", "model_source": "profile", "effort": "high", "effort_source": "profile",
		"sandbox": "read-only",
	})
	checkFields(t, "turn 2 request", readJSONFile(t, filepath.Join(s.home, "runs", "cs2b", "request.json")), map[string]any{
		"sandbox": "read-only", "sandbox_source": "default",
	})
}

// A conversation written before the sources were recorded falls back to the
// first turn's request, and to the profile when that is gone too.
func TestSendOnALegacyConversationFallsBack(t *testing.T) {
	cases := []struct {
		name        string
		dropRequest bool
		wantSource  string
	}{
		{"first turn request still present", false, "flag"},
		{"first turn request gone", true, "profile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
			conv := startConversation(t, s, "lg1", "--purpose", "second-opinion", "--model", "m1", "--effort", "high")
			rewriteConversation(t, s.home, conv, func(rec map[string]any) {
				d := rec["defaults"].(map[string]any)
				delete(d, "model_source")
				delete(d, "effort_source")
				delete(d, "sandbox_source")
			})
			if c.dropRequest {
				if err := os.Remove(filepath.Join(s.home, "runs", "lg1", "request.json")); err != nil {
					t.Fatal(err)
				}
			}
			s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
			if r := s.run("send", conv, "--run-id", "lg2", "again"); r.code != 0 {
				t.Fatalf("send exit %d, stderr %s", r.code, r.stderr)
			}
			checkFields(t, "turn 2 record", lastRunRecord(t, s.home), map[string]any{
				"model": "m1", "model_source": c.wantSource, "effort_source": c.wantSource,
			})
		})
	}
}
