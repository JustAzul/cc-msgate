package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// addTokenCount appends to the rollout of session id a token_count stamped
// long before any run, whose thread totals are the given counts.
func addTokenCount(t *testing.T, codexHome, id string, input, cached, output int) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(codexHome, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("rollout of %s: %v %v", id, files, err)
	}
	line := fmt.Sprintf(`{"timestamp":"2020-01-01T00:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":0,"output_tokens":%d,"reasoning_output_tokens":0}}}}`+"\n",
		input, cached, output)
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func recordedUsage(t *testing.T, home string) map[string]any {
	t.Helper()
	usage, _ := lastRunRecord(t, home)["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("the run record has no usage")
	}
	return usage
}

func checkUsage(t *testing.T, usage map[string]any, input, cached, output int) {
	t.Helper()
	checkFields(t, "usage", usage, map[string]any{
		"input_tokens": float64(input), "cached_input_tokens": float64(cached), "cache_write_input_tokens": float64(0),
		"output_tokens": float64(output), "reasoning_output_tokens": float64(0),
	})
}

// The resume-turn1 fixture reports 18584 input, 11008 cached and 5 output
// tokens; the exec-ok fixture 18573 input and 5 output.
func TestSendRecordsTheTurnsOwnUsageNotTheThreadTotal(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	addTokenCount(t, codexHome, resumeThread, 18000, 11000, 3)
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)

	if r := s.run("send", resumeThread, "--json", "--run-id", "u1", "carry on"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	checkUsage(t, recordedUsage(t, s.home), 584, 8, 2)
	totals := s.run("stats", "--json").json(t)["usage_totals"].(map[string]any)
	if totals["input_tokens"] != float64(584) || totals["cached_input_tokens"] != float64(8) || totals["output_tokens"] != float64(2) {
		t.Errorf("stats usage_totals = %v, want the turn's own tokens", totals)
	}
}

func TestSendFloorsEachCountOfTheTurnAtZero(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	addTokenCount(t, codexHome, resumeThread, 20000, 1000, 3)
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)

	if r := s.run("send", resumeThread, "--json", "--run-id", "u1", "carry on"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	checkUsage(t, recordedUsage(t, s.home), 0, 10008, 2)
}

func TestSendRecordsUsageAsReportedWithoutAnEarlierTokenCount(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)

	r := s.run("send", resumeThread, "--json", "--run-id", "u1", "carry on")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	checkUsage(t, recordedUsage(t, s.home), 18584, 11008, 5)
	if r.stderr != "" {
		t.Errorf("stderr = %q, want none", r.stderr)
	}
}

// execThread is the thread the exec-ok fixture reports.
const execThread = "00000000-0000-4000-8000-000000000001"

func TestExecRecordsUsageAsReportedWhateverTheRolloutHolds(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, execThread, gitRepo(t), "outside-model", "medium")
	addTokenCount(t, codexHome, execThread, 18000, 11000, 3)
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome)

	startConversation(t, s, "u1")

	usage := recordedUsage(t, s.home)
	if usage["input_tokens"] != float64(18573) {
		t.Errorf("exec usage = %v, want input 18573 as reported", usage)
	}
}
