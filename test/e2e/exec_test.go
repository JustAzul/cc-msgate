package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const execOKThread = "00000000-0000-4000-8000-000000000001"

func TestExecForeground(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()
	cwd := symlinkedGitRepo(t)
	r := s.run(append([]string{"exec"}, secondOpinionFlags("--cwd", cwd, "--run-id", "fg1", "q")...)...)
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	runDir := filepath.Join(s.home, "runs", "fg1")
	out := filepath.Join(runDir, "output.md")
	if len(r.lines()) != 1 || r.lastLine() != out {
		t.Errorf("stdout = %q, want exactly %q", r.stdout, out)
	}
	if got := readFile(t, out); got != "pong" {
		t.Errorf("output.md = %q", got)
	}
	if got := readFile(t, filepath.Join(runDir, "prompt.md")); got != "q" {
		t.Errorf("prompt.md = %q", got)
	}

	state := readJSONFile(t, filepath.Join(runDir, "state.json"))
	for k, want := range map[string]any{
		"run_id": "fg1", "state": "done", "outcome": "ok", "exit_code": float64(0),
		"turn": float64(1), "background": false, "unparsed_events": float64(0),
		"output_path": out, "run_dir": runDir, "error_excerpt": nil,
	} {
		if !reflect.DeepEqual(state[k], want) {
			t.Errorf("state.%s = %v, want %v", k, state[k], want)
		}
	}
	for _, k := range []string{"admitted_at", "started_at", "ended_at"} {
		if s, _ := state[k].(string); !strings.HasSuffix(s, "Z") {
			t.Errorf("state.%s = %v", k, state[k])
		}
	}

	convID := state["conversation_id"].(string)
	conv := readJSONFile(t, filepath.Join(s.home, "conversations", convID+".json"))
	if conv["provider"] != "codex" || conv["provider_session_id"] != execOKThread || conv["cwd"] != cwd {
		t.Errorf("conversation = %v", conv)
	}
	if !reflect.DeepEqual(conv["turns"], []any{"fg1"}) || conv["active_run_id"] != nil {
		t.Errorf("turns/active = %v / %v", conv["turns"], conv["active_run_id"])
	}
	defaults := conv["defaults"].(map[string]any)
	if defaults["purpose"] != "second-opinion" {
		t.Errorf("defaults = %v", defaults)
	}

	req := readJSONFile(t, filepath.Join(runDir, "request.json"))
	if req["purpose"] != "second-opinion" || req["source"] != "cli" || req["provider"] != "codex" {
		t.Errorf("request.json = %v", req)
	}
	if req["model"] != "gpt-6.1-sol" || req["model_source"] != "profile" {
		t.Errorf("request.json model = %v (%v)", req["model"], req["model_source"])
	}

	rec0 := oneRecord(t, s.home)
	usage, _ := rec0["usage"].(map[string]any)
	for k, want := range map[string]any{
		"command": "exec", "turn": float64(1), "outcome": "ok", "model": "gpt-6.1-sol",
		"model_source": "profile", "provider_session_id": execOKThread, "conversation_id": convID,
	} {
		if rec0[k] != want {
			t.Errorf("telemetry %s = %v, want %v", k, rec0[k], want)
		}
	}
	if usage["output_tokens"] != float64(5) {
		t.Errorf("telemetry usage = %v", rec0["usage"])
	}

	fake := readRecord(t, rec)
	if fake.Stdin != "q" || !samePath(t, fake.Cwd, cwd) {
		t.Errorf("fake saw stdin=%q cwd=%q", fake.Stdin, fake.Cwd)
	}
	// The settings come from the flags the caller passed.
	wantArgv := []string{"codex", "exec", "-C", cwd, "-s", "read-only", "-m", "gpt-6.1-sol",
		"-c", "model_reasoning_effort=high", "--json", "-o", out, "-"}
	if !reflect.DeepEqual(fake.Argv[1:], wantArgv[1:]) {
		t.Errorf("fake argv = %v, want %v", fake.Argv[1:], wantArgv[1:])
	}
}

func TestExecJSONOutput(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	r := s.run("exec", "--json", "--run-id", "j1", "q")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	m := r.json(t)
	runDir := filepath.Join(s.home, "runs", "j1")
	for k, want := range map[string]any{
		"run_id": "j1", "state": "done", "outcome": "ok", "sdk_status": "ok",
		"provider_exit": float64(0), "exit_code": float64(0),
		"output_path": filepath.Join(runDir, "output.md"), "run_dir": runDir,
	} {
		if !reflect.DeepEqual(m[k], want) {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	if c, _ := m["conversation_id"].(string); !strings.HasPrefix(c, "c-") {
		t.Errorf("conversation_id = %v", m["conversation_id"])
	}
}

func TestExecPromptFromStdin(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()
	s.stdin = "from stdin\nsecond line"
	r := s.run("exec", "--run-id", "in1", "-")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if got := readFile(t, filepath.Join(s.home, "runs", "in1", "prompt.md")); got != s.stdin {
		t.Errorf("prompt.md = %q", got)
	}
	if got := readRecord(t, rec).Stdin; got != s.stdin {
		t.Errorf("fake stdin = %q", got)
	}
}

func TestExecPromptFromFile(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()
	pf := filepath.Join(t.TempDir(), "p.md")
	if err := os.WriteFile(pf, []byte("file prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := s.run("exec", "--run-id", "pf1", "--prompt-file", pf)
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if got := readFile(t, filepath.Join(s.home, "runs", "pf1", "prompt.md")); got != "file prompt\n" {
		t.Errorf("prompt.md = %q", got)
	}
	if got := readRecord(t, rec).Stdin; got != "file prompt\n" {
		t.Errorf("fake stdin = %q", got)
	}
}

func TestOutcomeClassification(t *testing.T) {
	cases := []struct {
		name   string
		env    map[string]string
		args   []string
		want   string
		exit   int
		output string
	}{
		{"empty output", map[string]string{"FAKECODEX_OUTPUT": ""}, nil, "empty", 0, ""},
		{"clean", map[string]string{"FAKECODEX_OUTPUT": "  No Material Findings.\n"}, []string{"--clean-sentinel", "no material findings."}, "clean", 0, ""},
		{"material label", map[string]string{"FAKECODEX_OUTPUT": "1. bug in x"}, []string{"--clean-sentinel", "No material findings.", "--material-label", "findings"}, "findings", 0, ""},
		{"default label", nil, nil, "ok", 0, "pong"},
		{"missing output file", map[string]string{}, nil, "ok", 0, ""}, // fixture has a message: file written
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
			for k, v := range c.env {
				s.set(k, v)
			}
			args := append([]string{"exec", "--run-id", "o1", "--json"}, c.args...)
			r := s.run(append(args, "q")...)
			if r.code != c.exit {
				t.Fatalf("exit %d stderr %s", r.code, r.stderr)
			}
			if got := r.json(t)["outcome"]; got != c.want {
				t.Errorf("outcome = %v, want %q", got, c.want)
			}
			st := readJSONFile(t, filepath.Join(s.home, "runs", "o1", "state.json"))
			if st["outcome"] != c.want || st["state"] != "done" {
				t.Errorf("state.json outcome=%v state=%v", st["outcome"], st["state"])
			}
		})
	}
}

func TestEmptyOutputWhenProviderWritesNoFile(t *testing.T) {
	// A fixture with no agent message and no FAKECODEX_OUTPUT: the file is missing.
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("error-400"))
	r := s.run("exec", "--run-id", "m1", "--json", "q")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if got := r.json(t)["outcome"]; got != "empty" {
		t.Errorf("outcome = %v", got)
	}
	if exists(filepath.Join(s.home, "runs", "m1", "output.md")) {
		t.Error("output.md should be missing")
	}
}

func TestUnknownEventsAreCounted(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-unknown-events"))
	r := s.run("exec", "--run-id", "u1", "--json", "q")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	st := readJSONFile(t, filepath.Join(s.home, "runs", "u1", "state.json"))
	if st["unparsed_events"] != float64(1) || st["state"] != "done" || st["outcome"] != "ok" {
		t.Errorf("state = %v", st)
	}
}

func TestProviderMissing(t *testing.T) {
	s := newSandbox(t).withoutProvider()
	r := s.run("exec", "--run-id", "x1", "--json", "q")
	if r.code != 127 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	m := r.json(t)
	if m["state"] != "failed" || m["outcome"] != "error" || m["exit_code"] != float64(127) || m["provider_exit"] != nil {
		t.Errorf("json = %v", m)
	}
	st := readJSONFile(t, filepath.Join(s.home, "runs", "x1", "state.json"))
	if st["state"] != "failed" || st["outcome"] != "error" || st["exit_code"] != float64(127) {
		t.Errorf("state = %v", st)
	}
	if ex, _ := st["error_excerpt"].(string); !strings.Contains(ex, "codex") {
		t.Errorf("error_excerpt = %v", st["error_excerpt"])
	}
	conv := readJSONFile(t, filepath.Join(s.home, "conversations", st["conversation_id"].(string)+".json"))
	if conv["active_run_id"] != nil {
		t.Errorf("conversation still busy: %v", conv["active_run_id"])
	}
	// Plain mode still prints the output path as its only stdout line.
	r = s.run("exec", "--run-id", "x2", "q")
	if r.code != 127 || r.lastLine() != filepath.Join(s.home, "runs", "x2", "output.md") || len(r.lines()) != 1 {
		t.Errorf("plain: exit %d stdout %q", r.code, r.stdout)
	}
}

func TestModelRejected(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("error-400")).set("FAKECODEX_EXIT", "1").
		set("FAKECODEX_STDERR", "informational line\n")
	r := s.run("exec", "--run-id", "e1", "--json", "--model", "no-such-model-xyz", "q")
	if r.code != 1 {
		t.Fatalf("exit %d", r.code)
	}
	m := r.json(t)
	if m["state"] != "failed" || m["outcome"] != "error" || m["provider_exit"] != float64(1) || m["exit_code"] != float64(1) || m["sdk_status"] != "ok" {
		t.Errorf("json = %v", m)
	}
	st := readJSONFile(t, filepath.Join(s.home, "runs", "e1", "state.json"))
	want := "The 'no-such-model-xyz' model is not supported when using Codex with a ChatGPT account."
	if st["error_excerpt"] != want {
		t.Errorf("error_excerpt = %v, want %q", st["error_excerpt"], want)
	}
	if len(want) > 200 {
		t.Fatal("test expectation exceeds 200 characters")
	}
}

func TestStderrFallbackExcerpt(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_EXIT", "3").
		set("FAKECODEX_STDERR", "starting\n\x1b[31mfatal: boom\x1b[0m\n\n")
	r := s.run("exec", "--run-id", "f1", "q")
	if r.code != 3 {
		t.Fatalf("exit %d", r.code)
	}
	st := readJSONFile(t, filepath.Join(s.home, "runs", "f1", "state.json"))
	if st["error_excerpt"] != "fatal: boom" || st["outcome"] != "error" {
		t.Errorf("state = %v", st)
	}
	tail := readFile(t, filepath.Join(s.home, "runs", "f1", "stderr.tail"))
	if !strings.Contains(tail, "fatal: boom") {
		t.Errorf("stderr.tail = %q", tail)
	}
}

func TestExcerptTruncatedTo200(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_EXIT", "1").set("FAKECODEX_STDERR", strings.Repeat("é", 500))
	s.run("exec", "--run-id", "t1", "q")
	st := readJSONFile(t, filepath.Join(s.home, "runs", "t1", "state.json"))
	if got := []rune(st["error_excerpt"].(string)); len(got) != 200 {
		t.Errorf("excerpt has %d runes", len(got))
	}
}

func TestHookEnvReachesProvider(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("MY_HOOK_MARKER", "locked-42")
	rec := s.recordTo()
	if r := s.run("exec", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if got := readRecord(t, rec).Env["MY_HOOK_MARKER"]; got != "locked-42" {
		t.Errorf("provider saw MY_HOOK_MARKER=%q", got)
	}
}

func TestSessionIDDefaultsFromEnv(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("CLAUDE_CODE_SESSION_ID", "sess-9")
	s.run("exec", "--run-id", "s1", "q")
	req := readJSONFile(t, filepath.Join(s.home, "runs", "s1", "request.json"))
	if req["session_id"] != "sess-9" {
		t.Errorf("session_id = %v", req["session_id"])
	}
	s.run("exec", "--run-id", "s2", "--session-id", "flag-1", "q")
	req = readJSONFile(t, filepath.Join(s.home, "runs", "s2", "request.json"))
	if req["session_id"] != "flag-1" {
		t.Errorf("session_id = %v", req["session_id"])
	}
}

func TestFlagsInterspersedWithPrompt(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	r := s.run("exec", "question", "--json", "--run-id", "i1")
	if r.code != 0 || r.json(t)["run_id"] != "i1" {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestSecurityPermissions(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_STDERR", "warn\n")
	if r := s.run("exec", "--run-id", "p1", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	count := 0
	err := filepath.WalkDir(s.home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		count++
		want := os.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", path, info.Mode().Perm(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"prompt.md", "request.json", "output.md", "state.json", "stderr.tail"} {
		if !exists(filepath.Join(s.home, "runs", "p1", f)) {
			t.Errorf("missing %s", f)
		}
	}
	if count < 8 {
		t.Errorf("walked only %d entries", count)
	}
}

func TestOutputModeTightenedWhenProviderWritesLoose(t *testing.T) {
	// A real provider writes output.md with the process umask; msgate
	// tightens it to 0600.
	dir := t.TempDir()
	script := `#!/bin/sh
cat >/dev/null
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then umask 022; printf 'pong' > "$2"; fi
  shift
done
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newSandbox(t)
	s.path = dir + string(os.PathListSeparator) + "/usr/bin:/bin"
	if r := s.run("exec", "--run-id", "lm1", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	info, err := os.Stat(filepath.Join(s.home, "runs", "lm1", "output.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("output.md mode %v", info.Mode().Perm())
	}
}

func TestLargePromptIsDeliveredIntact(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()
	s.stdin = strings.Repeat("0123456789abcdef", 128*1024) // 2 MiB
	if r := s.run("exec", "--run-id", "big1", "-"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if got := readRecord(t, rec).Stdin; got != s.stdin {
		t.Errorf("fake received %d bytes, want %d", len(got), len(s.stdin))
	}
}

func TestStderrTailIsBounded(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_STDERR", strings.Repeat("a", 100000)+"END")
	s.run("exec", "--run-id", "tb1", "q")
	tail := readFile(t, filepath.Join(s.home, "runs", "tb1", "stderr.tail"))
	if len(tail) != 64*1024 || !strings.HasSuffix(tail, "aaaEND") {
		t.Errorf("stderr.tail has %d bytes, suffix %q", len(tail), tail[len(tail)-6:])
	}
}
