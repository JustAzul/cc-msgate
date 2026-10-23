package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// leanOverrides are the -c pairs the Codex adapter adds at lean, in order,
// given the instructions file msgate keeps in its home.
func leanOverrides(home string) []string {
	return []string{
		"-c", "model_instructions_file=" + filepath.Join(home, "harness", "instructions.md"),
		"-c", "skills.include_instructions=false",
		"-c", "include_collaboration_mode_instructions=false",
		"-c", "include_apps_instructions=false",
		"-c", "features.plugins=false",
		"-c", "agents.enabled=false",
	}
}

// hasRun reports whether argv holds sub as consecutive elements.
func hasRun(argv, sub []string) bool {
	for i := 0; i+len(sub) <= len(argv); i++ {
		if slices.Equal(argv[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func TestExecHarnessLeanRunsCodexWithTheLeanOverridesAndRecordsTheLevel(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()

	r := s.run("exec", "--json", "--run-id", "h1", "--harness", "lean", "--cwd", gitRepo(t), "q")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	argv := readRecord(t, rec).Argv
	if !hasRun(argv, leanOverrides(s.home)) {
		t.Errorf("argv %q lacks the lean overrides %q", argv, leanOverrides(s.home))
	}
	if got := readFile(t, filepath.Join(s.home, "harness", "instructions.md")); got != "Follow the user's instructions. Be concise.\n" {
		t.Errorf("instructions file = %q", got)
	}
	if got := readJSONFile(t, filepath.Join(s.home, "runs", "h1", "request.json"))["harness"]; got != "lean" {
		t.Errorf("request.json harness = %v, want lean", got)
	}
	conv := conversationRecord(t, s.home, r.json(t)["conversation_id"].(string))
	if got := conv["defaults"].(map[string]any)["harness"]; got != "lean" {
		t.Errorf("conversation defaults.harness = %v, want lean", got)
	}
}

func TestExecWithoutHarnessRunsCodexAsBefore(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()

	r := s.run("exec", "--run-id", "h1", "--cwd", gitRepo(t), "q")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	for _, arg := range readRecord(t, rec).Argv {
		if arg == "-c" {
			t.Errorf("argv %q carries a config override", readRecord(t, rec).Argv)
		}
	}
	if got := readJSONFile(t, filepath.Join(s.home, "runs", "h1", "request.json"))["harness"]; got != "full" {
		t.Errorf("request.json harness = %v, want full", got)
	}
	if _, err := os.Stat(filepath.Join(s.home, "harness")); !os.IsNotExist(err) {
		t.Errorf("a full run touched the harness directory (stat error %v)", err)
	}
}

func TestSendKeepsTheConversationsHarnessAndAFlagAppliesToOneTurn(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "t1", "--harness", "lean", "--cwd", gitRepo(t))
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	rec := s.recordTo()
	lean := leanOverrides(s.home)

	for _, turn := range []struct {
		runID string
		args  []string
		lean  bool
	}{
		{"t2", nil, true},
		{"t3", []string{"--harness", "full"}, false},
		{"t4", nil, true},
	} {
		r := s.run(append(append([]string{"send", "--run-id", turn.runID}, turn.args...), conv, "again")...)
		if r.code != 0 {
			t.Fatalf("send %s: exit %d stderr %s", turn.runID, r.code, r.stderr)
		}
		argv := readRecord(t, rec).Argv
		if hasRun(argv, lean[:2]) != turn.lean {
			t.Errorf("send %s: lean overrides present = %v, want %v (argv %q)", turn.runID, !turn.lean, turn.lean, argv)
		}
		if !hasRun(argv, []string{"resume", resumeThread, "-"}) {
			t.Errorf("send %s: argv %q does not resume the thread", turn.runID, argv)
		}
	}
}

func TestExecHarnessBareTurnsOffEveryMCPServerCodexLists(t *testing.T) {
	list := filepath.Join(t.TempDir(), "mcp-list.json")
	if err := os.WriteFile(list, []byte(`[{"name":"fixture-one","enabled":true},{"name":"fixture.two","enabled":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_MCP_LIST", list)
	rec := s.recordTo()

	r := s.run("exec", "--harness", "bare", "--cwd", gitRepo(t), "q")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	argv := readRecord(t, rec).Argv
	for _, want := range [][]string{
		{"-c", "features.shell_tool=false"},
		{"-c", `mcp_servers={"fixture-one"={enabled=false},"fixture.two"={enabled=false}}`},
	} {
		if !hasRun(argv, want) {
			t.Errorf("argv %q lacks %q", argv, want)
		}
	}
}

func TestHarnessRefusalsExitWithUsageAndStartNoRun(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	cwd := gitRepo(t)
	cases := []struct {
		name string
		args []string
	}{
		{"unknown level", []string{"exec", "--harness", "other", "--cwd", cwd, "--run-id", "x1", "q"}},
		{"review at bare", []string{"review", "--harness", "bare", "--uncommitted", "--cwd", cwd, "--run-id", "x2"}},
		{"override of a lean key at lean", []string{"exec", "--harness", "lean", "--cwd", cwd, "--run-id", "x3", "q", "--", "-c", "features.plugins=true"}},
		{"feature flag of a bare key at bare", []string{"exec", "--harness", "bare", "--cwd", cwd, "--run-id", "x4", "q", "--", "--enable", "shell_tool"}},
		{"table override above a lean key at lean", []string{"exec", "--harness", "lean", "--cwd", cwd, "--run-id", "x5", "q", "--", "-c", "features={plugins=true}"}},
	}
	for _, c := range cases {
		if r := s.run(c.args...); r.code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %s)", c.name, r.code, r.stderr)
		}
	}
	for _, id := range []string{"x1", "x2", "x3", "x4", "x5"} {
		if exists(filepath.Join(s.home, "runs", id)) {
			t.Errorf("the refused run %s left a run directory", id)
		}
	}
}

func TestHarnessKeyPassesThroughAtFull(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	rec := s.recordTo()

	r := s.run("exec", "--cwd", gitRepo(t), "q", "--", "-c", "features.plugins=true")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if argv := readRecord(t, rec).Argv; !hasRun(argv, []string{"-c", "features.plugins=true"}) {
		t.Errorf("argv %q lost the passthrough", argv)
	}
}

func TestHarnessReviewAtLeanRunsWithTheLeanOverrides(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("review-ok"))
	rec := s.recordTo()

	r := s.run("review", "--harness", "lean", "--uncommitted", "--cwd", gitRepo(t), "--run-id", "rv1")

	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if argv := readRecord(t, rec).Argv; !hasRun(argv, leanOverrides(s.home)) || !hasRun(argv, []string{"review", "--uncommitted"}) {
		t.Errorf("argv %q lacks the lean overrides before the review tail", argv)
	}
}

func TestDryRunOfAHarnessShowsTheOverridesAndWritesNothing(t *testing.T) {
	s := newSandbox(t)

	r := s.run("exec", "--dry-run", "--harness", "lean", "--cwd", gitRepo(t), "q")

	argv := dryArgv(t, r)
	strs := make([]string, len(argv))
	for i, a := range argv {
		strs[i] = a.(string)
	}
	if !hasRun(strs, leanOverrides(s.home)) {
		t.Errorf("planned argv %q lacks the lean overrides", strs)
	}
	for _, name := range []string{"harness", "runs", "conversations"} {
		if exists(filepath.Join(s.home, name)) {
			t.Errorf("a dry run created %s", name)
		}
	}
}

func TestExecHarnessBareWithoutCodexExitsProviderMissingLikeFull(t *testing.T) {
	s := newSandbox(t).withoutProvider()

	r := s.run("exec", "--json", "--run-id", "hb1", "--harness", "bare", "--cwd", gitRepo(t), "q")

	if r.code != 127 {
		t.Fatalf("exit %d, want 127; stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if m := r.json(t); m["sdk_status"] != "provider_missing" || m["exit_code"] != float64(127) {
		t.Errorf("error object = %v, want provider_missing with exit_code 127", m)
	}
}
