package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fr31Fields is the exact key set of a run record.
var fr31Fields = []string{
	"v", "kind", "run_id", "ts", "provider", "provider_version", "command", "purpose",
	"model", "model_source", "effort", "effort_source", "model_used", "effort_used", "sandbox", "source", "session_id",
	"conversation_id", "turn", "cwd", "background", "exit_code", "outcome", "duration_ms",
	"timeout_s", "output_file", "output_bytes", "error_excerpt", "usage", "provider_session_id", "attrs",
}

func telemetryDir(home string) string { return filepath.Join(home, "telemetry") }

// telemetryRecords returns every parsed line of every month file, in order.
func telemetryRecords(t *testing.T, home string) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(telemetryDir(home), "*.jsonl"))
	sort.Strings(files)
	var out []map[string]any
	for _, f := range files {
		for _, line := range strings.Split(strings.TrimSuffix(readFile(t, f), "\n"), "\n") {
			var m map[string]any
			if err := jsonUnmarshal(line, &m); err != nil {
				t.Fatalf("%s: unparseable line %q: %v", f, line, err)
			}
			out = append(out, m)
		}
	}
	return out
}

func oneRecord(t *testing.T, home string) map[string]any {
	t.Helper()
	recs := telemetryRecords(t, home)
	if len(recs) != 1 {
		t.Fatalf("telemetry has %d records, want exactly 1: %v", len(recs), recs)
	}
	return recs[0]
}

func currentMonthFile(home string) string {
	return filepath.Join(telemetryDir(home), time.Now().UTC().Format("2006-01")+".jsonl")
}

func TestTelemetryRecordForForegroundRun(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).
		set("FAKECODEX_VERSION", "codex-cli 9.9.9").set("CLAUDE_CODE_SESSION_ID", "sess-1")
	cwd := gitRepo(t)
	r := s.run(append([]string{"exec"}, secondOpinionFlags("--cwd", cwd, "--run-id", "tr1", "--source", "hook-x", "q")...)...)
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	rec := oneRecord(t, s.home)
	if !exists(currentMonthFile(s.home)) {
		t.Errorf("record not in %s", currentMonthFile(s.home))
	}
	got := make([]string, 0, len(rec))
	for k := range rec {
		got = append(got, k)
	}
	sort.Strings(got)
	want := append([]string(nil), fr31Fields...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("record keys\n got %v\nwant %v", got, want)
	}
	state := readJSONFile(t, filepath.Join(s.home, "runs", "tr1", "state.json"))
	out := filepath.Join(s.home, "runs", "tr1", "output.md")
	for k, w := range map[string]any{
		"v": float64(1), "kind": "run", "run_id": "tr1", "provider": "codex",
		"provider_version": "codex-cli 9.9.9", "command": "exec", "purpose": "second-opinion",
		"model": "gpt-6.1-sol", "model_source": "profile", "effort": "high", "effort_source": "profile",
		"sandbox": "read-only", "source": "hook-x", "session_id": "sess-1",
		"conversation_id": state["conversation_id"], "turn": float64(1), "cwd": cwd, "background": false,
		"exit_code": float64(0), "outcome": "ok", "timeout_s": nil, "output_file": out,
		"output_bytes": float64(4), "error_excerpt": nil, "provider_session_id": execOKThread,
		"attrs": map[string]any{},
		"usage": map[string]any{
			"input_tokens": float64(18573), "cached_input_tokens": float64(11008),
			"cache_write_input_tokens": float64(0), "output_tokens": float64(5), "reasoning_output_tokens": float64(0),
		},
	} {
		if !reflect.DeepEqual(rec[k], w) {
			t.Errorf("record.%s = %#v, want %#v", k, rec[k], w)
		}
	}
	if ts, _ := rec["ts"].(string); !strings.HasSuffix(ts, "Z") || ts != state["started_at"] {
		t.Errorf("ts = %v, state.started_at = %v", rec["ts"], state["started_at"])
	}
	if d, ok := rec["duration_ms"].(float64); !ok || d < 0 {
		t.Errorf("duration_ms = %v", rec["duration_ms"])
	}
}

func TestTelemetryAttrsAtDispatchAndNamespacedKeys(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	r := s.run("exec", "--attr", "ticket=2056", "--attr-json", `review.findings={"total":1}`, "q")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	want := map[string]any{"ticket": "2056", "review.findings": map[string]any{"total": float64(1)}}
	attrs := oneRecord(t, s.home)["attrs"]
	if !reflect.DeepEqual(attrs, want) {
		t.Errorf("attrs = %v, want %v", attrs, want)
	}
	if _, nested := attrs.(map[string]any)["review"]; nested {
		t.Error("review.findings was nested as review -> findings")
	}
}

func TestTelemetryOneRecordForProviderError(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("error-400")).set("FAKECODEX_EXIT", "1")
	r := s.run("exec", "--run-id", "te1", "--model", "no-such-model-xyz", "q")
	if r.code != 1 {
		t.Fatalf("exit %d", r.code)
	}
	rec := oneRecord(t, s.home)
	want := "The 'no-such-model-xyz' model is not supported when using Codex with a ChatGPT account."
	for k, w := range map[string]any{
		"exit_code": float64(1), "outcome": "error", "error_excerpt": want, "usage": nil,
		"model": "no-such-model-xyz", "model_source": "flag", "output_bytes": float64(0),
		"provider_session_id": "00000000-0000-4000-8000-000000000004",
	} {
		if !reflect.DeepEqual(rec[k], w) {
			t.Errorf("record.%s = %#v, want %#v", k, rec[k], w)
		}
	}
}

func TestTelemetryOneRecordForProviderMissing(t *testing.T) {
	s := newSandbox(t).withoutProvider()
	r := s.run("exec", "--run-id", "tm1", "q")
	if r.code != 127 {
		t.Fatalf("exit %d", r.code)
	}
	rec := oneRecord(t, s.home)
	if ex, _ := rec["error_excerpt"].(string); !strings.Contains(ex, "codex") {
		t.Errorf("error_excerpt = %v", rec["error_excerpt"])
	}
	for k, w := range map[string]any{
		"exit_code": float64(127), "outcome": "error", "provider_version": nil, "usage": nil,
		"provider_session_id": nil, "run_id": "tm1",
	} {
		if !reflect.DeepEqual(rec[k], w) {
			t.Errorf("record.%s = %#v, want %#v", k, rec[k], w)
		}
	}
}

func TestTelemetryOneRecordForEmptyOutput(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_OUTPUT", "")
	if r := s.run("exec", "q"); r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	rec := oneRecord(t, s.home)
	if rec["outcome"] != "empty" || rec["exit_code"] != float64(0) || rec["output_bytes"] != float64(0) {
		t.Errorf("record = %v", rec)
	}
}

func TestDryRunWritesNoTelemetry(t *testing.T) {
	s := newSandbox(t)
	s.run("exec", "--dry-run", "q")
	if exists(s.home) {
		t.Error("dry-run created the home")
	}
}

func TestPromptNeverInTelemetry(t *testing.T) {
	const marker = "UNIQUE-MARKER-7f3a91"
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	if r := s.run("exec", "--run-id", "pm1", "--attr", "note=fine", "do not leak "+marker); r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	files, _ := filepath.Glob(filepath.Join(telemetryDir(s.home), "*"))
	if len(files) == 0 {
		t.Fatal("no telemetry written")
	}
	for _, f := range files {
		if strings.Contains(readFile(t, f), marker) {
			t.Errorf("%s contains the prompt", f)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(s.home, "runs", "pm1", "prompt.md")), marker) {
		t.Error("prompt.md lost the prompt")
	}
}

func TestProviderVersionFailureIsNull(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo broken >&2; exit 1; fi\nexec " +
		filepath.Join(fakeDir, "codex") + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	s.path = dir + string(os.PathListSeparator) + "/usr/bin:/bin"
	r := s.run("exec", "--json", "q")
	if r.code != 0 || r.json(t)["outcome"] != "ok" {
		t.Fatalf("version failure affected the run: exit %d stdout %s stderr %s", r.code, r.stdout, r.stderr)
	}
	if rec := oneRecord(t, s.home); rec["provider_version"] != nil {
		t.Errorf("provider_version = %v, want null", rec["provider_version"])
	}
}

func TestTornTailGetsANewLine(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	if err := os.MkdirAll(telemetryDir(s.home), 0o700); err != nil {
		t.Fatal(err)
	}
	file := currentMonthFile(s.home)
	if err := os.WriteFile(file, []byte(`{"v":1,"kind":"run","run_`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := s.run("exec", "--run-id", "tt1", "q"); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(readFile(t, file), "\n"), "\n")
	if len(lines) != 2 || lines[0] != `{"v":1,"kind":"run","run_` {
		t.Fatalf("lines = %q", lines)
	}
	var m map[string]any
	if err := jsonUnmarshal(lines[1], &m); err != nil || m["run_id"] != "tt1" {
		t.Errorf("new record = %q (%v)", lines[1], err)
	}
}

func warningLines(stderr string) []string {
	var out []string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.Contains(l, "warning") {
			out = append(out, l)
		}
	}
	return out
}

func TestTelemetryUnwritable(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_EXIT", "3")
	dir := telemetryDir(s.home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	r := s.run("exec", "--run-id", "uw1", "q")
	if r.code != 3 {
		t.Fatalf("exit %d, want the provider's 3 (stderr %s)", r.code, r.stderr)
	}
	if w := warningLines(r.stderr); len(w) != 1 || !strings.Contains(w[0], "telemetry") {
		t.Errorf("warnings = %q (stderr %q)", w, r.stderr)
	}
	if got := readFile(t, filepath.Join(s.home, "runs", "uw1", "output.md")); got != "pong" {
		t.Errorf("output.md = %q", got)
	}
	if st := readJSONFile(t, filepath.Join(s.home, "runs", "uw1", "state.json")); st["state"] != "failed" || st["exit_code"] != float64(3) {
		t.Errorf("state = %v", st)
	}
	if r.lastLine() != filepath.Join(s.home, "runs", "uw1", "output.md") {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestTelemetryLockHeld(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_EXIT", "3")
	dir := telemetryDir(s.home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r := s.run("exec", "--run-id", "lk1", "q")
	elapsed := time.Since(start)
	if r.code != 3 {
		t.Fatalf("exit %d, want the provider's 3 (stderr %s)", r.code, r.stderr)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Errorf("run took %v, want about the 5 s lock wait", elapsed)
	}
	if w := warningLines(r.stderr); len(w) != 1 || !strings.Contains(w[0], "telemetry") {
		t.Errorf("warnings = %q", w)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Errorf("a record was written while the lock was held: %v", files)
	}
	if got := readFile(t, filepath.Join(s.home, "runs", "lk1", "output.md")); got != "pong" {
		t.Errorf("output.md = %q", got)
	}
}
