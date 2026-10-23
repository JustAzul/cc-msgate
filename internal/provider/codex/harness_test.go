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

const harnessInstructions = "/home/msgate/harness/instructions.md"

// harnessRequest is an exec request at level with the instructions file the
// CLI provides for any level but full.
func harnessRequest(cwd, level string) provider.Request {
	req := provider.Request{
		Command: "exec", Cwd: cwd, Effort: "low", Sandbox: "read-only", OutputPath: "/out/output.md", Harness: level,
	}
	if level != "" && level != provider.HarnessFull {
		req.HarnessInstructions = harnessInstructions
	}
	return req
}

// leanArgv is the config overrides lean adds after the shared flags.
var leanArgv = []string{
	"-c", "model_instructions_file=" + harnessInstructions,
	"-c", "skills.include_instructions=false",
	"-c", "include_collaboration_mode_instructions=false",
	"-c", "include_apps_instructions=false",
	"-c", "features.plugins=false",
	"-c", "agents.enabled=false",
}

func argvAround(cwd string, overrides []string) []string {
	argv := []string{"codex", "exec", "-C", cwd, "-s", "read-only", "-c", "model_reasoning_effort=low"}
	argv = append(argv, overrides...)
	return append(argv, "--json", "-o", "/out/output.md", "-")
}

func TestCapabilitiesListEveryHarnessLevel(t *testing.T) {
	got := codex.New().Capabilities().Harnesses
	if want := []string{"full", "lean", "bare"}; !reflect.DeepEqual(got, want) {
		t.Errorf("harnesses = %q, want %q", got, want)
	}
}

func TestBuildPlanFullAddsNothing(t *testing.T) {
	cwd := gitDir(t)
	for _, level := range []string{"", "full"} {
		plan, err := codex.New().BuildPlan(harnessRequest(cwd, level))
		if err != nil {
			t.Fatal(err)
		}
		if want := argvAround(cwd, nil); !reflect.DeepEqual(plan.Argv, want) {
			t.Errorf("level %q argv\n got %q\nwant %q", level, plan.Argv, want)
		}
	}
}

func TestBuildPlanLeanReplacesTheInstructionLayersAndKeepsTheTools(t *testing.T) {
	cwd := gitDir(t)

	plan, err := codex.New().BuildPlan(harnessRequest(cwd, "lean"))

	if err != nil {
		t.Fatal(err)
	}
	if want := argvAround(cwd, leanArgv); !reflect.DeepEqual(plan.Argv, want) {
		t.Errorf("argv\n got %q\nwant %q", plan.Argv, want)
	}
}

// bareArgv is what bare adds to lean's overrides: the environment note, every
// tool toggle and web search, before any MCP server.
var bareArgv = []string{
	"-c", "include_environment_context=false",
	"-c", "features.shell_tool=false",
	"-c", "features.apps=false",
	"-c", "features.browser_use=false",
	"-c", "features.computer_use=false",
	"-c", "features.image_generation=false",
	"-c", "features.goals=false",
	"-c", "features.skill_search=false",
	"-c", "features.sleep_tool=false",
	"-c", "features.tool_suggest=false",
	"-c", "features.hooks=false",
	"-c", "tools.view_image=false",
	"-c", "web_search=disabled",
}

// fakeMCPList is an environment whose PATH holds a codex that answers only
// `mcp list --json`, with the contents of mcpList and from the directory it was
// run in, which it records in the file returned.
func fakeMCPList(t *testing.T, mcpList string) (env []string, ranIn string) {
	t.Helper()
	bin, home := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\n[ \"$1 $2 $3\" = \"mcp list --json\" ] || exit 2\npwd -P > \"$CODEX_HOME/ran-in\"\ncat \"$CODEX_HOME/mcp-list.json\"\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "mcp-list.json"), []byte(mcpList), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"PATH=" + bin + ":/usr/bin:/bin", "CODEX_HOME=" + home}, filepath.Join(home, "ran-in")
}

func TestBuildPlanBareAlsoTurnsOffTheToolsAndEveryMCPServerCodexListsForTheDirectory(t *testing.T) {
	const listed = `[{"name":"two words","enabled":true},{"name":"alpha","enabled":false},{"name":"a.b","enabled":true},` +
		`{"name":"say \"hi\"","enabled":true},{"name":"alpha","enabled":true}]`
	cwd := gitDir(t)
	req := harnessRequest(cwd, "bare")
	env, ranIn := fakeMCPList(t, listed)
	req.Env = env

	plan, err := codex.New().BuildPlan(req)

	if err != nil {
		t.Fatal(err)
	}
	overrides := append(append([]string{}, leanArgv...), bareArgv...)
	overrides = append(overrides, "-c", `mcp_servers={"a.b"={enabled=false},"alpha"={enabled=false},"say \"hi\""={enabled=false},"two words"={enabled=false}}`)
	if want := argvAround(cwd, overrides); !reflect.DeepEqual(plan.Argv, want) {
		t.Errorf("argv\n got %q\nwant %q", plan.Argv, want)
	}
	if got, err := os.ReadFile(ranIn); err != nil || strings.TrimSpace(string(got)) != cwd {
		t.Errorf("codex mcp list ran in %q (read error %v), want the run's directory %q", got, err, cwd)
	}
}

func TestBuildPlanBareNamesNoMCPServerWhenCodexListsNone(t *testing.T) {
	cwd := gitDir(t)
	req := harnessRequest(cwd, "bare")
	req.Env, _ = fakeMCPList(t, "[]")

	plan, err := codex.New().BuildPlan(req)

	if err != nil {
		t.Fatal(err)
	}
	want := argvAround(cwd, append(append([]string{}, leanArgv...), bareArgv...))
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Errorf("argv\n got %q\nwant %q", plan.Argv, want)
	}
}

func TestBuildPlanBareIsRefusedWhenCodexCannotListItsMCPServers(t *testing.T) {
	cases := map[string][]string{"no codex on the PATH": {"PATH=" + t.TempDir()}}
	cases["unreadable list"], _ = fakeMCPList(t, "not json")
	for name, env := range cases {
		req := harnessRequest(gitDir(t), "bare")
		req.Env = env

		if _, err := codex.New().BuildPlan(req); err == nil {
			t.Errorf("%s: a bare plan was built", name)
		}
	}
}

func TestBuildPlanLeanKeepsTheMCPServersAndNeverListsThem(t *testing.T) {
	cwd := gitDir(t)
	req := harnessRequest(cwd, "lean")
	env, ranIn := fakeMCPList(t, `[{"name":"alpha","enabled":true}]`)
	req.Env = env

	plan, err := codex.New().BuildPlan(req)

	if err != nil {
		t.Fatal(err)
	}
	if want := argvAround(cwd, leanArgv); !reflect.DeepEqual(plan.Argv, want) {
		t.Errorf("argv\n got %q\nwant %q", plan.Argv, want)
	}
	if _, err := os.Stat(ranIn); !os.IsNotExist(err) {
		t.Errorf("a lean plan asked Codex for its MCP servers (stat error %v)", err)
	}
}

func TestHarnessReservedFlagNamesTheKeysAndFeaturesALevelSets(t *testing.T) {
	guard, ok := codex.New().(provider.HarnessReserver)
	if !ok {
		t.Fatal("the Codex adapter does not guard the keys its harness levels set")
	}
	cases := []struct {
		arg                   string
		lean, bare, fullLevel bool
	}{
		{"-c model_instructions_file=/x", true, true, false},
		{"-c skills.include_instructions=true", true, true, false},
		{"--config agents.enabled=true", true, true, false},
		{"-cfeatures.plugins=true", true, true, false},
		{"-c features.shell_tool=true", false, true, false},
		{"-c tools.view_image=true", false, true, false},
		{"-c web_search=live", false, true, false},
		{"-c mcp_servers.anything.enabled=true", false, true, false},
		{`-c mcp_servers."two words".enabled=true`, false, true, false},
		{"-c mcp_servers.anything.command=run", false, true, false},
		{"-c tools.web_search=true", false, true, false},
		{"-c features={shell_tool=true}", true, true, false},
		{"-c features={plugins=true}", true, true, false},
		{`-c features={"shell_tool"=true}`, true, true, false},
		{`-c "features".shell_tool=true`, false, true, false},
		{"-c skills={include_instructions=true}", true, true, false},
		{"-c agents={enabled=true}", true, true, false},
		{"-c tools={view_image=true}", false, true, false},
		{"-c mcp_servers={x={enabled=true}}", false, true, false},
		{"-c mcp_servers=1", false, true, false},
		{"-c features.shell_tool=true", false, true, false},
		{"-c featuresx=1", false, false, false},
		{"-c skills.other=1", false, false, false},
		{"-c model_reasoning_summary=none", false, false, false},
		{"--disable plugins", true, true, false},
		{"--enable=plugins", true, true, false},
		{"--enable shell_tool", false, true, false},
		{"--disable skill_search", false, true, false},
		{"--enable fast_mode", false, false, false},
		{"--add-dir /tmp/x", false, false, false},
	}
	for _, c := range cases {
		for level, want := range map[string]bool{"lean": c.lean, "bare": c.bare, "full": c.fullLevel, "": false} {
			if got := guard.HarnessReservedFlag(level, c.arg); got != want {
				t.Errorf("HarnessReservedFlag(%q, %q) = %v, want %v", level, c.arg, got, want)
			}
		}
	}
}

func TestBuildPlanOfALevelWithoutItsInstructionsFileIsRefused(t *testing.T) {
	req := harnessRequest(gitDir(t), "lean")
	req.HarnessInstructions = ""

	if _, err := codex.New().BuildPlan(req); err == nil {
		t.Error("a lean plan without an instructions file was built")
	}
}

func TestBuildPlanOfAResumeAndAReviewCarriesTheLevelBeforeTheirTail(t *testing.T) {
	cwd := gitDir(t)
	resume := harnessRequest(cwd, "lean")
	resume.Command, resume.SessionID = "resume", "00000000-0000-4000-8000-000000000001"
	review := harnessRequest(cwd, "lean")
	review.Command, review.ReviewTarget = "review", "uncommitted"

	resumePlan, err := codex.New().BuildPlan(resume)
	if err != nil {
		t.Fatal(err)
	}
	reviewPlan, err := codex.New().BuildPlan(review)
	if err != nil {
		t.Fatal(err)
	}

	wantResume := append(argvAround(cwd, leanArgv)[:len(argvAround(cwd, leanArgv))-1], "resume", resume.SessionID, "-")
	wantReview := append(argvAround(cwd, leanArgv)[:len(argvAround(cwd, leanArgv))-1], "review", "--uncommitted")
	if !reflect.DeepEqual(resumePlan.Argv, wantResume) {
		t.Errorf("resume argv\n got %q\nwant %q", resumePlan.Argv, wantResume)
	}
	if !reflect.DeepEqual(reviewPlan.Argv, wantReview) {
		t.Errorf("review argv\n got %q\nwant %q", reviewPlan.Argv, wantReview)
	}
}
