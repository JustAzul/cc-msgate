// Package e2e runs black-box tests against the built msgate binary and the
// fake codex provider.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	msgateBin string // absolute path to the built msgate
	msgateDir string // directory holding msgate only (no codex)
	fakeDir   string // directory holding the fake provider as "codex"
	repoRoot  string
)

func TestMain(m *testing.M) {
	code := realMain(m)
	cleanupVariants()
	os.Exit(code)
}

func realMain(m *testing.M) int {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	repoRoot = root
	tmp, err := os.MkdirTemp("", "msgate-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	msgateDir = filepath.Join(tmp, "msgate-bin")
	fakeDir = filepath.Join(tmp, "fake-bin")
	msgateBin = filepath.Join(msgateDir, "msgate")
	builds := []struct{ out, pkg string }{
		{msgateBin, "./cmd/msgate"},
		{filepath.Join(fakeDir, "codex"), "./internal/testutil/fakecodex"},
	}
	for _, b := range builds {
		cmd := exec.Command("go", "build", "-trimpath", "-o", b.out, b.pkg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", b.pkg, err, out)
			return 1
		}
	}
	return m.Run()
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above test directory")
		}
		dir = parent
	}
}

func fixture(name string) string { return filepath.Join(repoRoot, "testdata", "codex", name+".jsonl") }

// sandbox is one test's isolated world: its own MSGATE_HOME and PATH.
type sandbox struct {
	t     *testing.T
	home  string // MSGATE_HOME (not created in advance)
	path  string
	env   map[string]string
	stdin string
}

// newSandbox puts the fake codex on PATH and switches the price refresh off; a
// test that refreshes sets MSGATE_PRICES_URL to its own server. The child
// environment is built explicitly and never inherited, so the host's
// CLAUDE_CODE_SESSION_ID or a real codex cannot leak in.
func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	return &sandbox{
		t:    t,
		home: filepath.Join(t.TempDir(), "home"),
		path: fakeDir + string(os.PathListSeparator) + "/usr/bin:/bin",
		env:  map[string]string{"MSGATE_PRICES_URL": "off"},
	}
}

// withoutProvider removes the fake from PATH.
func (s *sandbox) withoutProvider() *sandbox {
	s.path = msgateDir
	return s
}

func (s *sandbox) set(k, v string) *sandbox { s.env[k] = v; return s }

func (s *sandbox) recordTo() string {
	p := filepath.Join(s.t.TempDir(), "record.json")
	s.env["FAKECODEX_RECORD"] = p
	return p
}

type result struct {
	stdout, stderr string
	code           int
}

func (r result) lines() []string {
	if r.stdout == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
}

func (r result) lastLine() string {
	l := r.lines()
	if len(l) == 0 {
		return ""
	}
	return l[len(l)-1]
}

func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%q", err, r.stdout)
	}
	return m
}

func (s *sandbox) run(args ...string) result {
	s.t.Helper()
	cmd := exec.Command(msgateBin, args...)
	cmd.Dir = s.t.TempDir()
	cmd.Env = []string{"PATH=" + s.path, "HOME=" + s.t.TempDir(), "MSGATE_HOME=" + s.home}
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(s.stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			s.t.Fatalf("run %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

// gitRepo returns a directory that is a git work tree.
func gitRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	cmd := exec.Command("git", "init", "-q", d)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return d
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

type fakeRecord struct {
	Argv  []string          `json:"argv"`
	Stdin string            `json:"stdin"`
	Env   map[string]string `json:"env"`
	Cwd   string            `json:"cwd"`
}

func readRecord(t *testing.T, path string) fakeRecord {
	t.Helper()
	var r fakeRecord
	if err := json.Unmarshal([]byte(readFile(t, path)), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
