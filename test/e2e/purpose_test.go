package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// secondOpinionFlags are the settings a caller that keeps its own table of
// per-purpose settings passes: the engine applies none by itself.
func secondOpinionFlags(extra ...string) []string {
	return append([]string{"--purpose", "second-opinion", "--model", "gpt-6.1-sol", "--effort", "high",
		"--sandbox", "read-only", "--settings-source", "profile"}, extra...)
}

func dryArgv(t *testing.T, r result) []any {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	return r.json(t)["argv"].([]any)
}

func TestPurposeFlagIsRecordedAndDefaultsToAdhoc(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	if r := s.run("exec", "--purpose", "second-opinion", "--run-id", "pu1", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if r := s.run("exec", "--run-id", "pu2", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}

	for id, want := range map[string]string{"pu1": "second-opinion", "pu2": "adhoc"} {
		req := readJSONFile(t, filepath.Join(s.home, "runs", id, "request.json"))
		checkFields(t, id+" request", req, map[string]any{"purpose": want})
		if _, ok := req["scenario"]; ok {
			t.Errorf("%s request.json still has a scenario key", id)
		}
	}
	recs := telemetryRecords(t, s.home)
	if recs[0]["purpose"] != "second-opinion" || recs[1]["purpose"] != "adhoc" {
		t.Errorf("telemetry purposes = %v, %v", recs[0]["purpose"], recs[1]["purpose"])
	}
	if _, ok := recs[0]["scenario"]; ok {
		t.Error("the telemetry record still has a scenario key")
	}
}

func TestPurposeIsReportedByStatusAndTheConversationDefaults(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	if r := s.run("exec", "--purpose", "cross-check", "--run-id", "pu3", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}

	j := s.run("status", "--json", "pu3").json(t)
	checkFields(t, "status", j, map[string]any{"purpose": "cross-check"})
	if _, ok := j["scenario"]; ok {
		t.Error("status --json still has a scenario key")
	}
	conv := conversationRecord(t, s.home, j["conversation_id"].(string))
	checkFields(t, "defaults", conv["defaults"].(map[string]any), map[string]any{"purpose": "cross-check"})
}

func TestScenarioFlagIsUnknown(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	for _, args := range [][]string{
		{"exec", "--scenario", "x", "-"},
		{"review", "--uncommitted", "--scenario", "x"},
		{"send", "c1", "--scenario", "x", "-"},
		{"eta", "--scenario", "x"},
	} {
		if r := s.run(args...); r.code != 2 {
			t.Errorf("%v: exit %d, want 2 (stderr %s)", args, r.code, r.stderr)
		}
	}
}

func TestPurposeReadFromOldRecords(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	repo := gitRepo(t)
	dir, err := json.Marshal(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeMonthFile(t, s.home, monthOf(ts),
		recLine("old1", ts, `"scenario":"code-review","cwd":`+string(dir)+`,"duration_ms":60000,"outcome":"ok"`),
		recLine("old2", ts, `"scenario":"code-review","purpose":"kept","source":"skill"`),
	)

	runs := jsonLines(t, s.run("runs", "--all"))
	if len(runs) != 2 {
		t.Fatalf("runs = %v", runs)
	}
	checkFields(t, "old1", runs[0], map[string]any{"purpose": "code-review"})
	checkFields(t, "old2", runs[1], map[string]any{"purpose": "kept"})
	for _, run := range runs {
		if _, ok := run["scenario"]; ok {
			t.Errorf("%v still has a scenario key", run["run_id"])
		}
	}
	j := s.run("eta", "--purpose", "code-review", "--cwd", repo, "--json").json(t)
	if j["samples"] != float64(1) || j["eta_ms"] != float64(60000) || j["purpose"] != "code-review" {
		t.Errorf("eta = %v, want the old record counted under purpose code-review", j)
	}
}

func TestPurposeIsRequiredOnETA(t *testing.T) {
	s := newSandbox(t)
	if r := s.run("eta", "--json"); r.code != 2 {
		t.Errorf("exit %d, want 2", r.code)
	}
}

func TestStatusReadsPurposeOfAnOldRequest(t *testing.T) {
	s := newSandbox(t)
	seedRunFiles(t, s, "pu-old", map[string]any{}, map[string]any{}, []byte("out"))
	oldRequest := `{"command":"exec","provider":"codex","scenario":"cross-check","source":"cli","session_id":""}`
	if err := os.WriteFile(filepath.Join(s.home, "runs", "pu-old", "request.json"), []byte(oldRequest), 0o600); err != nil {
		t.Fatal(err)
	}

	j := s.run("status", "--json", "pu-old").json(t)
	checkFields(t, "status", j, map[string]any{"purpose": "cross-check"})
	if _, ok := j["scenario"]; ok {
		t.Error("status --json still has a scenario key")
	}
}

func TestNoProfileFromThePurpose(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	repo := gitRepo(t)
	argv := dryArgv(t, s.run("exec", "--purpose", "second-opinion", "--dry-run", "--run-id", "np1", "--cwd", repo, "q"))
	out := filepath.Join(s.home, "runs", "np1", "output.md")
	want := []any{"codex", "exec", "-C", repo, "-s", "read-only", "--json", "-o", out, "-"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv\n got %v\nwant %v", argv, want)
	}

	if r := s.run("exec", "--purpose", "second-opinion", "--run-id", "np2", "--cwd", repo, "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	req := readJSONFile(t, filepath.Join(s.home, "runs", "np2", "request.json"))
	checkFields(t, "request", req, map[string]any{
		"model": nil, "model_source": "provider-default",
		"effort": nil, "effort_source": "provider-default",
		"sandbox": "read-only", "sandbox_source": "default",
	})
}

func TestSettingsSource(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	repo := gitRepo(t)
	flags := []string{"--purpose", "delegation", "--model", "gpt-6.1-sol", "--effort", "medium", "--sandbox", "workspace-write", "--cwd", repo}

	if r := s.run(append([]string{"exec", "--run-id", "ss1", "--settings-source", "profile"}, append(flags, "q")...)...); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "profile source", readJSONFile(t, filepath.Join(s.home, "runs", "ss1", "request.json")), map[string]any{
		"model": "gpt-6.1-sol", "model_source": "profile", "effort": "medium", "effort_source": "profile",
		"sandbox": "workspace-write", "sandbox_source": "profile",
	})

	if r := s.run(append([]string{"exec", "--run-id", "ss2"}, append(flags, "q")...)...); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "default source", readJSONFile(t, filepath.Join(s.home, "runs", "ss2", "request.json")), map[string]any{
		"model_source": "flag", "effort_source": "flag", "sandbox_source": "flag",
	})

	if r := s.run("exec", "--run-id", "ss3", "--settings-source", "profile", "--model", "m1", "--cwd", repo, "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "partly passed", readJSONFile(t, filepath.Join(s.home, "runs", "ss3", "request.json")), map[string]any{
		"model_source": "profile", "effort_source": "provider-default", "sandbox": "read-only", "sandbox_source": "default",
	})
}

func TestSettingsSourceRejectsOtherValues(t *testing.T) {
	s := newSandbox(t)
	if r := s.run("exec", "--settings-source", "plugin", "--dry-run", "q"); r.code != 2 {
		t.Errorf("exit %d, want 2 (stderr %s)", r.code, r.stderr)
	}
}

func TestSendKeepsTheSourcesOfTheConversation(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "sk1", "--purpose", "delegation", "--model", "m1", "--effort", "low",
		"--sandbox", "workspace-write", "--settings-source", "profile")
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))

	if r := s.run("send", conv, "--run-id", "sk2", "--effort", "high", "again"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "turn 2", lastRunRecord(t, s.home), map[string]any{
		"purpose": "delegation", "model": "m1", "model_source": "profile",
		"effort": "high", "effort_source": "flag", "sandbox": "workspace-write",
	})

	if r := s.run("send", conv, "--run-id", "sk3", "--settings-source", "profile", "--effort", "xhigh", "again"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "turn 3", lastRunRecord(t, s.home), map[string]any{"effort": "xhigh", "effort_source": "profile"})
}

func TestOldSandboxNull(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "on1")
	rewriteConversation(t, s.home, conv, func(rec map[string]any) {
		d := rec["defaults"].(map[string]any)
		d["sandbox"] = nil
		delete(d, "sandbox_source")
	})
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	rec := s.recordTo()

	if r := s.run("send", conv, "--run-id", "on2", "again"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "turn 2", lastRunRecord(t, s.home), map[string]any{"sandbox": "read-only"})
	checkFields(t, "turn 2 request", readJSONFile(t, filepath.Join(s.home, "runs", "on2", "request.json")), map[string]any{
		"sandbox": "read-only", "sandbox_source": "default",
	})
	if argv := readRecord(t, rec).Argv; !slices.Contains(argv, "read-only") {
		t.Errorf("argv = %v, want the read-only sandbox", argv)
	}
}

func TestOldConversationDefaults(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "oc1")
	rewriteConversation(t, s.home, conv, func(rec map[string]any) {
		d := rec["defaults"].(map[string]any)
		delete(d, "purpose")
		d["scenario"] = "cross-check"
	})
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))

	if r := s.run("send", conv, "--run-id", "oc2", "again"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	checkFields(t, "turn 2", lastRunRecord(t, s.home), map[string]any{"purpose": "cross-check"})
}
