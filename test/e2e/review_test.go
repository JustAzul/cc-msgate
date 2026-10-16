package e2e

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewPlanPerTarget(t *testing.T) {
	cases := []struct {
		flags []string
		tail  []any
	}{
		{[]string{"--base", "main"}, []any{"review", "--base", "main"}},
		{[]string{"--uncommitted"}, []any{"review", "--uncommitted"}},
		{[]string{"--commit", "abc123"}, []any{"review", "--commit", "abc123"}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.flags, " "), func(t *testing.T) {
			s := newSandbox(t)
			repo := gitRepo(t)
			r := s.run(append([]string{"review", "--dry-run", "--run-id", "rp1", "--cwd", repo}, c.flags...)...)
			out := filepath.Join(s.home, "runs", "rp1", "output.md")
			want := append([]any{"codex", "exec", "-C", repo, "-s", "read-only", "--json", "-o", out}, c.tail...)
			plan := r.json(t)
			if r.code != 0 || !reflect.DeepEqual(plan["argv"], want) {
				t.Fatalf("exit %d argv\n got %v\nwant %v", r.code, plan["argv"], want)
			}
			if plan["stdin"] != "empty" || plan["command"] != "review" {
				t.Errorf("stdin=%v command=%v", plan["stdin"], plan["command"])
			}
			if exists(s.home) {
				t.Errorf("dry-run created %s", s.home)
			}
		})
	}
}

func TestReviewRunsWithEmptyStdin(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("review-ok"))
	rec := s.recordTo()
	repo := gitRepo(t)
	r := s.run("review", "--uncommitted", "--cwd", repo, "--run-id", "rv1", "--purpose", "code-review")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	out := filepath.Join(s.home, "runs", "rv1", "output.md")
	if r.stdout != out+"\n" {
		t.Errorf("stdout = %q", r.stdout)
	}
	fake := readRecord(t, rec)
	if fake.Stdin != "" {
		t.Errorf("provider stdin = %q, want empty", fake.Stdin)
	}
	if n := len(fake.Argv); n < 3 || !reflect.DeepEqual(fake.Argv[n-2:], []string{"review", "--uncommitted"}) {
		t.Errorf("argv = %v", fake.Argv)
	}
	tel := oneRecord(t, s.home)
	if tel["command"] != "review" || tel["usage"] != nil || tel["turn"] != float64(1) {
		t.Errorf("command=%v usage=%v turn=%v", tel["command"], tel["usage"], tel["turn"])
	}
	req := readJSONFile(t, filepath.Join(s.home, "runs", "rv1", "request.json"))
	if req["command"] != "review" {
		t.Errorf("request.command = %v", req["command"])
	}
}

func TestReviewRefusesAnyPrompt(t *testing.T) {
	for name, args := range map[string][]string{
		"positional": {"review", "--base", "main", "focus"},
		"stdin dash": {"review", "--base", "main", "-"},
		"file":       {"review", "--base", "main", "--prompt-file", "/nonexistent-p.md"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			rec := s.recordTo()
			r := s.run(args...)
			if r.code != 2 || !strings.Contains(r.stderr, "take no prompt") {
				t.Fatalf("exit %d stderr %q", r.code, r.stderr)
			}
			if exists(rec) || exists(filepath.Join(s.home, "runs")) {
				t.Error("provider ran or a run directory was created")
			}
		})
	}
}

func TestReviewNeedsExactlyOneTarget(t *testing.T) {
	for name, args := range map[string][]string{
		"none":              {"review"},
		"base and local":    {"review", "--base", "main", "--uncommitted"},
		"all three":         {"review", "--base", "main", "--uncommitted", "--commit", "abc"},
		"empty base":        {"review", "--base", ""},
		"base and commit":   {"review", "--base", "main", "--commit", "abc"},
		"uncommitted false": {"review", "--uncommitted=false"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			rec := s.recordTo()
			r := s.run(append(args, "--json")...)
			if r.code != 2 || r.json(t)["sdk_status"] != "usage_error" || !strings.Contains(r.stderr, "exactly one review target") {
				t.Fatalf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
			}
			if exists(rec) || exists(filepath.Join(s.home, "runs")) {
				t.Error("provider ran or a run directory was created")
			}
		})
	}
}
