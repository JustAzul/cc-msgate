package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
)

var (
	importBinOnce sync.Once
	importBin     string
	importBinErr  error
)

// importLegacyBin builds tools/import-legacy once per test run.
func importLegacyBin(t *testing.T) string {
	t.Helper()
	importBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "import-legacy-*")
		if err != nil {
			importBinErr = err
			return
		}
		importBin = filepath.Join(dir, "import-legacy")
		cmd := exec.Command("go", "build", "-trimpath", "-o", importBin, "./tools/import-legacy")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			importBinErr = fmt.Errorf("build import-legacy: %v\n%s", err, out)
		}
	})
	if importBinErr != nil {
		t.Fatal(importBinErr)
	}
	return importBin
}

// runImport runs the import tool with exactly the given environment entries.
func runImport(t *testing.T, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(importLegacyBin(t), args...)
	cmd.Dir = t.TempDir()
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run import-legacy: %v", err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

func legacyFixture() string { return filepath.Join(repoRoot, "testdata", "legacy", "legacy.jsonl") }

func importInto(t *testing.T, home string, from string, extra ...string) result {
	t.Helper()
	args := append([]string{"--from", from, "--home", home}, extra...)
	return runImport(t, []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}, args...)
}

func legacyRunID(line string, occurrence int) string {
	sum := sha256.Sum256([]byte(line + "\n" + strconv.Itoa(occurrence)))
	return "legacy-" + hex.EncodeToString(sum[:])[:16]
}

func TestLegacyImportSummaryAndRerun(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	r := importInto(t, home, legacyFixture())
	if r.code != 0 || strings.TrimSpace(r.stdout) != "imported=22 skipped_existing=0 unparseable=2" {
		t.Fatalf("first import: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	before := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(telemetryDir(home), "*.jsonl"))
	for _, f := range files {
		before[f] = readFile(t, f)
	}
	r = importInto(t, home, legacyFixture())
	if r.code != 0 || strings.TrimSpace(r.stdout) != "imported=0 skipped_existing=22 unparseable=2" {
		t.Fatalf("re-run: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	for f, want := range before {
		if got := readFile(t, f); got != want {
			t.Errorf("%s changed on re-run", f)
		}
	}
	if recs := telemetryRecords(t, home); len(recs) != 22 {
		t.Errorf("records = %d, want 22", len(recs))
	}
}

func TestLegacyImportPlacesRecordsInTheMonthOfTheirTS(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if r := importInto(t, home, legacyFixture()); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	files, _ := filepath.Glob(filepath.Join(telemetryDir(home), "*.jsonl"))
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
		for _, line := range strings.Split(strings.TrimSuffix(readFile(t, f), "\n"), "\n") {
			var m map[string]any
			if err := jsonUnmarshal(line, &m); err != nil {
				t.Fatal(err)
			}
			if ts := m["ts"].(string); ts[:7]+".jsonl" != filepath.Base(f) {
				t.Errorf("%s holds a record with ts %s", filepath.Base(f), ts)
			}
		}
	}
	if !reflect.DeepEqual(names, []string{"2026-07.jsonl", "2026-08.jsonl", "2026-09.jsonl"}) {
		t.Errorf("month files = %v", names)
	}
}

func TestLegacyImportMapsFieldsByName(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if r := importInto(t, home, legacyFixture()); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(readFile(t, legacyFixture()), "\n"), "\n")
	var findingsLine string
	for _, l := range lines {
		if strings.Contains(l, `"output_file":"/work/repo-b/review-1.md"`) {
			findingsLine = l
		}
	}
	id := legacyRunID(findingsLine, 0)
	var rec map[string]any
	for _, r := range telemetryRecords(t, home) {
		if r["run_id"] == id {
			rec = r
		}
	}
	if rec == nil {
		t.Fatalf("no record %s", id)
	}
	want := map[string]any{
		"v": float64(1), "kind": "run", "run_id": id, "ts": "2026-08-03T12:30:00Z", "provider": "codex",
		"scenario": "code-review", "source": "hook-post-commit", "effort": "high", "exit_code": float64(0),
		"outcome": "findings", "duration_ms": float64(98000), "output_bytes": float64(1840),
		"output_file": "/work/repo-b/review-1.md", "error_excerpt": nil, "status": "ok",
		"provider_version": nil, "command": nil, "model": nil, "model_source": nil, "effort_source": nil,
		"sandbox": nil, "session_id": nil, "conversation_id": nil, "turn": nil, "cwd": nil, "background": nil,
		"timeout_s": nil, "usage": nil, "provider_session_id": nil,
		"attrs": map[string]any{"legacy": true, "review.findings": map[string]any{
			"total": float64(3), "critical": float64(0), "high": float64(1), "medium": float64(2), "low": float64(0), "violations": float64(1)}},
	}
	if !reflect.DeepEqual(rec, want) {
		t.Errorf("record\n got %v\nwant %v", rec, want)
	}
	if _, moved := rec["findings"]; moved {
		t.Error("findings was not moved into attrs")
	}
}

func TestLegacyImportKeepsByteIdenticalLinesAsDistinctRuns(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if r := importInto(t, home, legacyFixture()); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	var dup string
	for _, l := range strings.Split(readFile(t, legacyFixture()), "\n") {
		if strings.Contains(l, `"output_file":"/work/repo-a/review-3.md"`) {
			dup = l
		}
	}
	got := map[string]bool{}
	for _, r := range telemetryRecords(t, home) {
		if r["output_file"] == "/work/repo-a/review-3.md" {
			got[r["run_id"].(string)] = true
		}
	}
	want := map[string]bool{legacyRunID(dup, 0): true, legacyRunID(dup, 1): true, legacyRunID(dup, 2): true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ids of the identical lines = %v, want %v", got, want)
	}
	ids := map[string]bool{}
	for _, r := range telemetryRecords(t, home) {
		ids[r["run_id"].(string)] = true
	}
	if len(ids) != 22 {
		t.Errorf("distinct run ids = %d, want 22", len(ids))
	}
}

func TestLegacyImportLeavesExistingRecordsAlone(t *testing.T) {
	s := newSandbox(t)
	seedRun(t, s, "native1")
	before := oneRecord(t, s.home)
	if r := importInto(t, s.home, legacyFixture()); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	recs := telemetryRecords(t, s.home)
	if len(recs) != 23 {
		t.Fatalf("records = %d, want 23", len(recs))
	}
	found := false
	for _, r := range recs {
		if r["run_id"] == "native1" {
			found = reflect.DeepEqual(r, before)
		}
	}
	if !found {
		t.Error("the native record changed or vanished")
	}
}

func TestLegacyImportResolvesTheHomeLikeTheCLI(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"MSGATE_HOME", []string{"MSGATE_HOME=" + filepath.Join(base, "a")}, nil, filepath.Join(base, "a")},
		{"XDG_STATE_HOME", []string{"XDG_STATE_HOME=" + filepath.Join(base, "x")}, nil, filepath.Join(base, "x", "msgate")},
		{"default", []string{"HOME=" + filepath.Join(base, "h")}, nil, filepath.Join(base, "h", ".local", "state", "msgate")},
		{"--home wins", []string{"MSGATE_HOME=" + filepath.Join(base, "ignored")}, []string{"--home", filepath.Join(base, "o")}, filepath.Join(base, "o")},
	}
	for _, c := range cases {
		args := append([]string{"--from", legacyFixture()}, c.args...)
		r := runImport(t, append([]string{"PATH=/usr/bin:/bin"}, c.env...), args...)
		if r.code != 0 {
			t.Errorf("%s: exit %d: %s", c.name, r.code, r.stderr)
			continue
		}
		if files, _ := filepath.Glob(filepath.Join(c.want, "telemetry", "*.jsonl")); len(files) != 3 {
			t.Errorf("%s: no telemetry under %s", c.name, c.want)
		}
	}
}

func TestLegacyImportUsageAndIOErrors(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if r := importInto(t, home, ""); r.code != 2 {
		t.Errorf("missing --from: exit %d, want 2", r.code)
	}
	if r := runImport(t, []string{"PATH=/usr/bin:/bin"}, "--bogus"); r.code != 2 {
		t.Errorf("bad flag: exit %d, want 2", r.code)
	}
	if r := importInto(t, home, filepath.Join(t.TempDir(), "missing.jsonl")); r.code != 1 {
		t.Errorf("missing file: exit %d, want 1 (stderr %q)", r.code, r.stderr)
	}
	if r := runImport(t, []string{"PATH=/usr/bin:/bin"}, "--from", legacyFixture()); r.code != 1 {
		t.Errorf("no resolvable home: exit %d, want 1", r.code)
	}
	if exists(home) {
		t.Error("an error path created the home")
	}
}

func TestLegacyImportCountsUnplaceableRecordsAsUnparseable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	from := filepath.Join(t.TempDir(), "legacy.jsonl")
	body := "{\"source\":\"skill\",\"scenario\":\"x\"}\n{\"ts\":\"yesterday\",\"source\":\"skill\"}\n[1]\nnull\n{\"ts\":\"2026-08-01T00:00:00Z\",\"source\":\"skill\"}\n   \n"
	if err := os.WriteFile(from, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := importInto(t, home, from)
	if r.code != 0 || strings.TrimSpace(r.stdout) != "imported=1 skipped_existing=0 unparseable=4" {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestLegacyImportHoldsTheTelemetryLock(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(telemetryDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(telemetryDir(home), ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	r := importInto(t, home, legacyFixture(), "--lock-wait", "300ms")
	if r.code != 1 {
		t.Errorf("exit %d, want 1 while the lock is held (stdout %q stderr %q)", r.code, r.stdout, r.stderr)
	}
	if files, _ := filepath.Glob(filepath.Join(telemetryDir(home), "*.jsonl")); len(files) != 0 {
		t.Errorf("wrote %v without the lock", files)
	}
}

// keysOf is used by stats parity checks.
func keysOf(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
