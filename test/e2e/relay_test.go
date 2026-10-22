package e2e

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const childThread = "00000000-0000-4000-8000-000000000004" // a sub-agent of resumeThread

// writeChildRollout lays out the session file of a sub-agent the session
// parent spawned and addresses as address: a session_meta line naming the
// spawn, and no turn.
func writeChildRollout(t *testing.T, codexHome, id, cwd, parent, address string) {
	t.Helper()
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":%q,"depth":1,"agent_path":"/root/%s","agent_nickname":"N","agent_role":null}}},"thread_source":"subagent"}}`, id, cwd, parent, address)
	writeRolloutLines(t, codexHome, id, meta)
}

// checkRelayedStdin checks what the parent's provider received for the prompt
// q passed to its child helper: it asks for the child by its quoted name with
// followup_task, and ends with the caller's prompt.
func checkRelayedStdin(t *testing.T, stdin string) {
	t.Helper()
	for _, want := range []string{`"helper"`, "followup_task", "wait_agent"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("provider stdin %q does not contain %s", stdin, want)
		}
	}
	if !strings.HasSuffix(stdin, "\nq") {
		t.Errorf("provider stdin %q does not end with the caller's prompt q", stdin)
	}
}

// relaySandbox is a sandbox whose codex home holds a parent session and its
// sub-agent helper, both in cwd.
func relaySandbox(t *testing.T) (s *sandbox, cwd string) {
	t.Helper()
	codexHome := t.TempDir()
	cwd = gitRepo(t)
	writeRollout(t, codexHome, resumeThread, cwd, "outside-model", "medium")
	writeChildRollout(t, codexHome, childThread, cwd, resumeThread, "helper")
	return newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1")).set("CODEX_HOME", codexHome), cwd
}

func requestRecordOf(t *testing.T, s *sandbox, run string) map[string]any {
	t.Helper()
	return readJSONFile(t, filepath.Join(s.home, "runs", run, "request.json"))
}

func TestSendOfAChildSessionRelaysATurnOfTheConversationHoldingItsParent(t *testing.T) {
	s, cwd := relaySandbox(t)
	conv := firstOutsideTurn(t, s, resumeThread)
	rec := s.recordTo()

	r := s.run("send", childThread, "--json", "--run-id", "r1", "q")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	fake := readRecord(t, rec)
	checkResumeArgv(t, fake.Argv, cwd, []string{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}, resumeThread)
	checkRelayedStdin(t, fake.Stdin)
	if p := s.run("prompt", "r1"); p.stdout != "q" {
		t.Errorf("prompt of r1 = %q, want %q", p.stdout, "q")
	}
	relay, _ := requestRecordOf(t, s, "r1")["relay"].(map[string]any)
	checkFields(t, "relay", relay, map[string]any{"provider_session_id": childThread, "address": "helper"})
	checkContinued(t, s, conv, "r1", 2)
}

func TestSendOfAChildSessionIsRefusedWhileTheParentsConversationIsBusy(t *testing.T) {
	s, _ := relaySandbox(t)
	conv := firstOutsideTurn(t, s, resumeThread)
	s.set("FAKECODEX_SLEEP_MS", "1500")
	cleanupJob(t, s, "b1")
	if r := s.run("send", resumeThread, "--background", "--json", "--run-id", "b1", "busy"); r.code != 0 {
		t.Fatalf("background send: exit %d stderr %s", r.code, r.stderr)
	}

	r := s.run("send", childThread, "--json", "--run-id", "r1", "q")

	if r.code != 3 {
		t.Errorf("exit %d stderr %q, want 3", r.code, r.stderr)
	}
	checkFields(t, "refused send", r.json(t), map[string]any{"conversation_id": conv, "sdk_status": "busy"})
	if exists(filepath.Join(s.home, "runs", "r1")) {
		t.Error("a refused send left a run directory")
	}
	waitOK(t, s, "b1")
	if c := conversationRecord(t, s.home, conv); !reflect.DeepEqual(c["turns"], []any{"o1", "b1"}) {
		t.Errorf("turns = %v, want [o1 b1]", c["turns"])
	}
}

func TestFlagsOfARelayedSendApplyToThatTurnOnly(t *testing.T) {
	s, cwd := relaySandbox(t)
	firstOutsideTurn(t, s, resumeThread)
	rec := s.recordTo()

	if r := s.run("send", childThread, "--model", "m2", "--run-id", "r1", "q"); r.code != 0 {
		t.Fatalf("relayed send: exit %d stderr %s", r.code, r.stderr)
	}
	checkResumeArgv(t, readRecord(t, rec).Argv, cwd, []string{"-s", "read-only", "-m", "m2", "-c", "model_reasoning_effort=medium"}, resumeThread)

	if r := s.run("send", resumeThread, "--run-id", "r2", "x"); r.code != 0 {
		t.Fatalf("later send: exit %d stderr %s", r.code, r.stderr)
	}
	checkResumeArgv(t, readRecord(t, rec).Argv, cwd, []string{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}, resumeThread)
}

func TestSendOfAChildSessionAdoptsAParentNoConversationHolds(t *testing.T) {
	s, cwd := relaySandbox(t)
	rec := s.recordTo()

	r := s.run("send", childThread, "--json", "--run-id", "r1", "q")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	conv := r.json(t)["conversation_id"].(string)
	checkContinued(t, s, conv, "r1", 1)
	c := conversationRecord(t, s.home, conv)
	checkFields(t, "conversation", c, map[string]any{"provider": "codex", "provider_session_id": resumeThread})
	if !reflect.DeepEqual(c["turns"], []any{"r1"}) {
		t.Errorf("turns = %v, want [r1]", c["turns"])
	}
	fake := readRecord(t, rec)
	checkResumeArgv(t, fake.Argv, cwd, []string{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}, resumeThread)
	checkRelayedStdin(t, fake.Stdin)
	relay, _ := requestRecordOf(t, s, "r1")["relay"].(map[string]any)
	checkFields(t, "relay", relay, map[string]any{"provider_session_id": childThread, "address": "helper"})
}

func TestSendOfAChildSessionAConversationHoldsContinuesThatConversationUnrelayed(t *testing.T) {
	const execThread = "00000000-0000-4000-8000-000000000001" // what the exec-ok fixture reports
	codexHome := t.TempDir()
	writeRollout(t, codexHome, resumeThread, gitRepo(t), "outside-model", "medium")
	writeChildRollout(t, codexHome, execThread, gitRepo(t), resumeThread, "helper")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome)
	conv := startConversation(t, s, "t1")
	rec := s.recordTo()

	r := s.run("send", execThread, "--json", "--run-id", "t2", "second")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	checkContinued(t, s, conv, "t2", 2)
	fake := readRecord(t, rec)
	if tail := fake.Argv[len(fake.Argv)-3:]; !reflect.DeepEqual(tail, []string{"resume", execThread, "-"}) {
		t.Errorf("argv tail = %v, want resume %s -", tail, execThread)
	}
	if fake.Stdin != "second" {
		t.Errorf("provider stdin = %q, want %q", fake.Stdin, "second")
	}
	if _, has := requestRecordOf(t, s, "t2")["relay"]; has {
		t.Error("request.json of a turn that was not relayed has a relay field")
	}
}

func TestDryRunOfAChildSessionPlansTheParentsResumeAndCreatesNothing(t *testing.T) {
	s, _ := relaySandbox(t)
	conv := firstOutsideTurn(t, s, resumeThread)
	rec := s.recordTo()

	r := s.run("send", childThread, "--dry-run", "--json", "--run-id", "r1", "q")

	plan := r.json(t)
	argv, _ := plan["argv"].([]any)
	if r.code != 0 || plan["command"] != "send" || len(argv) < 3 || !reflect.DeepEqual(argv[len(argv)-3:], []any{"resume", resumeThread, "-"}) {
		t.Errorf("exit %d plan %v, want a send plan that resumes %s", r.code, plan, resumeThread)
	}
	if exists(filepath.Join(s.home, "runs", "r1")) || exists(rec) {
		t.Error("the dry run left a run directory or ran the provider")
	}
	if c := conversationRecord(t, s.home, conv); !reflect.DeepEqual(c["turns"], []any{"o1"}) {
		t.Errorf("turns = %v, want [o1]", c["turns"])
	}
	if got := conversationCount(t, s.home); got != 1 {
		t.Errorf("%d conversations, want 1", got)
	}
}

func TestBackgroundSendOfAChildSessionRelaysThePromptFromTheWorker(t *testing.T) {
	s, cwd := relaySandbox(t)
	conv := firstOutsideTurn(t, s, resumeThread)
	rec := s.recordTo()
	cleanupJob(t, s, "bg1")

	if r := s.run("send", childThread, "--background", "--json", "--run-id", "bg1", "q"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	waitOK(t, s, "bg1")

	fake := readRecord(t, rec)
	checkResumeArgv(t, fake.Argv, cwd, []string{"-s", "read-only", "-m", "outside-model", "-c", "model_reasoning_effort=medium"}, resumeThread)
	checkRelayedStdin(t, fake.Stdin)
	relay, _ := requestRecordOf(t, s, "bg1")["relay"].(map[string]any)
	checkFields(t, "relay", relay, map[string]any{"provider_session_id": childThread, "address": "helper"})
	checkContinued(t, s, conv, "bg1", 2)
}

const (
	// subAgentThread is the session the next tests send to: the id the exec-ok
	// fixture reports, so that the conversation keeps it after the turn.
	subAgentThread = "00000000-0000-4000-8000-000000000001"
	missingParent  = "00000000-0000-4000-8000-000000000002" // a session whose rollout is never written
)

// writeReviewRollout lays out the session file of a review sub-thread: a
// sub-agent session that no parent spawned.
func writeReviewRollout(t *testing.T, codexHome, id, cwd string) {
	t.Helper()
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":%q,"source":{"subagent":"review"},"thread_source":"subagent"}}`, id, cwd)
	writeRolloutLines(t, codexHome, id, meta)
}

// checkAdoptedAsItself checks that sending to session id made one new
// conversation holding id, which the provider resumed itself with the
// caller's prompt, and that nothing was relayed.
func checkAdoptedAsItself(t *testing.T, s *sandbox, rec, cwd, id string) {
	t.Helper()
	r := s.run("send", id, "--json", "--run-id", "r1", "q")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	fake := readRecord(t, rec)
	checkResumeArgv(t, fake.Argv, cwd, []string{"-s", "read-only"}, id)
	if fake.Stdin != "q" {
		t.Errorf("provider stdin = %q, want %q", fake.Stdin, "q")
	}
	if _, has := requestRecordOf(t, s, "r1")["relay"]; has {
		t.Error("request.json of a turn that was not relayed has a relay field")
	}
	conv := r.json(t)["conversation_id"].(string)
	checkContinued(t, s, conv, "r1", 1)
	checkFields(t, "conversation", conversationRecord(t, s.home, conv), map[string]any{"provider": "codex", "provider_session_id": id})
}

func TestSendOfAReviewSubThreadAdoptsItAsItself(t *testing.T) {
	codexHome := t.TempDir()
	cwd := gitRepo(t)
	writeReviewRollout(t, codexHome, subAgentThread, cwd)
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome)

	checkAdoptedAsItself(t, s, s.recordTo(), cwd, subAgentThread)
}

func TestSendOfAChildWhoseParentIsUnknownAdoptsTheChildAsItself(t *testing.T) {
	codexHome := t.TempDir()
	cwd := gitRepo(t)
	writeChildRollout(t, codexHome, subAgentThread, cwd, missingParent, "helper")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome)

	checkAdoptedAsItself(t, s, s.recordTo(), cwd, subAgentThread)
}

func TestSendOfAChildWhoseParentIsItselfAChildAdoptsTheChildAsItself(t *testing.T) {
	codexHome := t.TempDir()
	cwd := gitRepo(t)
	writeRollout(t, codexHome, resumeThread, cwd, "outside-model", "medium")
	writeChildRollout(t, codexHome, childThread, cwd, resumeThread, "helper")
	writeChildRollout(t, codexHome, subAgentThread, cwd, childThread, "grandhelper")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome)

	checkAdoptedAsItself(t, s, s.recordTo(), cwd, subAgentThread)
}

// The provider's refusal to resume a child on its own reaches the caller as
// the provider wrote it.
func TestRefusalOfTheProviderToResumeAChildIsPassedThrough(t *testing.T) {
	const refusal = "cannot resume an unloaded multi-agent v2 sub-agent through its parent"
	codexHome := t.TempDir()
	writeChildRollout(t, codexHome, subAgentThread, gitRepo(t), missingParent, "helper")
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CODEX_HOME", codexHome).
		set("FAKECODEX_EXIT", "1").set("FAKECODEX_STDERR", refusal+"\n")

	r := s.run("send", subAgentThread, "--json", "--run-id", "r1", "q")

	if r.code != 1 {
		t.Errorf("exit %d stderr %q, want 1", r.code, r.stderr)
	}
	status := s.run("status", "r1", "--json").json(t)
	if status["outcome"] != "error" {
		t.Errorf("outcome = %v, want error", status["outcome"])
	}
	if excerpt, _ := status["error_excerpt"].(string); !strings.Contains(excerpt, refusal) {
		t.Errorf("error_excerpt = %q, want it to contain %q", excerpt, refusal)
	}
}
