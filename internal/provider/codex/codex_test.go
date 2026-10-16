package codex_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

func gitDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBuildPlanExecArgvOrder(t *testing.T) {
	cwd := gitDir(t)
	p := codex.New()
	plan, err := p.BuildPlan(provider.Request{
		Command: "exec", Cwd: cwd, Model: "m", Effort: "high", Sandbox: "read-only",
		OutputPath: "/out/output.md", Passthrough: []string{"--add-dir", "/tmp/x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "exec", "-C", cwd, "-s", "read-only", "-m", "m",
		"-c", "model_reasoning_effort=high", "--json", "-o", "/out/output.md",
		"--add-dir", "/tmp/x", "-"}
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Fatalf("argv\n got %q\nwant %q", plan.Argv, want)
	}
	if plan.Stdin != provider.StdinPrompt || plan.Dir != cwd {
		t.Fatalf("stdin=%q dir=%q", plan.Stdin, plan.Dir)
	}
}

func TestBuildPlanReviewTails(t *testing.T) {
	cwd := gitDir(t)
	cases := []struct {
		target, ref string
		tail        []string
	}{
		{"base", "main", []string{"review", "--base", "main"}},
		{"uncommitted", "", []string{"review", "--uncommitted"}},
		{"commit", "abc123", []string{"review", "--commit", "abc123"}},
	}
	for _, c := range cases {
		plan, err := codex.New().BuildPlan(provider.Request{
			Command: "review", Cwd: cwd, Model: "m", OutputPath: "/o", Passthrough: []string{"--x"},
			ReviewTarget: c.target, ReviewRef: c.ref,
		})
		if err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		want := append([]string{"codex", "exec", "-C", cwd, "-m", "m", "--json", "-o", "/o", "--x"}, c.tail...)
		if !reflect.DeepEqual(plan.Argv, want) {
			t.Errorf("%s argv\n got %q\nwant %q", c.target, plan.Argv, want)
		}
		if plan.Stdin != provider.StdinEmpty {
			t.Errorf("%s stdin = %q, want empty", c.target, plan.Stdin)
		}
	}
}

func TestBuildPlanReviewNeedsATarget(t *testing.T) {
	if _, err := codex.New().BuildPlan(provider.Request{Command: "review", Cwd: t.TempDir(), OutputPath: "/o"}); err == nil {
		t.Error("review without a target built a plan")
	}
	if _, err := codex.New().BuildPlan(provider.Request{Command: "review", Cwd: t.TempDir(), OutputPath: "/o", ReviewTarget: "base"}); err == nil {
		t.Error("review --base without a ref built a plan")
	}
}

func TestBuildPlanResumeTail(t *testing.T) {
	cwd := gitDir(t)
	plan, err := codex.New().BuildPlan(provider.Request{
		Command: "resume", Cwd: cwd, Effort: "low", SessionID: "T-1", OutputPath: "/o", Passthrough: []string{"--x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "exec", "-C", cwd, "-c", "model_reasoning_effort=low", "--json", "-o", "/o", "--x", "resume", "T-1", "-"}
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Errorf("argv\n got %q\nwant %q", plan.Argv, want)
	}
	if plan.Stdin != provider.StdinPrompt {
		t.Errorf("stdin = %q, want prompt", plan.Stdin)
	}
	if _, err := codex.New().BuildPlan(provider.Request{Command: "resume", Cwd: cwd, OutputPath: "/o"}); err == nil {
		t.Error("resume without a session id built a plan")
	}
}

func TestBuildPlanOmitsUnsetFlags(t *testing.T) {
	cwd := gitDir(t)
	plan, err := codex.New().BuildPlan(provider.Request{Command: "exec", Cwd: cwd, OutputPath: "/o"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "exec", "-C", cwd, "--json", "-o", "/o", "-"}
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Fatalf("argv\n got %q\nwant %q", plan.Argv, want)
	}
}

func TestBuildPlanSkipGitRepoCheckOutsideGit(t *testing.T) {
	outside := t.TempDir()
	plan, _ := codex.New().BuildPlan(provider.Request{Command: "exec", Cwd: outside, OutputPath: "/o", Passthrough: []string{"--x"}})
	got := strings.Join(plan.Argv, " ")
	if !strings.Contains(got, "--skip-git-repo-check") {
		t.Fatalf("missing flag: %s", got)
	}
	if !strings.Contains(got, "-o /o --skip-git-repo-check --x -") {
		t.Fatalf("flag must follow SDK flags and precede passthrough: %s", got)
	}
	// nested directory inside a repo is still inside a work tree
	repo := gitDir(t)
	nested := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, _ = codex.New().BuildPlan(provider.Request{Command: "exec", Cwd: nested, OutputPath: "/o"})
	if strings.Contains(strings.Join(plan.Argv, " "), "--skip-git-repo-check") {
		t.Fatalf("flag added inside git work tree: %q", plan.Argv)
	}
}

func TestBuildPlanNeverEphemeral(t *testing.T) {
	plan, _ := codex.New().BuildPlan(provider.Request{Command: "exec", Cwd: t.TempDir(), OutputPath: "/o"})
	for _, a := range plan.Argv {
		if a == "--ephemeral" {
			t.Fatal("--ephemeral present")
		}
	}
}

func TestCapabilities(t *testing.T) {
	c := codex.New().Capabilities()
	if !c.Supports("exec") || !c.SupportsSandbox("read-only") || !c.SupportsSandbox("workspace-write") || c.SupportsSandbox("danger-full-access") {
		t.Fatalf("capabilities: %+v", c)
	}
}

func TestParseEvent(t *testing.T) {
	p := codex.New()
	cases := []struct {
		name, line string
		ok         bool
		want       provider.Event
	}{
		{"thread started", `{"type":"thread.started","thread_id":"T1"}`, true, provider.Event{SessionID: "T1"}},
		{"usage", `{"type":"turn.completed","usage":{"input_tokens":3,"cached_input_tokens":2,"cache_write_input_tokens":1,"output_tokens":5,"reasoning_output_tokens":4}}`, true,
			provider.Event{Usage: &provider.Usage{InputTokens: 3, CachedInputTokens: 2, CacheWriteInputTokens: 1, OutputTokens: 5, ReasoningOutputTokens: 4}}},
		{"error plain", `{"type":"error","message":"boom"}`, true, provider.Event{ErrorMsg: "boom"}},
		{"error wrapped", `{"type":"error","message":"{\"type\":\"error\",\"error\":{\"message\":\"inner\"}}"}`, true, provider.Event{ErrorMsg: "inner"}},
		{"turn failed wrapped", `{"type":"turn.failed","error":{"message":"{\"error\":{\"message\":\"inner2\"}}"}}`, true, provider.Event{ErrorMsg: "inner2"}},
		{"turn failed plain", `{"type":"turn.failed","error":{"message":"plain"}}`, true, provider.Event{ErrorMsg: "plain"}},
		{"json message without inner", `{"type":"error","message":"{\"a\":1}"}`, true, provider.Event{ErrorMsg: `{"a":1}`}},
		{"item error ignored", `{"type":"item.completed","item":{"type":"error","message":"warn"}}`, true, provider.Event{}},
		{"command started, quoted", `{"type":"item.started","item":{"type":"command_execution","command":"/bin/sh -lc 'git status --short'","status":"in_progress"}}`, true,
			provider.Event{Progress: &provider.Progress{Kind: "command", Text: "git status --short"}}},
		{"command started, bare", `{"type":"item.started","item":{"type":"command_execution","command":"/usr/bin/zsh -lc ls","status":"in_progress"}}`, true,
			provider.Event{Progress: &provider.Progress{Kind: "command", Text: "ls"}}},
		{"command started, no shell", `{"type":"item.started","item":{"type":"command_execution","command":"rg -n foo"}}`, true,
			provider.Event{Progress: &provider.Progress{Kind: "command", Text: "rg -n foo"}}},
		{"command completed shows nothing new", `{"type":"item.completed","item":{"type":"command_execution","command":"/bin/sh -lc ls","exit_code":0}}`, true, provider.Event{}},
		{"message completed", `{"type":"item.completed","item":{"type":"agent_message","text":"I'll list the files with ` + "`ls`" + `."}}`, true,
			provider.Event{Progress: &provider.Progress{Kind: "message", Text: "I'll list the files with `ls`."}}},
		{"reasoning completed", `{"type":"item.completed","item":{"type":"reasoning","text":"Checking the lock order"}}`, true,
			provider.Event{Progress: &provider.Progress{Kind: "reasoning", Text: "Checking the lock order"}}},
		{"empty message", `{"type":"item.completed","item":{"type":"agent_message","text":""}}`, true, provider.Event{}},
		{"unknown type", `{"type":"brand.new"}`, true, provider.Event{}},
		{"garbage", `not json`, false, provider.Event{}},
		{"non object", `42`, false, provider.Event{}},
	}
	for _, c := range cases {
		got, ok := p.ParseEvent([]byte(c.line))
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got (%+v, %v) want (%+v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestReservedFlag(t *testing.T) {
	p := codex.New()
	reserved := []string{
		"-o", "--output-last-message", "--output-last-message=x", "--json", "-C", "--cd", "--cd=/x", "-C/x",
		"-m", "--model", "--model=m", "-s", "--sandbox", "--sandbox=read-only", "-s=read-only",
		"--dangerously-bypass-approvals-and-sandbox", "--dangerously-anything", "--yolo",
		"--approve-for-me", "--ephemeral",
		"-c model=x", "-c model_reasoning_effort=low", "-c sandbox_permissions=[]", "-c sandbox_mode=x",
		"--config model=x", "--config=model=x", "-cmodel=x", "-c=model=x", "-c model = x",
		"-p", "--profile", "--profile=work", "-pwork", "-p=work",
		"-c profile=work", "--config profile=work", "--config=profile=work", "-cprofile=work", "-c=profile=work", "-c profile = work",
	}
	for _, a := range reserved {
		if !p.ReservedFlag(a) {
			t.Errorf("%q should be reserved", a)
		}
	}
	allowed := []string{
		"-c features.web_search=true", "-c", "features.web_search=true", "--add-dir", "/tmp/x",
		"--add-dir /tmp/x", "-c model_providers.x.name=y", "-c models=1", "-i", "--skip-git-repo-check",
		"-c profiler=1",
	}
	for _, a := range allowed {
		if p.ReservedFlag(a) {
			t.Errorf("%q should be allowed", a)
		}
	}
}

func TestReviewUsageZeroBecomesNull(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/codex/review-ok.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var usage *provider.Usage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if ev, ok := codex.New().ParseEvent([]byte(line)); ok && ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if usage == nil || !usage.IsZero() {
		t.Fatalf("fixture usage = %+v, want an all-zero usage object", usage)
	}
	if got := provider.NormalizeUsage("review", usage); got != nil {
		t.Errorf("review with zero usage = %+v, want nil", got)
	}
	if got := provider.NormalizeUsage("exec", usage); got == nil || !got.IsZero() {
		t.Errorf("exec keeps its zero usage object, got %+v", got)
	}
	real := &provider.Usage{OutputTokens: 7}
	if got := provider.NormalizeUsage("review", real); got != real {
		t.Errorf("review with real usage = %+v", got)
	}
	if got := provider.NormalizeUsage("review", nil); got != nil {
		t.Errorf("nil stays nil, got %+v", got)
	}
}

func TestCodexCachesPromptsImplicitly(t *testing.T) {
	caching, ok := codex.New().(provider.ImplicitCaching)
	if !ok || !caching.ImplicitCacheWrites() {
		t.Fatalf("codex ImplicitCaching = %v (implemented %v), want true", caching, ok)
	}
	for name, want := range map[string]bool{"codex": true, "anthropic": false, "": false} {
		if got := provider.HasImplicitCacheWrites(name); got != want {
			t.Errorf("HasImplicitCacheWrites(%q) = %v, want %v", name, got, want)
		}
	}
}
