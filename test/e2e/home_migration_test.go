package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// migrationWorld is one test's state directory tier with an old home to
// migrate. Commands run with an explicit environment that never holds
// MSGATE_HOME unless a test adds it.
type migrationWorld struct {
	t      *testing.T
	env    []string
	parent string // the directory holding the old and the new home
	old    string
	new    string
}

// newDefaultTierWorld resolves homes under $HOME/.local/state.
func newDefaultTierWorld(t *testing.T) *migrationWorld {
	t.Helper()
	home := t.TempDir()
	parent := filepath.Join(home, ".local", "state")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	return newMigrationWorld(t, parent, "HOME="+home)
}

// newXDGTierWorld resolves homes under $XDG_STATE_HOME, with a HOME of its
// own whose default tier is empty. It returns that HOME too.
func newXDGTierWorld(t *testing.T) (*migrationWorld, string) {
	t.Helper()
	home := t.TempDir()
	parent := t.TempDir()
	return newMigrationWorld(t, parent, "HOME="+home, "XDG_STATE_HOME="+parent), home
}

func newMigrationWorld(t *testing.T, parent string, tier ...string) *migrationWorld {
	t.Helper()
	env := append([]string{
		"PATH=" + fakeDir + string(os.PathListSeparator) + "/usr/bin:/bin",
		"MSGATE_PRICES_URL=off", "FAKECODEX_FIXTURE=" + fixture("exec-ok"),
	}, tier...)
	return &migrationWorld{t: t, env: env, parent: parent, old: filepath.Join(parent, "agentcli"), new: filepath.Join(parent, "msgate")}
}

func (w *migrationWorld) run(args ...string) result {
	w.t.Helper()
	return runBin(w.t, msgateBin, w.env, args...)
}

// seedOldRuns runs n foreground runs (m1..mn) to completion in the old home.
func (w *migrationWorld) seedOldRuns(n int) []string {
	w.t.Helper()
	s := newSandbox(w.t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	s.home = w.old
	cwd := gitRepo(w.t)
	var ids []string
	for i := 1; i <= n; i++ {
		id := "m" + string(rune('0'+i))
		if r := s.run("exec", "--cwd", cwd, "--run-id", id, "q"); r.code != 0 {
			w.t.Fatalf("seeding %s: exit %d, stderr %s", id, r.code, r.stderr)
		}
		ids = append(ids, id)
	}
	return ids
}

func assertMovedWithRuns(t *testing.T, w *migrationWorld, ids []string) {
	t.Helper()
	if exists(w.old) {
		t.Errorf("the old home %s still exists", w.old)
	}
	for _, id := range ids {
		if !exists(filepath.Join(w.new, "runs", id, "state.json")) {
			t.Errorf("run %s is not under %s", id, w.new)
		}
	}
	if got := len(telemetryRecords(t, w.new)); got != len(ids) {
		t.Errorf("telemetry under the new home holds %d records, want %d", got, len(ids))
	}
}

func TestStatusMovesTheOldHomeOfTheDefaultTier(t *testing.T) {
	w := newDefaultTierWorld(t)
	ids := w.seedOldRuns(3)

	r := w.run("status", "m2")

	if r.code != 0 {
		t.Fatalf("status exit %d, stderr %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "state: done") {
		t.Errorf("status stdout = %q, want the migrated run listed as done", r.stdout)
	}
	assertMovedWithRuns(t, w, ids)
}

// slowOldRun admits a background run on a slow provider in the old home and
// waits until its worker holds it.
func (w *migrationWorld) slowOldRun(runID string) *sandbox {
	w.t.Helper()
	s := newSandbox(w.t).set("FAKECODEX_SLEEP_MS", "60000")
	s.home = w.old
	admitJob(w.t, s, runID, "q")
	waitForState(w.t, s, runID, "a running state with the provider group", hasProviderGroup)
	return s
}

func TestMigrationRefusesWhileAWorkerIsAlive(t *testing.T) {
	w := newDefaultTierWorld(t)
	s := w.slowOldRun("live1")

	r := w.run("status", "live1")

	if r.code != 1 {
		t.Errorf("exit %d, want 1; stderr %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "live1") {
		t.Errorf("stderr %q does not name the run", r.stderr)
	}
	if exists(w.new) {
		t.Errorf("the new home %s was created", w.new)
	}
	if got := stateOf(t, s, "live1")["state"]; got != "running" {
		t.Errorf("the old run's state = %v, want it untouched", got)
	}
}

func TestStatusMovesTheOldHomeOfTheXDGTier(t *testing.T) {
	w, home := newXDGTierWorld(t)
	ids := w.seedOldRuns(3)
	bystander := filepath.Join(home, ".local", "state", "agentcli")
	if err := os.MkdirAll(bystander, 0o700); err != nil {
		t.Fatal(err)
	}

	r := w.run("status", "m1")

	if r.code != 0 {
		t.Fatalf("status exit %d, stderr %s", r.code, r.stderr)
	}
	assertMovedWithRuns(t, w, ids)
	if !exists(bystander) || exists(filepath.Join(home, ".local", "state", "msgate")) {
		t.Errorf("the default tier was touched: %s exists %v", bystander, exists(bystander))
	}
}

func TestACommandThatFindsTheNewHomeNeverLooksAtTheOldOne(t *testing.T) {
	w := newDefaultTierWorld(t)
	current := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	current.home = w.new
	if r := current.run("exec", "--cwd", gitRepo(t), "--run-id", "n1", "q"); r.code != 0 {
		t.Fatalf("seeding the new home: exit %d, stderr %s", r.code, r.stderr)
	}
	stale := newSandbox(t)
	stale.home = w.old
	// A running run with no worker: any reader that opened it would settle it as lost.
	seedRunFiles(t, stale, "stale1", map[string]any{"state": "running", "ended_at": nil, "exit_code": nil, "outcome": nil}, nil, nil)
	before := readFile(t, filepath.Join(w.old, "runs", "stale1", "state.json"))

	r := w.run("status", "n1")

	if r.code != 0 || !strings.Contains(r.stdout, "state: done") {
		t.Fatalf("status exit %d, stdout %q, stderr %s", r.code, r.stdout, r.stderr)
	}
	if after := readFile(t, filepath.Join(w.old, "runs", "stale1", "state.json")); after != before {
		t.Errorf("the old home changed:\n%s\n->\n%s", before, after)
	}
	if exists(filepath.Join(w.new, "runs", "stale1")) {
		t.Errorf("the old run was moved into the new home")
	}
}

func TestMsgateHomeSetNeverMigrates(t *testing.T) {
	w := newDefaultTierWorld(t)
	w.seedOldRuns(1)
	explicit := w.new // sits beside the old home, where a migration would put it
	w.env = append(w.env, "MSGATE_HOME="+explicit)

	r := w.run("exec", "--cwd", gitRepo(t), "--run-id", "x1", "q")

	if r.code != 0 {
		t.Fatalf("exec exit %d, stderr %s", r.code, r.stderr)
	}
	if !exists(filepath.Join(explicit, "runs", "x1")) {
		t.Errorf("the run is not under MSGATE_HOME %s", explicit)
	}
	if !exists(filepath.Join(w.old, "runs", "m1")) || exists(filepath.Join(explicit, "runs", "m1")) {
		t.Errorf("the old home was moved: old run exists %v, moved run exists %v", exists(filepath.Join(w.old, "runs", "m1")), exists(filepath.Join(explicit, "runs", "m1")))
	}
}

func TestMigrationSettlesARunWhoseWorkerIsGone(t *testing.T) {
	w := newDefaultTierWorld(t)
	s := newSandbox(t).set("FAKECODEX_SLEEP_MS", "60000")
	s.home = w.old
	admitJob(t, s, "dead1", "q")
	before := crashWorker(t, s, "dead1")

	// stats reads telemetry only, so the lost run is settled by the migration alone.
	r := w.run("stats", "--json")

	if r.code != 0 {
		t.Fatalf("stats exit %d, stderr %s", r.code, r.stderr)
	}
	if exists(w.old) {
		t.Errorf("the old home still exists")
	}
	s.home = w.new
	assertLostOnce(t, s, "dead1")
	assertGroupGone(t, int(before["provider_pgid"].(float64)))
}

// readOnlyParent seeds the old home, optionally leaves the lock file in place,
// then removes write access from the directory holding both homes.
func readOnlyParent(t *testing.T, w *migrationWorld, withLockFile bool) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind root")
	}
	w.seedOldRuns(1)
	if withLockFile {
		if err := os.WriteFile(filepath.Join(w.parent, ".msgate-migrate.lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(w.parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(w.parent, 0o700) })
}

func assertRefusedWithNothingMoved(t *testing.T, w *migrationWorld, r result) {
	t.Helper()
	if r.code != 1 {
		t.Errorf("exit %d, want 1; stderr %s", r.code, r.stderr)
	}
	for _, path := range []string{w.old, w.new} {
		if !strings.Contains(r.stderr, path) {
			t.Errorf("stderr %q does not name %s", r.stderr, path)
		}
	}
	if !exists(filepath.Join(w.old, "runs", "m1", "state.json")) || exists(w.new) {
		t.Errorf("something moved: old run exists %v, new home exists %v", exists(filepath.Join(w.old, "runs", "m1", "state.json")), exists(w.new))
	}
}

func TestMigrationNamesBothPathsWhenTheParentIsReadOnly(t *testing.T) {
	w := newDefaultTierWorld(t)
	readOnlyParent(t, w, false)

	assertRefusedWithNothingMoved(t, w, w.run("status", "m1"))
}

func TestMigrationNamesBothPathsWhenTheRenameFails(t *testing.T) {
	w := newDefaultTierWorld(t)
	readOnlyParent(t, w, true)

	assertRefusedWithNothingMoved(t, w, w.run("status", "m1"))
}

func TestASecondCommandAfterTheMoveLeavesTheOldPathAlone(t *testing.T) {
	w := newDefaultTierWorld(t)
	w.seedOldRuns(2)
	if r := w.run("status", "m1"); r.code != 0 {
		t.Fatalf("first command: exit %d, stderr %s", r.code, r.stderr)
	}
	state := readFile(t, filepath.Join(w.new, "runs", "m1", "state.json"))

	r := w.run("status", "m1")

	if r.code != 0 || !strings.Contains(r.stdout, "state: done") {
		t.Fatalf("second command: exit %d, stdout %q, stderr %s", r.code, r.stdout, r.stderr)
	}
	if exists(w.old) {
		t.Errorf("the old path %s exists again", w.old)
	}
	if readFile(t, filepath.Join(w.new, "runs", "m1", "state.json")) != state {
		t.Errorf("the migrated run changed")
	}
}

// startBin starts msgate in the background; the returned function waits for it.
func startBin(t *testing.T, env []string, args ...string) func() result {
	t.Helper()
	cmd := exec.Command(msgateBin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return func() result {
		code := 0
		if err := cmd.Wait(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("wait %v: %v", args, err)
			}
			code = ee.ExitCode()
		}
		return result{out.String(), errb.String(), code}
	}
}

func TestTwoCommandsAgainstOneOldHomeBothSucceed(t *testing.T) {
	for i := 0; i < 8; i++ {
		w := newDefaultTierWorld(t)
		ids := w.seedOldRuns(3)

		first := startBin(t, w.env, "status", "m1")
		second := startBin(t, w.env, "status", "m2")

		for n, wait := range []func() result{first, second} {
			if r := wait(); r.code != 0 || !strings.Contains(r.stdout, "state: done") {
				t.Fatalf("round %d command %d: exit %d, stdout %q, stderr %s", i, n, r.code, r.stdout, r.stderr)
			}
		}
		assertMovedWithRuns(t, w, ids)
	}
}

func TestACommandWaitsOnTheMigrationLockThenUsesTheNewHome(t *testing.T) {
	w := newDefaultTierWorld(t)
	ids := w.seedOldRuns(2)
	lock, err := os.OpenFile(filepath.Join(w.parent, ".msgate-migrate.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan result, 1)
	wait := startBin(t, w.env, "status", "m1")
	go func() { waiting <- wait() }()

	select {
	case r := <-waiting:
		t.Fatalf("the command did not wait for the lock: exit %d, stdout %q, stderr %s", r.code, r.stdout, r.stderr)
	case <-time.After(700 * time.Millisecond):
	}
	if !exists(w.old) || exists(w.new) {
		t.Fatalf("the command moved the home while the lock was held")
	}
	if err := os.Rename(w.old, w.new); err != nil { // the other process's move
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-waiting:
		if r.code != 0 || !strings.Contains(r.stdout, "state: done") {
			t.Errorf("exit %d, stdout %q, stderr %s", r.code, r.stdout, r.stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not proceed after the lock was released")
	}
	assertMovedWithRuns(t, w, ids)
}

// A migrated run is read from where it now lives: the paths its state file
// recorded name the old home.
func TestMigratedRunIsReadFromTheNewHome(t *testing.T) {
	w := newDefaultTierWorld(t)
	w.seedOldRuns(1)
	output := filepath.Join(w.new, "runs", "m1", "output.md")
	runDir := filepath.Join(w.new, "runs", "m1")

	status := w.run("status", "--json", "m1") // the first command migrates
	if status.code != 0 {
		t.Fatalf("status exit %d, stderr %s", status.code, status.stderr)
	}
	checkFields(t, "status json", status.json(t), map[string]any{"output_path": output, "run_dir": runDir})
	if text := w.run("status", "m1"); !strings.Contains(text.stdout, "output_path: "+output+"\n") {
		t.Errorf("status text = %q, want output_path %s", text.stdout, output)
	}

	waitJSON := w.run("wait", "--json", "m1")
	if waitJSON.code != 0 {
		t.Fatalf("wait --json exit %d, stderr %s", waitJSON.code, waitJSON.stderr)
	}
	checkFields(t, "wait json", waitJSON.json(t), map[string]any{"output_path": output, "run_dir": runDir})
	if text := w.run("wait", "m1"); strings.TrimSpace(text.stdout) != output {
		t.Errorf("wait text = %q, want %s", text.stdout, output)
	}

	want := readFile(t, output)
	if got := w.run("result", "m1"); got.code != 0 || got.stdout != want || want == "" {
		t.Errorf("result: exit %d, stdout %q, stderr %q, want the output %q", got.code, got.stdout, got.stderr, want)
	}

	ann := w.run("annotate", output, "--attr", "acted=yes", "--json")
	if ann.code != 0 {
		t.Fatalf("annotate by output path: exit %d, stderr %s", ann.code, ann.stderr)
	}
	checkFields(t, "annotate json", ann.json(t), map[string]any{"run_id": "m1"})
}
