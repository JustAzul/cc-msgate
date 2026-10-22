package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeRollout lays out the session file of a codex session some other client
// started: a session_meta line, then one turn_context.
func writeRollout(t *testing.T, codexHome, id, cwd, model, effort string) {
	t.Helper()
	turn := fmt.Sprintf(`{"type":"turn_context","payload":{"cwd":%q,"model":%q,"effort":%q,"sandbox_policy":{"type":"workspace-write"},"approval_policy":"on-request"}}`, cwd, model, effort)
	writeRolloutLines(t, codexHome, id, rolloutMeta(id, cwd), turn)
}

// writeMetaOnlyRollout lays out the session file of a session that never began
// a turn: a session_meta line alone.
func writeMetaOnlyRollout(t *testing.T, codexHome, id, cwd string) {
	t.Helper()
	writeRolloutLines(t, codexHome, id, rolloutMeta(id, cwd))
}

func rolloutMeta(id, cwd string) string {
	return fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q,"originator":"codex_exec","source":"exec","thread_source":"user"}}`, id, id, cwd)
}

func writeRolloutLines(t *testing.T, codexHome, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "10", "22")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-10-22T00-00-00-"+id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// checkResumeArgv checks the fake codex was run as exec -C <cwd> <flags>
// resume <id> -, each part reported on its own.
func checkResumeArgv(t *testing.T, argv []string, cwd string, wantFlags []string, id string) {
	t.Helper()
	tail := []string{"resume", id, "-"}
	if len(argv) < 4+len(wantFlags)+len(tail) {
		t.Errorf("argv too short: %v", argv)
		return
	}
	if argv[1] != "exec" {
		t.Errorf("argv[1] = %q, want exec", argv[1])
	}
	if argv[2] != "-C" || !samePath(t, argv[3], cwd) {
		t.Errorf("argv[2:4] = %v, want -C %s", argv[2:4], cwd)
	}
	if got := argv[4 : 4+len(wantFlags)]; !reflect.DeepEqual(got, wantFlags) {
		t.Errorf("flags = %v, want %v", got, wantFlags)
	}
	if got := argv[len(argv)-len(tail):]; !reflect.DeepEqual(got, tail) {
		t.Errorf("argv tail = %v, want %v", got, tail)
	}
}

func TestSendAdoptsASessionNoConversationHolds(t *testing.T) {
	codexHome := t.TempDir()
	cwd := gitRepo(t)
	writeRollout(t, codexHome, resumeThread, cwd, "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	rec := s.recordTo()

	r := s.run("send", resumeThread, "--json", "--run-id", "o1", "carry on")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	fake := readRecord(t, rec)
	wantFlags := []string{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}
	checkResumeArgv(t, fake.Argv, cwd, wantFlags, resumeThread)
	if fake.Stdin != "carry on" {
		t.Errorf("provider stdin = %q", fake.Stdin)
	}

	conv := r.json(t)["conversation_id"].(string)
	c := conversationRecord(t, s.home, conv)
	checkFields(t, "conversation", c, map[string]any{"provider": "codex", "provider_session_id": resumeThread})
	if got, _ := c["cwd"].(string); !samePath(t, got, cwd) {
		t.Errorf("conversation cwd = %q, want %q", got, cwd)
	}
	defaults, _ := c["defaults"].(map[string]any)
	checkFields(t, "defaults", defaults, map[string]any{
		"model": "outside-model", "model_source": "provider-session",
		"effort": "medium", "effort_source": "provider-session",
		"sandbox": "read-only", "sandbox_source": "default",
		"purpose": "adhoc",
	})

	checkFields(t, "run record", lastRunRecord(t, s.home), map[string]any{
		"command": "send", "turn": float64(1),
		"model": "outside-model", "model_source": "provider-session",
		"effort": "medium", "effort_source": "provider-session",
	})
}

// firstOutsideTurn adopts the outside session id with run o1 and returns the
// conversation that holds it.
func firstOutsideTurn(t *testing.T, s *sandbox, id string) string {
	t.Helper()
	r := s.run("send", id, "--json", "--run-id", "o1", "first")
	if r.code != 0 {
		t.Fatalf("adopting %s: exit %d stderr %s", id, r.code, r.stderr)
	}
	return r.json(t)["conversation_id"].(string)
}

func conversationCount(t *testing.T, home string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(home, "conversations", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

// checkContinued checks run is turn n of conversation conv and that conv is
// the only conversation.
func checkContinued(t *testing.T, s *sandbox, conv, run string, n float64) {
	t.Helper()
	if got := conversationCount(t, s.home); got != 1 {
		t.Errorf("%d conversations, want 1", got)
	}
	st := stateOf(t, s, run)
	if st["conversation_id"] != conv || st["turn"] != n {
		t.Errorf("run %s state = conversation %v turn %v, want %s turn %v", run, st["conversation_id"], st["turn"], conv, n)
	}
}

func TestSendOfAHeldSessionIdContinuesItsConversation(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	conv := firstOutsideTurn(t, s, resumeThread)

	r := s.run("send", resumeThread, "--json", "--run-id", "o2", "second")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	checkContinued(t, s, conv, "o2", 2)
	if c := conversationRecord(t, s.home, conv); !reflect.DeepEqual(c["turns"], []any{"o1", "o2"}) {
		t.Errorf("turns = %v, want [o1 o2]", c["turns"])
	}
}

func TestSendOfTheSessionAnExecStartedContinuesItsConversation(t *testing.T) {
	const execThread = "00000000-0000-4000-8000-000000000001" // what the exec-ok fixture reports
	for _, withRollout := range []bool{false, true} {
		name := "without a rollout"
		if withRollout {
			name = "with a rollout"
		}
		t.Run(name, func(t *testing.T) {
			codexHome := t.TempDir()
			if withRollout {
				writeRollout(t, codexHome, execThread, gitRepo(t), "outside-model", "medium")
			}
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome)
			conv := startConversation(t, s, "t1")

			r := s.run("send", execThread, "--json", "--run-id", "t2", "second")
			if r.code != 0 {
				t.Fatalf("exit %d stderr %s", r.code, r.stderr)
			}

			checkContinued(t, s, conv, "t2", 2)
		})
	}
}

func TestSendOfAHeldSessionIdNeedsNoRollout(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	conv := firstOutsideTurn(t, s, resumeThread)
	if err := os.RemoveAll(filepath.Join(codexHome, "sessions")); err != nil {
		t.Fatal(err)
	}

	r := s.run("send", resumeThread, "--json", "--run-id", "o2", "second")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	checkContinued(t, s, conv, "o2", 2)
}

func TestAdoptionRecordsTheSessionBeforeTheRunStarts(t *testing.T) {
	const outside = "00000000-0000-4000-8000-0000000000a1" // not the id the fixture's run reports
	codexHome := t.TempDir()
	writeRollout(t, codexHome, outside, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome).set("MSGATE_TEST_WORKER_STALL_MS", "1500")
	cleanupJob(t, s, "o1")
	p := s.start("o1", "send", outside, "--background", "--run-id", "o1", "carry on")

	queued := waitForState(t, s, "o1", "a queued state", func(m map[string]any) bool { return m["state"] == "queued" })
	// The run has not reported a thread yet: the session is the conversation's from its creation.
	c := conversationRecord(t, s.home, queued["conversation_id"].(string))
	if c["provider_session_id"] != outside {
		t.Errorf("provider_session_id = %v while the run is queued, want %s", c["provider_session_id"], outside)
	}
	if r := p.wait(15 * time.Second); r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	waitOK(t, s, "o1")
}

func TestSendOfASessionWithoutADirectoryNeedsCwd(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, "", "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	rec := s.recordTo()

	r := s.run("send", resumeThread, "carry on")

	if r.code != 2 || !strings.Contains(r.stderr, "--cwd") {
		t.Errorf("exit %d stderr %q, want exit 2 pointing at --cwd", r.code, r.stderr)
	}
	if exists(rec) || conversationCount(t, s.home) != 0 {
		t.Error("the provider ran or a conversation was created")
	}
}

func TestSendOfASessionWhoseDirectoryIsGoneNeedsCwd(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gone, "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	rec := s.recordTo()

	r := s.run("send", resumeThread, "carry on")

	if r.code != 2 || !strings.Contains(r.stderr, "--cwd") || !strings.Contains(r.stderr, gone) {
		t.Errorf("exit %d stderr %q, want exit 2 naming %s and pointing at --cwd", r.code, r.stderr, gone)
	}
	if exists(rec) || conversationCount(t, s.home) != 0 {
		t.Error("the provider ran or a conversation was created")
	}
}

func TestSendOfASessionRunsInTheCwdFlagInstead(t *testing.T) {
	elsewhere := gitRepo(t)
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, filepath.Join(t.TempDir(), "gone"), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	rec := s.recordTo()

	if r := s.run("send", resumeThread, "--cwd", elsewhere, "carry on"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	checkResumeArgv(t, readRecord(t, rec).Argv, elsewhere, []string{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}, resumeThread)

	missing := filepath.Join(t.TempDir(), "missing")
	r := s.run("send", resumeThread, "--cwd", missing, "carry on")
	if r.code != 2 || !strings.Contains(r.stderr, missing) {
		t.Errorf("exit %d stderr %q, want exit 2 naming %s", r.code, r.stderr, missing)
	}
	if got := conversationCount(t, s.home); got != 1 {
		t.Errorf("%d conversations, want the adopted one only", got)
	}
	if runs, _ := os.ReadDir(filepath.Join(s.home, "runs")); len(runs) != 1 {
		t.Errorf("%d run directories, want the adopted turn's only", len(runs))
	}
}

func TestSendOfAnIdNoSessionCouldHaveIsNotFound(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	rec := s.recordTo()
	for name, args := range map[string][]string{
		"an id no run id may be":     {"send", "../escape", "hi"},
		"an unknown provider":        {"send", "nosuch", "hi", "--provider", "bogus"},
		"a bad id, unknown provider": {"send", "../escape", "hi", "--provider", "bogus"},
		"an unknown id":              {"send", "nosuch", "hi"},
	} {
		r := s.run(args...)
		if r.code != 4 || !strings.Contains(r.stderr, args[1]) {
			t.Errorf("%s: exit %d stderr %q, want exit 4 naming %s", name, r.code, r.stderr, args[1])
		}
	}
	if exists(rec) {
		t.Error("the provider ran")
	}
}

func TestSendOfARunWhoseConversationIsGoneIsNotFound(t *testing.T) {
	const run = "00000000-0000-4000-8000-0000000000b1"
	codexHome := t.TempDir()
	writeRollout(t, codexHome, run, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	conv := startConversation(t, s, run)
	if err := os.Remove(filepath.Join(s.home, "conversations", conv+".json")); err != nil {
		t.Fatal(err)
	}
	rec := s.recordTo()

	r := s.run("send", run, "carry on")

	if r.code != 4 || !strings.Contains(r.stderr, run) {
		t.Errorf("exit %d stderr %q, want exit 4 naming %s", r.code, r.stderr, run)
	}
	if exists(rec) || conversationCount(t, s.home) != 0 {
		t.Error("the provider ran or a conversation was created")
	}
}

// raceSends has one send of the session per run id start at once, each from a
// process of its own over the sandbox's home.
func raceSends(t *testing.T, s *sandbox, runs []string) []result {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]result, len(runs))
	for i, id := range runs {
		sb := siblingSandbox(t, s)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = sb.run("send", resumeThread, "--json", "--run-id", id, "x")
		}()
	}
	close(start)
	wg.Wait()
	return results
}

func TestSimultaneousFirstSendsOfASessionAdoptItOnce(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome).set("FAKECODEX_SLEEP_MS", "1500")
	runs := []string{"sa", "sb"}
	results := raceSends(t, s, runs)

	won, lost := 0, 1
	if results[1].code == 0 {
		won, lost = 1, 0
	}
	if results[won].code != 0 || results[lost].code != 3 {
		t.Fatalf("exit codes %d and %d, want one 0 and one 3 (stderr %q / %q)", results[0].code, results[1].code, results[0].stderr, results[1].stderr)
	}
	if got := conversationCount(t, s.home); got != 1 {
		t.Fatalf("%d conversations, want 1", got)
	}
	conv := results[won].json(t)["conversation_id"].(string)
	c := conversationRecord(t, s.home, conv)
	checkFields(t, "conversation", c, map[string]any{"provider_session_id": resumeThread})
	if !reflect.DeepEqual(c["turns"], []any{runs[won]}) {
		t.Errorf("turns = %v, want only %s", c["turns"], runs[won])
	}
	checkFields(t, "refused send", results[lost].json(t), map[string]any{"conversation_id": conv, "sdk_status": "busy"})
	for _, id := range []string{conv, runs[won]} {
		if !strings.Contains(results[lost].stderr, id) {
			t.Errorf("stderr %q does not name %s", results[lost].stderr, id)
		}
	}
	if exists(filepath.Join(s.home, "runs", runs[lost])) {
		t.Error("a refused send left a run directory")
	}
}

// One session has one conversation: a turn that reports a session another
// conversation holds does not take it over.
func TestARunReportingASessionAnotherConversationHoldsDoesNotRecordIt(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	holder := firstOutsideTurn(t, s, resumeThread)

	r := s.run("exec", "--json", "--run-id", "x1", "hi")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	started := r.json(t)["conversation_id"].(string)
	if c := conversationRecord(t, s.home, started); c["provider_session_id"] != nil {
		t.Errorf("the new conversation holds %v, want no session: %s holds it", c["provider_session_id"], holder)
	}
	if c := conversationRecord(t, s.home, holder); c["provider_session_id"] != resumeThread {
		t.Errorf("the holder's session = %v, want %s", c["provider_session_id"], resumeThread)
	}
	if !strings.Contains(r.stderr, holder) {
		t.Errorf("stderr %q does not name the holder %s", r.stderr, holder)
	}
}

// A turn msgate started may be the one that announces the session: while one
// has not reported its session, a send of the session waits for it rather than
// creating a second conversation for the same thread.
func TestSendOfASessionWaitsForARunningTurnThatHasNotAnnouncedItYet(t *testing.T) {
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome).
		set("FAKECODEX_DELAY_MS", "1500").set("FAKECODEX_SLEEP_MS", "3000")
	cleanupJob(t, s, "p1")
	if r := s.run("exec", "--background", "--json", "--run-id", "p1", "first"); r.code != 0 {
		t.Fatalf("exec exit %d stderr %s", r.code, r.stderr)
	}
	queued := waitForState(t, s, "p1", "an admitted run", func(m map[string]any) bool { return m["conversation_id"] != nil })
	conv := queued["conversation_id"].(string)

	r := s.run("send", resumeThread, "--json", "--run-id", "s1", "second")

	if r.code != 3 {
		t.Errorf("exit %d stderr %q, want 3", r.code, r.stderr)
	}
	if got := conversationCount(t, s.home); got != 1 {
		t.Errorf("%d conversations, want 1", got)
	}
	checkFields(t, "refused send", r.json(t), map[string]any{"conversation_id": conv, "sdk_status": "busy"})
	waitOK(t, s, "p1")
	if c := conversationRecord(t, s.home, conv); c["provider_session_id"] != resumeThread {
		t.Errorf("provider_session_id = %v, want %s", c["provider_session_id"], resumeThread)
	}
}

func TestAdoptionFlagsOverrideTheSessionAndBecomeItsDefaults(t *testing.T) {
	cases := []struct {
		name          string
		flags         []string
		modelSource   string
		effortSrc     string
		sandboxSource string
	}{
		{"by default", nil, "flag", "flag", "flag"},
		{"with the profile as settings source", []string{"--settings-source", "profile"}, "profile", "profile", "profile"},
		{"with the profile as effort source", []string{"--effort-source", "profile"}, "flag", "profile", "flag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codexHome := t.TempDir()
			cwd := gitRepo(t)
			writeRollout(t, codexHome, resumeThread, cwd, "outside-model", "medium")
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
			rec := s.recordTo()

			args := append([]string{"send", resumeThread, "--json", "--run-id", "o1", "--model", "m", "--effort", "low", "--sandbox", "workspace-write"}, c.flags...)
			r := s.run(append(args, "carry on")...)
			if r.code != 0 {
				t.Fatalf("exit %d stderr %s", r.code, r.stderr)
			}

			wantFlags := []string{"-s", "workspace-write", "-m", "m", "-c", "model_reasoning_effort=low"}
			checkResumeArgv(t, readRecord(t, rec).Argv, cwd, wantFlags, resumeThread)
			checkFields(t, "run record", lastRunRecord(t, s.home), map[string]any{
				"model": "m", "model_source": c.modelSource, "effort": "low", "effort_source": c.effortSrc,
			})
			checkFields(t, "request", readJSONFile(t, filepath.Join(s.home, "runs", "o1", "request.json")), map[string]any{
				"sandbox": "workspace-write", "sandbox_source": c.sandboxSource,
			})
			conv := r.json(t)["conversation_id"].(string)
			defaults, _ := conversationRecord(t, s.home, conv)["defaults"].(map[string]any)
			checkFields(t, "defaults", defaults, map[string]any{
				"model": "m", "model_source": c.modelSource, "effort": "low", "effort_source": c.effortSrc,
				"sandbox": "workspace-write", "sandbox_source": c.sandboxSource,
			})

			if r := s.run("send", resumeThread, "--json", "--run-id", "o2", "again"); r.code != 0 {
				t.Fatalf("second send: exit %d stderr %s", r.code, r.stderr)
			}
			checkResumeArgv(t, readRecord(t, rec).Argv, cwd, wantFlags, resumeThread)
		})
	}
}

func TestAdoptionOfASessionWithNoTurnContextLeavesModelAndEffortToTheProvider(t *testing.T) {
	codexHome := t.TempDir()
	cwd := gitRepo(t)
	writeMetaOnlyRollout(t, codexHome, resumeThread, cwd)
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	rec := s.recordTo()

	r := s.run("send", resumeThread, "--json", "--run-id", "o1", "carry on")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	argv := readRecord(t, rec).Argv
	checkResumeArgv(t, argv, cwd, []string{"-s", "read-only"}, resumeThread)
	for _, a := range argv {
		if a == "-m" || strings.Contains(a, "model_reasoning_effort") {
			t.Errorf("argv %v overrides the model or effort", argv)
		}
	}
	checkFields(t, "run record", lastRunRecord(t, s.home), map[string]any{
		"model_source": "provider-default", "effort_source": "provider-default",
	})
	conv := r.json(t)["conversation_id"].(string)
	defaults, _ := conversationRecord(t, s.home, conv)["defaults"].(map[string]any)
	checkFields(t, "defaults", defaults, map[string]any{
		"model_source": "provider-default", "effort_source": "provider-default",
	})
}

func TestAdoptionRecordsThePurposeOfTheSend(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{"given", []string{"--purpose", "delegation"}, "delegation"},
		{"left out", nil, "adhoc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codexHome := t.TempDir()
			writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)

			args := append([]string{"send", resumeThread, "--json", "--run-id", "o1"}, c.flags...)
			r := s.run(append(args, "carry on")...)

			if r.code != 0 {
				t.Fatalf("exit %d stderr %s", r.code, r.stderr)
			}
			checkFields(t, "run record", lastRunRecord(t, s.home), map[string]any{"purpose": c.want})
			defaults, _ := conversationRecord(t, s.home, r.json(t)["conversation_id"].(string))["defaults"].(map[string]any)
			checkFields(t, "defaults", defaults, map[string]any{"purpose": c.want})
		})
	}
}

func TestDryRunOfASessionIdPlansWithoutAdopting(t *testing.T) {
	codexHome := t.TempDir()
	cwd := gitRepo(t)
	writeRollout(t, codexHome, resumeThread, cwd, "outside-model", "medium")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome)
	rec := s.recordTo()

	r := s.run("send", resumeThread, "--dry-run", "--json", "--run-id", "o1", "carry on")

	plan := r.json(t)
	if r.code != 0 || plan["command"] != "send" {
		t.Fatalf("exit %d plan %v", r.code, plan)
	}
	argv, _ := plan["argv"].([]any)
	wantTail := []any{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}
	if len(argv) < 4+len(wantTail)+3 || !reflect.DeepEqual(argv[4:4+len(wantTail)], wantTail) || !reflect.DeepEqual(argv[len(argv)-3:], []any{"resume", resumeThread, "-"}) {
		t.Errorf("plan argv = %v", argv)
	}
	if got := conversationCount(t, s.home); got != 0 {
		t.Errorf("%d conversations, want none", got)
	}
	if exists(filepath.Join(s.home, "runs", "o1")) {
		t.Error("dry-run left a run directory")
	}
	if exists(rec) {
		t.Error("the provider ran")
	}
}
