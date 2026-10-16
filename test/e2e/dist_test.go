package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var distPlatforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}

// buildDist runs scripts/build-dist.sh from root into a fresh out directory.
func buildDist(t *testing.T, root string, extraEnv ...string) (out string, res result) {
	t.Helper()
	out = filepath.Join(t.TempDir(), "dist")
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "build-dist.sh"), out)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), extraEnv...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run build-dist: %v", err)
		}
		code = ee.ExitCode()
	}
	return out, result{o.String(), e.String(), code}
}

func mustBuildDist(t *testing.T, extraEnv ...string) string {
	t.Helper()
	out, r := buildDist(t, repoRoot, extraEnv...)
	if r.code != 0 {
		t.Fatalf("build-dist exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	return out
}

var fixedEnv = []string{"SOURCE_SHA=0123456789abcdef0123456789abcdef01234567", "BUILD_SEQ=1234"}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestDistContent(t *testing.T) {
	out := mustBuildDist(t, fixedEnv...)

	manifest := readJSONFile(t, filepath.Join(out, ".claude-plugin", "plugin.json"))
	if manifest["name"] != "agentcli" {
		t.Errorf("manifest name = %v", manifest["name"])
	}
	if _, has := manifest["version"]; has {
		t.Error("dist manifest carries a version field")
	}

	shim := filepath.Join(out, "bin", "msgate")
	if st, err := os.Stat(shim); err != nil || st.Mode().Perm()&0o111 == 0 {
		t.Errorf("bin/msgate missing or not executable: %v %v", st, err)
	}
	for _, d := range []string{"bin", "libexec"} {
		entries, err := os.ReadDir(filepath.Join(out, d))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		want := []string{"msgate"}
		if d == "libexec" {
			want = nil
			for _, p := range distPlatforms {
				want = append(want, "msgate-"+p)
			}
			sort.Strings(want)
		}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("%s/ = %v, want %v", d, names, want)
		}
	}

	// SHA256SUMS: sha256sum format, one line per binary, and every line verifies.
	sums := readFile(t, filepath.Join(out, "SHA256SUMS"))
	lines := strings.Split(strings.TrimSuffix(sums, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("SHA256SUMS has %d lines:\n%s", len(lines), sums)
	}
	for i, p := range distPlatforms {
		rel := "libexec/msgate-" + p
		want := sha256File(t, filepath.Join(out, rel)) + "  " + rel
		if lines[i] != want {
			t.Errorf("SHA256SUMS line %d = %q, want %q", i+1, lines[i], want)
		}
	}
	if _, err := exec.LookPath("sha256sum"); err == nil {
		cmd := exec.Command("sha256sum", "-c", "SHA256SUMS")
		cmd.Dir = out
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("sha256sum -c failed: %v\n%s", err, b)
		}
	}

	hooks := readJSONFile(t, filepath.Join(out, "hooks", "hooks.json"))
	if !strings.Contains(fmt.Sprint(hooks), sessionStartCommand) {
		t.Errorf("hooks.json lacks the SessionStart link and prune command: %v", hooks)
	}
	if m, _ := hooks["modules"].([]any); len(m) != 1 || m[0] != "./register.js" {
		t.Errorf("hooks.json modules = %v, want [./register.js]", hooks["modules"])
	}
	for _, f := range []string{"LICENSE", "README.md", filepath.Join("hooks", "register.js")} {
		if !exists(filepath.Join(out, f)) {
			t.Errorf("dist lacks %s", f)
		}
	}
	for _, f := range []string{"tests", filepath.Join(".claude-plugin", "types"), "tsconfig.json"} {
		if exists(filepath.Join(out, f)) {
			t.Errorf("dist carries the development path %s", f)
		}
	}
}

// The files Claude Code writes beside a mod it loads from a folder are not part
// of the shipped plugin, so planting them in the source tree must not change
// the dist tree.
func TestDistOmitsGeneratedModFiles(t *testing.T) {
	repo := t.TempDir()
	copyTree(t, repo)
	typesDir := filepath.Join(repo, "plugin", ".claude-plugin", "types", "claude-code")
	if err := os.MkdirAll(typesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(typesDir, "index.d.ts"):                     "export {}\n",
		filepath.Join(repo, "plugin", "tsconfig.json"):            "{}\n",
		filepath.Join(repo, "plugin", "tests", "planted.test.ts"): "export {}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, r := buildDist(t, repo, fixedEnv...)
	if r.code != 0 {
		t.Fatalf("build-dist exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, f := range []string{"tests", filepath.Join(".claude-plugin", "types"), "tsconfig.json"} {
		if exists(filepath.Join(out, f)) {
			t.Errorf("dist carries %s", f)
		}
	}
	if !exists(filepath.Join(out, ".claude-plugin", "plugin.json")) || !exists(filepath.Join(out, "hooks", "register.js")) {
		t.Error("dist lost the manifest or the mod entry")
	}
}

func TestDistIsReproducible(t *testing.T) {
	a := mustBuildDist(t, fixedEnv...)
	b := mustBuildDist(t, fixedEnv...)
	if x, y := readFile(t, filepath.Join(a, "SHA256SUMS")), readFile(t, filepath.Join(b, "SHA256SUMS")); x != y {
		t.Errorf("SHA256SUMS differ:\n%s\n%s", x, y)
	}
	for _, p := range distPlatforms {
		rel := filepath.Join("libexec", "msgate-"+p)
		if !bytes.Equal([]byte(readFile(t, filepath.Join(a, rel))), []byte(readFile(t, filepath.Join(b, rel)))) {
			t.Errorf("%s differs between two builds", rel)
		}
	}
}

func TestDistEmbedsVersionMetadata(t *testing.T) {
	out := mustBuildDist(t, fixedEnv...)
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skip("no dist binary for " + runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no dist binary for " + runtime.GOARCH)
	}
	want := strings.TrimSpace(readFile(t, filepath.Join(repoRoot, "VERSION")))
	// Through the plugin shim, so the whole chain runs.
	r := runShim(t, filepath.Join(out, "bin", "msgate"), "", "version", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	m := r.json(t)
	if m["version"] != want || m["source_commit"] != "0123456789abcdef0123456789abcdef01234567" ||
		m["build_seq"] != float64(1234) || m["platform"] != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("version --json = %v", m)
	}
}

// copyTree copies the buildable parts of the repository into dst.
func copyTree(t *testing.T, dst string) {
	t.Helper()
	skip := map[string]bool{".git": true, "build": true, "dist": true, ".claude": true}
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, path)
		if rel == "." {
			return nil
		}
		if skip[d.Name()] { // .git is a file in a linked worktree
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestDistBuildSeqComesFromFullGitHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	copyTree(t, repo)
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	for i := 1; i <= 3; i++ {
		gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("c%d", i))
	}
	head := gitIn(t, repo, "rev-parse", "HEAD")

	out, r := buildDist(t, repo)
	if r.code != 0 {
		t.Fatalf("build-dist exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		bin := filepath.Join(out, "libexec", "msgate-"+runtime.GOOS+"-"+runtime.GOARCH)
		if exists(bin) {
			m := runBin(t, bin, []string{"PATH=/usr/bin:/bin"}, "version", "--json").json(t)
			if m["build_seq"] != float64(3) || m["source_commit"] != head {
				t.Errorf("version --json = %v, want build_seq 3 and commit %s", m, head)
			}
		}
	}

	// A shallow clone would undercount build_seq: the script must refuse.
	shallow := filepath.Join(t.TempDir(), "shallow")
	gitIn(t, repo, "clone", "-q", "--depth", "1", "file://"+repo, shallow)
	_, r = buildDist(t, shallow)
	if r.code == 0 || !strings.Contains(r.stderr, "shallow") {
		t.Errorf("shallow clone: exit %d stderr %q, want a refusal naming the shallow history", r.code, r.stderr)
	}
}

func TestDistRefusesNonEmptyOutDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(repoRoot, "scripts", "build-dist.sh"), dir)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), fixedEnv...)
	err := cmd.Run()
	if err == nil {
		t.Error("expected failure for a non-empty out dir")
	}
	if !exists(filepath.Join(dir, "keep")) {
		t.Error("existing content was removed")
	}
}
