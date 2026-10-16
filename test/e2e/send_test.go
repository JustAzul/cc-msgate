package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const resumeThread = "00000000-0000-4000-8000-000000000003"

// startConversation runs a first turn and returns its conversation and run id.
func startConversation(t *testing.T, s *sandbox, runID string, extra ...string) (conversationID string) {
	t.Helper()
	args := append([]string{"exec", "--json", "--run-id", runID}, extra...)
	r := s.run(append(args, "first")...)
	if r.code != 0 {
		t.Fatalf("first turn: exit %d stderr %s", r.code, r.stderr)
	}
	return r.json(t)["conversation_id"].(string)
}

func conversationRecord(t *testing.T, home, id string) map[string]any {
	t.Helper()
	return readJSONFile(t, filepath.Join(home, "conversations", id+".json"))
}

func TestSendResumesTheProviderSession(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	cwd := gitRepo(t)
	conv := startConversation(t, s, "t1", "--cwd", cwd, "--scenario", "second-opinion")

	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	rec := s.recordTo()
	r := s.run("send", conv, "--run-id", "t2", "follow")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	out := filepath.Join(s.home, "runs", "t2", "output.md")
	if r.stdout != out+"\n" || readFile(t, out) != "ZEBRA-42" {
		t.Errorf("stdout=%q output=%q", r.stdout, readFile(t, out))
	}
	fake := readRecord(t, rec)
	if n := len(fake.Argv); n < 4 || !reflect.DeepEqual(fake.Argv[n-3:], []string{"resume", resumeThread, "-"}) {
		t.Errorf("argv = %v", fake.Argv)
	}
	if fake.Stdin != "follow" {
		t.Errorf("provider stdin = %q", fake.Stdin)
	}
	recs := telemetryRecords(t, s.home)
	if len(recs) != 2 {
		t.Fatalf("%d telemetry records", len(recs))
	}
	if recs[1]["command"] != "send" || recs[1]["turn"] != float64(2) || recs[1]["conversation_id"] != conv {
		t.Errorf("turn 2 record = %v", recs[1])
	}
	c := conversationRecord(t, s.home, conv)
	if !reflect.DeepEqual(c["turns"], []any{"t1", "t2"}) || c["active_run_id"] != nil {
		t.Errorf("turns=%v active=%v", c["turns"], c["active_run_id"])
	}
	if state := readJSONFile(t, filepath.Join(s.home, "runs", "t2", "state.json")); state["turn"] != float64(2) || state["conversation_id"] != conv {
		t.Errorf("state = %v", state)
	}
}

func TestSendResolvesARunIDToItsConversation(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1")
	if r := s.run("send", "t1", "--run-id", "t2", "x"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if st := readJSONFile(t, filepath.Join(s.home, "runs", "t2", "state.json")); st["conversation_id"] != conv || st["turn"] != float64(2) {
		t.Errorf("state = %v", st)
	}
}

func TestSendReadsPromptLikeExec(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1")
	rec := s.recordTo()
	s.stdin = "from stdin"
	if r := s.run("send", conv, "-"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if got := readRecord(t, rec).Stdin; got != "from stdin" {
		t.Errorf("provider stdin = %q", got)
	}
	for name, args := range map[string][]string{
		"no prompt":  {"send", conv},
		"two prompt": {"send", conv, "a", "b"},
	} {
		if r := s.run(args...); r.code != 2 {
			t.Errorf("%s: exit %d", name, r.code)
		}
	}
}

func TestSendUnknownIDIsNotFound(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	rec := s.recordTo()
	r := s.run("send", "--json", "c-nope", "x")
	if r.code != 4 || r.json(t)["sdk_status"] != "not_found" {
		t.Errorf("exit %d stdout %q", r.code, r.stdout)
	}
	if r := s.run("send", "../escape", "x"); r.code != 4 {
		t.Errorf("path-like id: exit %d", r.code)
	}
	if exists(rec) {
		t.Error("provider ran")
	}
}

func TestSendRefusesAProviderMismatch(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1")
	rec := s.recordTo()
	r := s.run("send", conv, "--provider", "other", "x")
	if r.code != 2 || !strings.Contains(r.stderr, `uses provider "codex"`) {
		t.Errorf("exit %d stderr %q", r.code, r.stderr)
	}
	if exists(rec) || exists(filepath.Join(s.home, "runs", "t2")) {
		t.Error("a mismatched send ran")
	}
	if r := s.run("send", conv, "--provider", "codex", "--run-id", "t2", "x"); r.code != 0 {
		t.Errorf("explicit matching provider: exit %d %s", r.code, r.stderr)
	}
}

func TestSendNotResumableWithoutProviderSession(t *testing.T) {
	// Turn 1 fails before the provider ever reports a thread.
	s := newSandbox(t).set("FAKECODEX_EXIT", "1")
	r := s.run("exec", "--json", "--run-id", "t1", "first")
	if r.code != 1 {
		t.Fatalf("first turn exit %d", r.code)
	}
	conv := r.json(t)["conversation_id"].(string)
	rec := s.recordTo()
	r = s.run("send", "--json", conv, "--run-id", "t2", "x")
	if r.code != 6 || r.json(t)["sdk_status"] != "not_resumable" || !strings.Contains(r.stderr, "provider session") {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	if exists(rec) || exists(filepath.Join(s.home, "runs", "t2")) {
		t.Error("a non-resumable send ran")
	}
	if c := conversationRecord(t, s.home, conv); !reflect.DeepEqual(c["turns"], []any{"t1"}) || c["active_run_id"] != nil {
		t.Errorf("conversation changed: %v", c)
	}
}

func TestSendRunsInTheConversationsDirectory(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	a := symlinkedGitRepo(t)
	conv := startConversation(t, s, "t1", "--cwd", a)
	rec := s.recordTo()
	// The sandbox runs msgate from an unrelated temporary directory.
	if r := s.run("send", conv, "--run-id", "t2", "x"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	fake := readRecord(t, rec)
	if !samePath(t, fake.Cwd, a) {
		t.Errorf("provider ran in %q, want %q", fake.Cwd, a)
	}
	var dashC string
	for i, arg := range fake.Argv {
		if arg == "-C" {
			dashC = fake.Argv[i+1]
		}
	}
	if dashC != a {
		t.Errorf("-C = %q, want %q", dashC, a)
	}
}

func TestSendWithMissingCwdIsRefusedUnlessCwdIsGiven(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	conv := startConversation(t, s, "t1", "--cwd", gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	rec := s.recordTo()
	r := s.run("send", conv, "--run-id", "t2", "x")
	if r.code != 2 || !strings.Contains(r.stderr, gone) {
		t.Fatalf("exit %d stderr %q", r.code, r.stderr)
	}
	if exists(rec) || exists(filepath.Join(s.home, "runs", "t2")) {
		t.Error("a send into a missing directory ran")
	}

	other := gitRepo(t)
	if r := s.run("send", conv, "--cwd", other, "--run-id", "t3", "x"); r.code != 0 {
		t.Fatalf("with --cwd: exit %d stderr %s", r.code, r.stderr)
	}
	if !samePath(t, readRecord(t, rec).Cwd, other) {
		t.Errorf("provider cwd = %q, want %q", readRecord(t, rec).Cwd, other)
	}
	if got := conversationRecord(t, s.home, conv)["cwd"]; got != gone {
		t.Errorf("--cwd changed the stored cwd to %v", got)
	}
}

func TestSendPerTurnFlagsDoNotChangeTheDefaults(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	cwd := gitRepo(t)
	conv := startConversation(t, s, "t1", "--cwd", cwd, "--scenario", "second-opinion")
	before := conversationRecord(t, s.home, conv)["defaults"]
	rec := s.recordTo()

	argvOf := func() string { return strings.Join(readRecord(t, rec).Argv, " ") }
	if r := s.run("send", conv, "--run-id", "t2", "--effort", "low", "x"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if got := argvOf(); !strings.Contains(got, "model_reasoning_effort=low") || !strings.Contains(got, "-m gpt-6.1-sol") || !strings.Contains(got, "-s read-only") {
		t.Errorf("turn 2 argv = %s", got)
	}
	if r := s.run("send", conv, "--run-id", "t3", "x"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if got := argvOf(); !strings.Contains(got, "model_reasoning_effort=high") {
		t.Errorf("turn 3 argv = %s", got)
	}
	if after := conversationRecord(t, s.home, conv)["defaults"]; !reflect.DeepEqual(before, after) {
		t.Errorf("defaults changed: %v -> %v", before, after)
	}
	recs := telemetryRecords(t, s.home)
	for i, want := range []struct{ effort, source, scenario string }{
		{"high", "profile", "second-opinion"}, {"low", "flag", "second-opinion"}, {"high", "profile", "second-opinion"},
	} {
		if recs[i]["effort"] != want.effort || recs[i]["effort_source"] != want.source || recs[i]["scenario"] != want.scenario {
			t.Errorf("turn %d record: effort=%v source=%v scenario=%v", i+1, recs[i]["effort"], recs[i]["effort_source"], recs[i]["scenario"])
		}
	}
}

func TestSendDryRunPlansWithoutAdmitting(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1")
	before := conversationRecord(t, s.home, conv)
	r := s.run("send", conv, "--dry-run", "--run-id", "t2", "x")
	plan := r.json(t)
	argv, _ := plan["argv"].([]any)
	if r.code != 0 || plan["command"] != "send" || len(argv) < 3 || !reflect.DeepEqual(argv[len(argv)-3:], []any{"resume", resumeThread, "-"}) {
		t.Fatalf("exit %d plan %v", r.code, plan)
	}
	if exists(filepath.Join(s.home, "runs", "t2")) || !reflect.DeepEqual(before, conversationRecord(t, s.home, conv)) {
		t.Error("dry-run changed the store")
	}
	if len(telemetryRecords(t, s.home)) != 1 {
		t.Error("dry-run wrote telemetry")
	}
}

// waitForActive polls until the conversation shows an active run.
func waitForActive(t *testing.T, home, conv string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if id, ok := conversationRecord(t, home, conv)["active_run_id"].(string); ok {
			return id
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no run became active")
	return ""
}

// siblingSandbox shares home and environment with s, so two commands can run
// at once from separate goroutines.
func siblingSandbox(t *testing.T, s *sandbox) *sandbox {
	t.Helper()
	o := newSandbox(t)
	o.home = s.home
	for k, v := range s.env {
		o.env[k] = v
	}
	return o
}

func TestSendWhileATurnIsActiveIsBusy(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1")
	s.set("FAKECODEX_SLEEP_MS", "2000")
	slow := siblingSandbox(t, s)
	done := make(chan result, 1)
	go func() { done <- slow.run("send", conv, "--run-id", "slow2", "x") }()
	if active := waitForActive(t, s.home, conv); active != "slow2" {
		t.Fatalf("active run = %q", active)
	}

	r := s.run("send", "--json", conv, "--run-id", "refused3", "y")
	if r.code != 3 || r.json(t)["sdk_status"] != "busy" || !strings.Contains(r.stderr, "slow2") {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	if exists(filepath.Join(s.home, "runs", "refused3")) {
		t.Error("a refused send left a run directory")
	}
	if first := <-done; first.code != 0 {
		t.Fatalf("first send exit %d %s", first.code, first.stderr)
	}
	if c := conversationRecord(t, s.home, conv); !reflect.DeepEqual(c["turns"], []any{"t1", "slow2"}) || c["active_run_id"] != nil {
		t.Errorf("conversation = %v", c)
	}
}

func TestSimultaneousSendsAdmitExactlyOne(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1")
	s.set("FAKECODEX_SLEEP_MS", "1500")
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]result, 2)
	for i, id := range []string{"sa", "sb"} {
		sb := siblingSandbox(t, s)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = sb.run("send", conv, "--run-id", id, "x")
		}()
	}
	close(start)
	wg.Wait()

	codes := []int{results[0].code, results[1].code}
	if !(codes[0] == 0 && codes[1] == 3) && !(codes[0] == 3 && codes[1] == 0) {
		t.Fatalf("exit codes %v, want one 0 and one 3 (stderr %q / %q)", codes, results[0].stderr, results[1].stderr)
	}
	winner := "sa"
	if codes[1] == 0 {
		winner = "sb"
	}
	c := conversationRecord(t, s.home, conv)
	if !reflect.DeepEqual(c["turns"], []any{"t1", winner}) || c["active_run_id"] != nil {
		t.Errorf("conversation = %v", c)
	}
	if st := readJSONFile(t, filepath.Join(s.home, "runs", winner, "state.json")); st["turn"] != float64(2) {
		t.Errorf("winner state = %v", st)
	}
}
