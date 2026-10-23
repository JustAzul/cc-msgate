package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

const harnessSession = "00000000-0000-4000-8000-000000000005"

// harnessProvider runs every level and echoes the level and the instructions
// path it was given into its argv, so a test reads what the CLI asked of it.
type harnessProvider struct{}

func (harnessProvider) Name() string { return "harnessprov" }
func (harnessProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Commands: []string{"exec", "review", "resume"}, ReviewTargets: []string{"uncommitted"},
		Sandboxes: []string{"read-only"}, Harnesses: []string{"lean", "bare"},
	}
}
func (harnessProvider) ReservedFlag(string) bool { return false }

// HarnessReservedFlag: "-c lean-key=" is set by lean and bare, "-c bare-key=" by bare only.
func (harnessProvider) HarnessReservedFlag(level, arg string) bool {
	return strings.HasPrefix(arg, "-c lean-key=") || (level == provider.HarnessBare && strings.HasPrefix(arg, "-c bare-key="))
}
func (harnessProvider) BuildPlan(req provider.Request) (provider.Plan, error) {
	argv := []string{"true", "harness=" + req.Harness, "instructions=" + req.HarnessInstructions}
	return provider.Plan{Argv: append(argv, req.Passthrough...)}, nil
}
func (harnessProvider) ParseEvent([]byte) (provider.Event, bool) { return provider.Event{}, true }
func (harnessProvider) VersionArgs() []string                    { return nil }
func (harnessProvider) ReadSession(_ []string, id string) (provider.Session, bool, error) {
	return provider.Session{Cwd: "/"}, id == harnessSession, nil
}

// fullOnlyProvider lists no harness level.
type fullOnlyProvider struct{ harnessProvider }

func (fullOnlyProvider) Name() string { return "fullonlyprov" }
func (fullOnlyProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Commands: []string{"exec", "review", "resume"}, ReviewTargets: []string{"uncommitted"}, Sandboxes: []string{"read-only"},
	}
}

func init() {
	provider.Register(harnessProvider{})
	provider.Register(fullOnlyProvider{})
}

// msgateAt runs msgate against home and returns its exit code and streams.
func msgateAt(home string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	env := []string{"MSGATE_HOME=" + home, "PATH=" + os.Getenv("PATH")}
	code = Run(append([]string{"msgate"}, args...), env, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// runAt is the request.json of run runID.
func runAt(t *testing.T, st *store.Store, runID string) requestRecord {
	t.Helper()
	var req requestRecord
	if err := st.ReadRequest(runID, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

// conversationOf is the conversation run runID belongs to.
func conversationOf(t *testing.T, st *store.Store, runID string) store.Conversation {
	t.Helper()
	state, err := st.ReadState(runID)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.ReadConversation(state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestExecHarnessIsRecordedOnTheRunAndTheConversation(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)

	code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", "lean", "go")

	if code != 0 {
		t.Fatalf("exit %d (stderr %q)", code, stderr)
	}
	if got := runAt(t, st, "r1").Harness; got != "lean" {
		t.Errorf("request.json harness = %q, want lean", got)
	}
	if got := conversationOf(t, st, "r1").Defaults.Harness; got != "lean" {
		t.Errorf("conversation defaults.harness = %q, want lean", got)
	}
	wantArgv := []string{"true", "harness=lean", "instructions=" + filepath.Join(home, "harness", "instructions.md")}
	if got := runAt(t, st, "r1").Plan.Argv; strings.Join(got, "|") != strings.Join(wantArgv, "|") {
		t.Errorf("plan argv = %v, want %v", got, wantArgv)
	}
}

func TestExecWithoutHarnessRunsFullAndWritesNoInstructions(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)

	code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "go")

	if code != 0 {
		t.Fatalf("exit %d (stderr %q)", code, stderr)
	}
	req := runAt(t, st, "r1")
	if got := strings.Join(req.Plan.Argv, "|"); got != "true|harness=full|instructions=" {
		t.Errorf("plan argv = %q, want true|harness=full|instructions=", got)
	}
	if req.Harness != "full" || conversationOf(t, st, "r1").Defaults.Harness != "full" {
		t.Errorf("recorded harness = %q / %q, want full / full", req.Harness, conversationOf(t, st, "r1").Defaults.Harness)
	}
	if _, err := os.Stat(filepath.Join(home, "harness")); !os.IsNotExist(err) {
		t.Errorf("a full run touched the harness directory (stat error %v)", err)
	}
}

// resumable gives the conversation of run runID a provider session, which the
// no-op provider never reports, and returns its id.
func resumable(t *testing.T, st *store.Store, runID string) string {
	t.Helper()
	conv := conversationOf(t, st, runID)
	session := "session-of-" + conv.ConversationID
	if err := st.UpdateConversation(conv.ConversationID, func(c *store.Conversation) { c.ProviderSessionID = &session }); err != nil {
		t.Fatal(err)
	}
	return conv.ConversationID
}

func TestSendKeepsTheConversationsHarnessUnlessTheTurnPassesOne(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	if code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", "lean", "go"); code != 0 {
		t.Fatalf("exec: exit %d (stderr %q)", code, stderr)
	}
	id := resumable(t, st, "r1")

	for _, turn := range []struct {
		runID string
		args  []string
		want  string
	}{
		{"r2", nil, "lean"},
		{"r3", []string{"--harness", "full"}, "full"},
		{"r4", nil, "lean"},
		{"r5", []string{"--harness", "bare"}, "bare"},
	} {
		args := append([]string{"send", "--run-id", turn.runID}, turn.args...)
		if code, _, stderr := msgateAt(home, append(args, id, "again")...); code != 0 {
			t.Fatalf("send %s: exit %d (stderr %q)", turn.runID, code, stderr)
		}
		req := runAt(t, st, turn.runID)
		if req.Harness != turn.want || req.Plan.Argv[1] != "harness="+turn.want {
			t.Errorf("run %s harness = %q, argv %v, want %q", turn.runID, req.Harness, req.Plan.Argv, turn.want)
		}
	}
	if got := conversationOf(t, st, "r1").Defaults.Harness; got != "lean" {
		t.Errorf("conversation defaults.harness = %q after the turns, want lean", got)
	}
}

func TestSendOnAConversationRecordedWithoutAHarnessRunsFull(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	if code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", "lean", "go"); code != 0 {
		t.Fatalf("exec: exit %d (stderr %q)", code, stderr)
	}
	id := resumable(t, st, "r1")
	if err := st.UpdateConversation(id, func(c *store.Conversation) { c.Defaults.Harness = "" }); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := msgateAt(home, "send", "--run-id", "r2", id, "again")

	if code != 0 {
		t.Fatalf("send: exit %d (stderr %q)", code, stderr)
	}
	if req := runAt(t, st, "r2"); req.Harness != "full" || req.Plan.Argv[1] != "harness=full" {
		t.Errorf("run r2 harness = %q, argv %v, want full", req.Harness, req.Plan.Argv)
	}
}

func TestAdoptingAnOutsideSessionRecordsTheLevelAsTheConversationsDefault(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"flag", []string{"--harness", "bare"}, "bare"},
		{"no flag", nil, "full"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			st := store.Open(home)
			args := append([]string{"send", "--provider", "harnessprov", "--run-id", "r1"}, c.args...)

			code, _, stderr := msgateAt(home, append(args, harnessSession, "go on")...)

			if code != 0 {
				t.Fatalf("exit %d (stderr %q)", code, stderr)
			}
			if got := conversationOf(t, st, "r1").Defaults.Harness; got != c.want {
				t.Errorf("conversation defaults.harness = %q, want %q", got, c.want)
			}
			if req := runAt(t, st, "r1"); req.Harness != c.want || req.Plan.Argv[1] != "harness="+c.want {
				t.Errorf("run r1 harness = %q, argv %v, want %q", req.Harness, req.Plan.Argv, c.want)
			}
		})
	}
}

func TestAnUnknownHarnessLevelIsAUsageError(t *testing.T) {
	home := t.TempDir()

	code, _, _ := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", "other", "go")

	if code != ExitUsage {
		t.Errorf("exit %d, want %d", code, ExitUsage)
	}
	if _, err := os.Stat(filepath.Join(home, "runs")); !os.IsNotExist(err) {
		t.Errorf("a refused run left a runs directory (stat error %v)", err)
	}
}

func TestReviewRunsLeanButRefusesBare(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	review := []string{"review", "--provider", "harnessprov", "--cwd", t.TempDir(), "--uncommitted"}

	bareCode, _, _ := msgateAt(home, append(review, "--run-id", "r-bare", "--harness", "bare")...)
	leanCode, _, stderr := msgateAt(home, append(review, "--run-id", "r-lean", "--harness", "lean")...)

	if bareCode != ExitUsage {
		t.Errorf("review --harness bare: exit %d, want %d", bareCode, ExitUsage)
	}
	if st.RunExists("r-bare") {
		t.Error("the refused bare review left a run directory")
	}
	if leanCode != 0 {
		t.Fatalf("review --harness lean: exit %d (stderr %q)", leanCode, stderr)
	}
	if req := runAt(t, st, "r-lean"); req.Harness != "lean" || req.Plan.Argv[1] != "harness=lean" {
		t.Errorf("run r-lean harness = %q, argv %v, want lean", req.Harness, req.Plan.Argv)
	}
}

func TestPassthroughOfAKeyTheLevelSetsIsAUsageErrorOnlyBelowFull(t *testing.T) {
	for _, c := range []struct {
		name    string
		harness string
		native  string
		refused bool
	}{
		{"lean sets lean-key", "lean", "lean-key=1", true},
		{"bare sets lean-key", "bare", "lean-key=1", true},
		{"bare sets bare-key", "bare", "bare-key=1", true},
		{"lean leaves bare-key", "lean", "bare-key=1", false},
		{"full leaves lean-key", "full", "lean-key=1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			st := store.Open(home)

			code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", c.harness, "go", "--", "-c", c.native)

			if c.refused {
				if code != ExitUsage || st.RunExists("r1") {
					t.Errorf("exit %d, run exists %v, want exit %d and no run", code, st.RunExists("r1"), ExitUsage)
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit %d (stderr %q)", code, stderr)
			}
			if got := runAt(t, st, "r1").Passthrough; strings.Join(got, "|") != "-c|"+c.native {
				t.Errorf("passthrough = %v, want [-c %s]", got, c.native)
			}
		})
	}
}

func TestSendRefusesPassthroughOfAKeyTheTurnsLevelSets(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	if code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", "lean", "go"); code != 0 {
		t.Fatalf("exec: exit %d (stderr %q)", code, stderr)
	}
	id := resumable(t, st, "r1")

	leanCode, _, _ := msgateAt(home, "send", "--run-id", "r2", id, "again", "--", "-c", "lean-key=1")
	fullCode, _, stderr := msgateAt(home, "send", "--run-id", "r3", "--harness", "full", id, "again", "--", "-c", "lean-key=1")

	if leanCode != ExitUsage || st.RunExists("r2") {
		t.Errorf("lean send: exit %d, run exists %v, want exit %d and no run", leanCode, st.RunExists("r2"), ExitUsage)
	}
	if fullCode != 0 {
		t.Errorf("full send: exit %d (stderr %q), want 0", fullCode, stderr)
	}
}

func TestAProviderThatDoesNotListALevelRefusesItOnEveryCommand(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	cwd := t.TempDir()
	if code, _, stderr := msgateAt(home, "exec", "--provider", "fullonlyprov", "--cwd", cwd, "--run-id", "r1", "go"); code != 0 {
		t.Fatalf("exec at full: exit %d (stderr %q)", code, stderr)
	}
	id := resumable(t, st, "r1")

	execCode, _, _ := msgateAt(home, "exec", "--provider", "fullonlyprov", "--cwd", cwd, "--run-id", "r2", "--harness", "lean", "go")
	reviewCode, _, _ := msgateAt(home, "review", "--provider", "fullonlyprov", "--cwd", cwd, "--run-id", "r3", "--uncommitted", "--harness", "lean")
	sendCode, _, _ := msgateAt(home, "send", "--run-id", "r4", "--harness", "lean", id, "again")

	for name, code := range map[string]int{"exec": execCode, "review": reviewCode, "send": sendCode} {
		if code != ExitUsage {
			t.Errorf("%s --harness lean: exit %d, want %d", name, code, ExitUsage)
		}
	}
	for _, runID := range []string{"r2", "r3", "r4"} {
		if st.RunExists(runID) {
			t.Errorf("the refused run %s left a run directory", runID)
		}
	}
}

func TestDryRunShowsTheLevelsPlanAndWritesNothing(t *testing.T) {
	home := t.TempDir()

	code, stdout, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--harness", "bare", "--dry-run", "--json", "go")

	if code != 0 {
		t.Fatalf("exit %d (stderr %q)", code, stderr)
	}
	var plan struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("stdout is not the plan: %v\n%q", err, stdout)
	}
	want := "true|harness=bare|instructions=" + filepath.Join(home, "harness", "instructions.md")
	if got := strings.Join(plan.Argv, "|"); got != want {
		t.Errorf("planned argv = %q, want %q", got, want)
	}
	for _, name := range []string{"runs", "conversations", "harness"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("a dry run created %s (stat error %v)", name, err)
		}
	}
}

func TestTheInstructionsFileHoldsMsgatesTextWhateverItHeldBefore(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "harness", "instructions.md")
	run := func(runID string) {
		t.Helper()
		if code, _, stderr := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", runID, "--harness", "lean", "go"); code != 0 {
			t.Fatalf("exec %s: exit %d (stderr %q)", runID, code, stderr)
		}
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	run("r1")
	missing := read()
	if err := os.WriteFile(path, []byte("edited by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("r2")

	const want = "Follow the user's instructions. Be concise.\n"
	if missing != want || read() != want {
		t.Errorf("instructions after a missing file = %q, after an edited one = %q, want %q both times", missing, read(), want)
	}
}

func TestAnInstructionsFileThatCannotBeWrittenFailsTheRunBeforeItIsAdmitted(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	if err := os.WriteFile(filepath.Join(home, "harness"), nil, 0o600); err != nil { // no file can be made below it
		t.Fatal(err)
	}

	code, _, _ := msgateAt(home, "exec", "--provider", "harnessprov", "--cwd", t.TempDir(), "--run-id", "r1", "--harness", "lean", "go")

	if code != ExitInternal || st.RunExists("r1") {
		t.Errorf("exit %d, run exists %v, want exit %d and no run", code, st.RunExists("r1"), ExitInternal)
	}
}
