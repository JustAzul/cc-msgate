package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeRollout lays out the session file of a codex session some other client
// started: a session_meta line, then one turn_context.
func writeRollout(t *testing.T, codexHome, id, cwd, model, effort string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "10", "22")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q,"originator":"codex_exec","source":"exec","thread_source":"user"}}`, id, id, cwd)
	turn := fmt.Sprintf(`{"type":"turn_context","payload":{"cwd":%q,"model":%q,"effort":%q,"sandbox_policy":{"type":"workspace-write"},"approval_policy":"on-request"}}`, cwd, model, effort)
	body := meta + "\n" + turn + "\n"
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

	want := "the provider session " + resumeThread + " records no working directory; pass --cwd"
	if r.code != 2 || !strings.Contains(r.stderr, want) {
		t.Errorf("exit %d stderr %q, want exit 2 saying %q", r.code, r.stderr, want)
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

	want := fmt.Sprintf("the provider session's working directory %q no longer exists; pass --cwd to run this turn elsewhere", gone)
	if r.code != 2 || !strings.Contains(r.stderr, want) {
		t.Errorf("exit %d stderr %q, want exit 2 saying %q", r.code, r.stderr, want)
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
	if want := fmt.Sprintf("--cwd %q is not an existing directory", missing); r.code != 2 || !strings.Contains(r.stderr, want) {
		t.Errorf("exit %d stderr %q, want exit 2 saying %q", r.code, r.stderr, want)
	}
}

func TestSendOfAnIdNoSessionCouldHaveIsNotFound(t *testing.T) {
	const notFound = `no conversation, run or provider session `
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	rec := s.recordTo()
	for name, args := range map[string][]string{
		"an id no run id may be":     {"send", "../escape", "hi"},
		"an unknown provider":        {"send", "nosuch", "hi", "--provider", "bogus"},
		"a bad id, unknown provider": {"send", "../escape", "hi", "--provider", "bogus"},
		"an unknown id":              {"send", "nosuch", "hi"},
	} {
		r := s.run(args...)
		if r.code != 4 || !strings.Contains(r.stderr, notFound+fmt.Sprintf("%q", args[1])) {
			t.Errorf("%s: exit %d stderr %q, want exit 4 saying %s%q", name, r.code, r.stderr, notFound, args[1])
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

	if r.code != 4 {
		t.Errorf("exit %d stderr %q, want 4", r.code, r.stderr)
	}
	if exists(rec) || conversationCount(t, s.home) != 0 {
		t.Error("the provider ran or a conversation was created")
	}
}
