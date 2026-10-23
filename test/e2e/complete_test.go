package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

const pingRequest = `{"messages":[{"role":"user","text":"ping"}]}`

// completeSandbox is a sandbox whose Codex answers "pong" and whose stdin
// holds request.
func completeSandbox(t *testing.T, request string) *sandbox {
	t.Helper()
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	s.stdin = request
	return s
}

func TestCompleteRunsCodexBareOnceAndPrintsTheAnswer(t *testing.T) {
	s := completeSandbox(t, pingRequest)
	rec := s.recordTo()

	r := s.run("complete", "--provider", "codex")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if r.stdout != "pong" {
		t.Errorf("stdout = %q, want the answer", r.stdout)
	}
	got := readRecord(t, rec)
	oneshot := filepath.Join(s.home, "oneshot")
	for _, want := range [][]string{
		{"exec", "-C", oneshot, "-s", "read-only"},
		{"-c", "model_instructions_file=" + filepath.Join(s.home, "harness", "instructions.md")},
		{"-c", "features.shell_tool=false"},
		{"-c", "include_environment_context=false"},
		{"--ephemeral"},
		{"--skip-git-repo-check"},
	} {
		if !hasRun(got.Argv, want) {
			t.Errorf("argv %q lacks %q", got.Argv, want)
		}
	}
	if !samePath(t, got.Cwd, oneshot) {
		t.Errorf("cwd = %q, want %q", got.Cwd, oneshot)
	}
	if got.Stdin != "ping" {
		t.Errorf("stdin = %q, want the prompt as is", got.Stdin)
	}
}

func TestCompleteRecordsOneModelCallWithTheCallersFieldsAndCodexsUsage(t *testing.T) {
	s := completeSandbox(t, pingRequest)

	r := s.run("complete", "--provider", "codex", "--model", "model-a",
		"--session-id", "sess-1", "--run-id", "r-1", "--source", "mod-summary", "--call-id", "call-1",
		"--attr", "purpose=live", "--attr", "step=2")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	calls := modelCalls(t, s.home)
	if len(calls) != 1 {
		t.Fatalf("model calls = %v, want one", calls)
	}
	delete(calls[0], "ts")
	want := map[string]any{
		"v": float64(1), "kind": "model_call", "call_id": "call-1", "session_id": "sess-1",
		"provider": "codex", "model": "model-a", "source": "mod-summary", "run_id": "r-1",
		"attrs": map[string]any{"purpose": "live", "step": "2"},
		"usage": map[string]any{
			"input_tokens": float64(18573), "cached_input_tokens": float64(11008), "cache_write_input_tokens": float64(0),
			"output_tokens": float64(5), "reasoning_output_tokens": float64(0),
		},
	}
	if !reflect.DeepEqual(calls[0], want) {
		t.Errorf("record = %v, want %v", calls[0], want)
	}
}

func TestCompletePassesTheModelAndEffortAndCreatesNoConversationOrRun(t *testing.T) {
	s := completeSandbox(t, pingRequest)
	rec := s.recordTo()

	if r := s.run("complete", "--model", "model-a", "--effort", "low"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	argv := readRecord(t, rec).Argv
	for _, want := range [][]string{{"-m", "model-a"}, {"-c", "model_reasoning_effort=low"}} {
		if !hasRun(argv, want) {
			t.Errorf("argv %q lacks %q", argv, want)
		}
	}
	for _, dir := range []string{"conversations", "runs"} {
		if exists(filepath.Join(s.home, dir)) {
			t.Errorf("%s/ exists after a completion", dir)
		}
	}
}

func TestCompleteJSONCarriesTheOutcomeTheTextTheModelAndCodexsUsage(t *testing.T) {
	s := completeSandbox(t, pingRequest)

	r := s.run("complete", "--json", "--model", "model-a")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	want := map[string]any{
		"outcome": "answered", "text": "pong", "model": "model-a",
		"usage": map[string]any{
			"input_tokens": float64(18573), "cached_input_tokens": float64(11008),
			"output_tokens": float64(5), "reasoning_output_tokens": float64(0),
		},
	}
	if got := r.json(t); !reflect.DeepEqual(got, want) {
		t.Errorf("json = %v, want %v", got, want)
	}
}

// instructionsFileOf is the file argv names in its model_instructions_file
// override, or "" when it names none.
func instructionsFileOf(argv []string) string {
	for _, arg := range argv {
		if path, ok := strings.CutPrefix(arg, "model_instructions_file="); ok {
			return path
		}
	}
	return ""
}

func TestCompleteSystemReplacesTheInstructionsForThatCallOnly(t *testing.T) {
	s := completeSandbox(t, `{"system":"Answer in one word.","messages":[{"role":"user","text":"ping"}]}`)
	rec := s.recordTo()

	r := s.run("complete")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	got := readRecord(t, rec)
	if got.Instructions != "Answer in one word." {
		t.Errorf("instructions at the call = %q", got.Instructions)
	}
	file := instructionsFileOf(got.Argv)
	if file == "" || file == filepath.Join(s.home, "harness", "instructions.md") || strings.HasPrefix(file, filepath.Join(s.home, "oneshot")) {
		t.Errorf("instructions file = %q, want a file of the call's own outside the working directory", file)
	}
	if exists(file) {
		t.Errorf("instructions file %s is still there after the call", file)
	}
	if entries, err := os.ReadDir(filepath.Join(s.home, "oneshot")); err != nil || len(entries) != 0 {
		t.Errorf("the working directory holds %v (error %v), want it empty", entries, err)
	}
}

func TestCompleteWithoutSystemRunsOnTheHarnessInstructions(t *testing.T) {
	s := completeSandbox(t, pingRequest)
	rec := s.recordTo()

	if r := s.run("complete"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	if got := readRecord(t, rec).Instructions; got != "Follow the user's instructions. Be concise.\n" {
		t.Errorf("instructions at the call = %q", got)
	}
}

func TestCompleteRendersSeveralMessagesAsATranscript(t *testing.T) {
	s := completeSandbox(t, `{"messages":[{"role":"user","text":"hi"},{"role":"assistant","text":"hello"},{"role":"user","text":"bye"}]}`)
	rec := s.recordTo()

	if r := s.run("complete"); r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}

	if got := readRecord(t, rec).Stdin; got != "User: hi\n\nAssistant: hello\n\nUser: bye" {
		t.Errorf("stdin = %q", got)
	}
}

func TestCompleteEmptyReplyExitsZeroWithNoText(t *testing.T) {
	s := completeSandbox(t, pingRequest).set("FAKECODEX_OUTPUT", "")

	r := s.run("complete", "--json")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if got := r.json(t); got["outcome"] != "empty-reply" || got["text"] != "" {
		t.Errorf("json = %v, want an empty-reply with no text", got)
	}
}

func TestCompleteFailedCallClassifiesItsErrorAndMarksCodexDown(t *testing.T) {
	cases := []struct{ message, class, reason string }{
		{"failure-rate-limit-usage-limit-plus", "rate-limit", "rate-limited"},
		{"failure-quota-exceeded", "quota", "quota-exhausted"},
		{"failure-auth-unauthorized", "auth", "not-logged-in"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			s, log := failingSandbox(t, tc.message)
			s.stdin = pingRequest

			r := s.run("complete", "--json")

			if r.code != 1 {
				t.Errorf("exit %d, want the provider's 1; stderr %q", r.code, r.stderr)
			}
			if got := r.json(t); got["outcome"] != "api-error" || got["failure"] != tc.class || got["text"] != "" {
				t.Errorf("json = %v, want an api-error of class %s with no text", got, tc.class)
			}
			wantCodexMarked(t, s, log, tc.reason)
			if calls := modelCalls(t, s.home); len(calls) != 0 {
				t.Errorf("model calls = %v, want none for a call that used no tokens", calls)
			}
		})
	}
}

func TestCompleteOrdinaryFailureIsAnApiErrorOfClassOtherAndMarksNothing(t *testing.T) {
	s, log := healthSandbox(t)
	s.set("FAKECODEX_FIXTURE", fixture("error-400")).set("FAKECODEX_EXIT", "1")
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(true, "", "check", 10*time.Second, nil),
		"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
	})
	s.stdin = pingRequest

	r := s.run("complete", "--json")

	if r.code != 1 {
		t.Errorf("exit %d, want the provider's 1; stderr %q", r.code, r.stderr)
	}
	if got := r.json(t); got["outcome"] != "api-error" || got["failure"] != "other" {
		t.Errorf("json = %v, want an api-error of class other", got)
	}
	wantCodexUntouched(t, s, log)
}

func TestCompleteSignalStopsTheCodexGroupAtOnceAndExitsAsACancelledRun(t *testing.T) {
	for name, sig := range map[string]syscall.Signal{"SIGTERM": syscall.SIGTERM, "SIGINT": syscall.SIGINT} {
		t.Run(name, func(t *testing.T) {
			s := completeSandbox(t, pingRequest).set("FAKECODEX_SLEEP_MS", "30000")
			rec := s.recordTo()
			p := s.start("", "complete")
			if !waitFor(func() bool { return exists(rec) }) {
				t.Fatal("the fake Codex never started")
			}
			pid := readRecord(t, rec).Pid

			p.signal(sig)
			r := p.wait(10 * time.Second)

			if r.code != 130 {
				t.Errorf("exit %d, want 130; stderr %q", r.code, r.stderr)
			}
			assertGroupGone(t, pid)
			if calls := modelCalls(t, s.home); len(calls) != 0 {
				t.Errorf("model calls = %v, want none for a stopped call", calls)
			}
		})
	}
}

func TestCompleteTimeoutEndsCodexAndExitsLikeExec(t *testing.T) {
	s := completeSandbox(t, pingRequest).set("FAKECODEX_SLEEP_MS", "30000")
	rec := s.recordTo()

	r := s.run("complete", "--timeout", "1")

	if r.code != 124 {
		t.Errorf("exit %d, want 124; stderr %q", r.code, r.stderr)
	}
	assertGroupGone(t, readRecord(t, rec).Pid)
}

func TestCompleteRefusesWhatItCannotTakeWithUsageAndRunsNothing(t *testing.T) {
	cases := map[string]struct {
		request string
		args    []string
	}{
		"another provider":           {pingRequest, []string{"--provider", "claude"}},
		"a request that is not JSON": {"ping", nil},
		"no messages":                {`{"messages":[]}`, nil},
		"content that is not text":   {`{"messages":[{"role":"user","text":[{"type":"image"}]}]}`, nil},
		"a message with no text":     {`{"messages":[{"role":"user","content":"ping"}]}`, nil},
		"a system that is not text":  {`{"system":7,"messages":[{"role":"user","text":"ping"}]}`, nil},
		"an unknown role":            {`{"messages":[{"role":"tool","text":"ping"}]}`, nil},
		"no closing user message":    {`{"messages":[{"role":"user","text":"a"},{"role":"assistant","text":"b"}]}`, nil},
		"a bad call id":              {pingRequest, []string{"--call-id", "bad id"}},
		"an extra argument":          {pingRequest, []string{"ping"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := completeSandbox(t, tc.request)
			rec := s.recordTo()

			r := s.run(append([]string{"complete"}, tc.args...)...)

			if r.code != 2 {
				t.Errorf("exit %d, want 2; stderr %q", r.code, r.stderr)
			}
			if exists(rec) {
				t.Error("Codex ran")
			}
			if calls := modelCalls(t, s.home); len(calls) != 0 {
				t.Errorf("model calls = %v, want none", calls)
			}
		})
	}
}

func TestCompleteWithoutCodexExitsProviderMissingAndRecordsNoModelCall(t *testing.T) {
	s := completeSandbox(t, pingRequest).withoutProvider()

	r := s.run("complete", "--json")

	if r.code != 127 {
		t.Fatalf("exit %d, want 127; stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if m := r.json(t); m["sdk_status"] != "provider_missing" || m["exit_code"] != float64(127) {
		t.Errorf("error object = %v, want provider_missing with exit_code 127", m)
	}
	if calls := modelCalls(t, s.home); len(calls) != 0 {
		t.Errorf("model calls = %v, want none", calls)
	}
}
