package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type commitSpec struct {
	subject string
	version string // when set, the commit writes this to VERSION
}

// newReleaseRepo makes a git repository with one commit per spec, in order,
// and returns its directory and the commit ids in the same order.
func newReleaseRepo(t *testing.T, specs ...commitSpec) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	var shas []string
	for i, s := range specs {
		if s.version != "" {
			if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(s.version+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "change"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", s.subject)
		shas = append(shas, gitIn(t, dir, "rev-parse", "HEAD"))
	}
	return dir, shas
}

// runScript runs one of the repository's scripts with dir as its working
// directory and env added to a git environment free of user configuration.
func runScript(t *testing.T, dir, script string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(repoRoot, "scripts", script)}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, env...)...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s: %v", script, err)
		}
		code = ee.ExitCode()
	}
	return result{o.String(), e.String(), code}
}

func shortSHA(t *testing.T, dir, sha string) string {
	t.Helper()
	return gitIn(t, dir, "log", "-1", "--format=%h", sha)
}

// threeVersions is a history whose VERSION went 0.1.0 → 0.2.0 → 0.3.0, with
// one fix after the last bump.
var threeVersions = []commitSpec{
	{"chore: bootstrap", "0.1.0"},
	{"feat: early feature", ""},
	{"chore: release 0.2.0", "0.2.0"},
	{"feat(cli): add a flag", ""},
	{"fix: handle an empty file", ""},
	{"docs: describe the flag", ""},
	{"chore: release 0.3.0", "0.3.0"},
	{"fix(mod)!: keep the label", ""},
}

func TestReleaseNotesListTheCommitsSinceThePreviousVersion(t *testing.T) {
	dir, shas := newReleaseRepo(t, threeVersions...)
	source := shas[7]

	r := runScript(t, dir, "release-notes.sh", nil, source)
	if r.code != 0 {
		t.Fatalf("exit %d\nstderr: %s", r.code, r.stderr)
	}
	want := "Build of " + source + ".\n" +
		"\n### Features\n" +
		"- feat(cli): add a flag (" + shortSHA(t, dir, shas[3]) + ")\n" +
		"\n### Fixes\n" +
		"- fix: handle an empty file (" + shortSHA(t, dir, shas[4]) + ")\n" +
		"- fix(mod)!: keep the label (" + shortSHA(t, dir, shas[7]) + ")\n" +
		"\n### Other\n" +
		"- docs: describe the flag (" + shortSHA(t, dir, shas[5]) + ")\n"
	if r.stdout != want {
		t.Errorf("notes:\n%s\nwant:\n%s", r.stdout, want)
	}
}

func TestReleaseNotesOfTheFirstVersionListEveryCommit(t *testing.T) {
	dir, shas := newReleaseRepo(t,
		commitSpec{"chore: bootstrap", "0.1.0"},
		commitSpec{"feat: one", ""},
		commitSpec{"fix: two", ""},
	)

	r := runScript(t, dir, "release-notes.sh", nil, shas[2])
	if r.code != 0 {
		t.Fatalf("exit %d\nstderr: %s", r.code, r.stderr)
	}
	want := "Build of " + shas[2] + ".\n" +
		"\n### Features\n- feat: one (" + shortSHA(t, dir, shas[1]) + ")\n" +
		"\n### Fixes\n- fix: two (" + shortSHA(t, dir, shas[2]) + ")\n" +
		"\n### Other\n- chore: bootstrap (" + shortSHA(t, dir, shas[0]) + ")\n"
	if r.stdout != want {
		t.Errorf("notes:\n%s\nwant:\n%s", r.stdout, want)
	}
}

// The gate, the publish script and the dist workflow are tested offline by one
// shell script, which the plugin repository runs the same way; real git runs
// against local repositories and the GitHub CLI is a fake.
func TestReleaseScripts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	r := runScript(t, repoRoot, "test-release.sh", nil)
	if r.code != 0 {
		t.Fatalf("test-release.sh exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
}
