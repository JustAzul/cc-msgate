package e2e

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReservedPassthrough(t *testing.T) {
	cases := [][]string{
		{"-o", "x"},
		{"--output-last-message", "x"},
		{"--json"},
		{"-C", "/x"},
		{"--cd", "/x"},
		{"-m", "m"},
		{"--model", "m"},
		{"--model=m"},
		{"-s", "read-only"},
		{"--sandbox", "read-only"},
		{"--sandbox=danger-full-access"},
		{"--dangerously-bypass-approvals-and-sandbox"},
		{"--approve-for-me"},
		{"--ephemeral"},
		{"-c", "model=x"},
		{"-c", "model_reasoning_effort=low"},
		{"-c", "sandbox_permissions=[]"},
		{"--config", "sandbox_mode=x"},
		{"-p", "work"},
		{"--profile", "work"},
		{"--profile=work"},
		{"-pwork"},
		{"-c", "profile=work"},
		{"--config", "profile=work"},
	}
	for _, flags := range cases {
		name := strings.Join(flags, " ")
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			rec := s.recordTo()
			r := s.run(append([]string{"exec", "q", "--"}, flags...)...)
			if r.code != 2 {
				t.Fatalf("exit %d, want 2 (stderr %s)", r.code, r.stderr)
			}
			if !strings.Contains(r.stderr, flags[0]) {
				t.Errorf("stderr does not name %q: %q", flags[0], r.stderr)
			}
			if len(flags) == 2 && flags[0] == "-c" && !strings.Contains(r.stderr, flags[1]) {
				t.Errorf("stderr does not name the override %q: %q", flags[1], r.stderr)
			}
			if exists(rec) {
				t.Error("provider was invoked")
			}
			if exists(filepath.Join(s.home, "runs")) {
				t.Error("run directory created")
			}
		})
	}
}

func TestReservedPassthroughAlsoRefusedOnDryRun(t *testing.T) {
	s := newSandbox(t)
	if r := s.run("exec", "--dry-run", "q", "--", "--ephemeral"); r.code != 2 {
		t.Errorf("exit %d", r.code)
	}
}

func TestAllowedPassthroughKeptAfterSDKFlags(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()
	cwd := gitRepo(t)
	r := s.run("exec", "--cwd", cwd, "--run-id", "ap1", "q", "--", "-c", "features.web_search=true", "--add-dir", "/tmp/x")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	out := filepath.Join(s.home, "runs", "ap1", "output.md")
	want := []string{"exec", "-C", cwd, "-s", "read-only", "--json", "-o", out, "-c", "features.web_search=true", "--add-dir", "/tmp/x", "-"}
	if got := readRecord(t, rec).Argv[1:]; !reflect.DeepEqual(got, want) {
		t.Errorf("argv\n got %v\nwant %v", got, want)
	}
	req := readJSONFile(t, filepath.Join(s.home, "runs", "ap1", "request.json"))
	if !reflect.DeepEqual(req["passthrough"], []any{"-c", "features.web_search=true", "--add-dir", "/tmp/x"}) {
		t.Errorf("request.passthrough = %v", req["passthrough"])
	}
}

func TestDangerSandboxRefused(t *testing.T) {
	s := newSandbox(t)
	rec := s.recordTo()
	r := s.run("exec", "--sandbox", "danger-full-access", "q")
	if r.code != 2 || !strings.Contains(r.stderr, "danger-full-access") {
		t.Errorf("exit %d stderr %q", r.code, r.stderr)
	}
	if exists(rec) || exists(filepath.Join(s.home, "runs")) {
		t.Error("spawned or admitted despite the refusal")
	}
}

// onlyJSONObject asserts stdout is exactly one JSON object and returns it.
func onlyJSONObject(t *testing.T, r result) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\nstdout %q stderr %q", err, r.stdout, r.stderr)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON value: %q", r.stdout)
	}
	return m
}

func TestJSONOnEveryUsageErrorPath(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"exec", "--json", "--no-such-flag", "q"}},
		{"unknown flag before json", []string{"exec", "--no-such-flag", "--json", "q"}},
		{"no prompt", []string{"exec", "--json"}},
		{"two prompt sources", []string{"exec", "--json", "--prompt-file", "p.md", "q"}},
		{"missing prompt file", []string{"exec", "--json", "--prompt-file", "/no/such/file"}},
		{"reserved flag", []string{"exec", "--json", "q", "--", "--ephemeral"}},
		{"reserved override", []string{"exec", "--json", "q", "--", "-c", "model=x"}},
		{"danger sandbox", []string{"exec", "--json", "--sandbox", "danger-full-access", "q"}},
		{"bad run id", []string{"exec", "--json", "--run-id", "a b", "q"}},
		{"unknown provider", []string{"exec", "--json", "--provider", "nope", "q"}},
		{"missing cwd", []string{"exec", "--json", "--cwd", "/no/such/dir", "q"}},
		{"bad attr", []string{"exec", "--json", "--attr", "novalue", "q"}},
		{"bad attr-json", []string{"exec", "--json", "--attr-json", "k={nope", "q"}},
		{"unknown command", []string{"nope", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t)
			r := s.run(c.args...)
			if r.code != 2 {
				t.Fatalf("exit %d, want 2 (stderr %s)", r.code, r.stderr)
			}
			m := onlyJSONObject(t, r)
			if m["sdk_status"] != "usage_error" || m["exit_code"] != float64(2) {
				t.Errorf("json = %v", m)
			}
			if msg, _ := m["error"].(string); msg == "" {
				t.Errorf("json has no error message: %v", m)
			}
		})
	}
}

func TestJSONNotPrintedWithoutJSONFlag(t *testing.T) {
	s := newSandbox(t)
	if r := s.run("exec", "--no-such-flag", "q"); r.stdout != "" {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestJSONHelpIsStillOneObject(t *testing.T) {
	r := newSandbox(t).run("exec", "--json", "-h")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if m := onlyJSONObject(t, r); m["sdk_status"] != "ok" || m["exit_code"] != float64(0) {
		t.Errorf("json = %v", m)
	}
}
