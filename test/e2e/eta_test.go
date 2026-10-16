package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// etaRecord is a run record of the code-review scenario that ran in cwd.
func etaRecord(t *testing.T, id, ts, cwd string, durationMS int) string {
	t.Helper()
	dir, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return recLine(id, ts, fmt.Sprintf(`"scenario":"code-review","cwd":%s,"duration_ms":%d,"outcome":"ok"`, dir, durationMS))
}

// seedETA writes the records into the current month file of the sandbox home.
func seedETA(t *testing.T, s *sandbox, records ...func(ts string) string) {
	t.Helper()
	ts := tsAgo(time.Hour)
	lines := make([]string, len(records))
	for i, rec := range records {
		lines[i] = rec(ts)
	}
	writeMonthFile(t, s.home, monthOf(ts), lines...)
}

func etaRun(t *testing.T, id, cwd string, durationMS int) func(ts string) string {
	return func(ts string) string { return etaRecord(t, id, ts, cwd, durationMS) }
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=eta-test", "-c", "user.email=eta-test@example.com"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestETARepoKeyIsSharedByWorktreesAndSubdirectories(t *testing.T) {
	s := newSandbox(t)
	repo := gitRepo(t)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "--detach", worktree)
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	seedETA(t, s, etaRun(t, "e1", repo, 100000), etaRun(t, "e2", worktree, 200000))
	wantKey := filepath.Join(realPath(t, repo), ".git")

	for name, from := range map[string]string{"main work tree": repo, "worktree": worktree, "subdirectory": sub} {
		j := s.run("eta", "--scenario", "code-review", "--cwd", from, "--json").json(t)
		if j["repo"] != wantKey || j["basis"] != "repo" || j["eta_ms"] != float64(150000) || j["samples"] != float64(2) || j["repos"] != float64(1) {
			t.Errorf("from the %s: got %v, want repo %s, basis repo, eta_ms 150000, samples 2, repos 1", name, j, wantKey)
		}
	}
}

func TestETARepoKeyFallsBackToTheDirectoryItself(t *testing.T) {
	s := newSandbox(t)
	plain := t.TempDir()
	gone := filepath.Join(t.TempDir(), "removed")
	seedETA(t, s, etaRun(t, "e1", plain, 1000), etaRun(t, "e2", gone, 3000))

	for name, c := range map[string]struct {
		dir, key string
		ms       float64
	}{"not a repository": {plain, realPath(t, plain), 1000}, "directory gone": {gone, gone, 3000}} {
		j := s.run("eta", "--scenario", "code-review", "--cwd", c.dir, "--json").json(t)
		if j["repo"] != c.key || j["basis"] != "repo" || j["eta_ms"] != c.ms || j["samples"] != float64(1) || j["repos"] != float64(1) {
			t.Errorf("%s: got %v, want repo %s, basis repo, eta_ms %v, samples 1, repos 1", name, j, c.key, c.ms)
		}
	}
}

func TestETARepoKeyResolvesSymbolicLinks(t *testing.T) {
	s := newSandbox(t)
	dir := realPath(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	seedETA(t, s, etaRun(t, "e1", dir, 20000), etaRun(t, "e2", link, 40000))

	for name, from := range map[string]string{"the directory": dir, "a link to it": link} {
		j := s.run("eta", "--scenario", "code-review", "--cwd", from, "--json").json(t)
		if j["repo"] != dir || j["basis"] != "repo" || j["eta_ms"] != float64(30000) || j["samples"] != float64(2) || j["repos"] != float64(1) {
			t.Errorf("from %s: got %v, want repo %s, basis repo, eta_ms 30000, samples 2, repos 1", name, j, dir)
		}
	}
}

// etaRuns seeds one run per duration, all in cwd.
func etaRuns(t *testing.T, prefix, cwd string, durationsMS ...int) []func(ts string) string {
	var runs []func(ts string) string
	for i, ms := range durationsMS {
		runs = append(runs, etaRun(t, fmt.Sprintf("%s-%d", prefix, i), cwd, ms))
	}
	return runs
}

func TestETAWithoutScenarioIsAUsageError(t *testing.T) {
	s := newSandbox(t)

	r := s.run("eta", "--json")

	if r.code != 2 {
		t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	if j := r.json(t); j["sdk_status"] != "usage_error" || j["exit_code"] != float64(2) {
		t.Errorf("error object = %v, want sdk_status usage_error and exit_code 2", j)
	}
	if r := s.run("eta"); r.code != 2 {
		t.Errorf("without --json: exit %d, want 2", r.code)
	}
}

func TestETAJSONHasTheDocumentedKeysAndValues(t *testing.T) {
	s := newSandbox(t)
	dir := t.TempDir()
	// Six runs of 150000 ms and six of 162000 ms average 156000 ms.
	seedETA(t, s, append(etaRuns(t, "a", dir, 150000, 150000, 150000, 150000, 150000, 150000),
		etaRuns(t, "b", dir, 162000, 162000, 162000, 162000, 162000, 162000)...)...)

	r := s.run("eta", "--scenario", "code-review", "--cwd", dir, "--json")

	want := fmt.Sprintf(`{"sdk_status":"ok","exit_code":0,"scenario":"code-review","repo":%q,"basis":"repo","eta_ms":156000,"samples":12,"repos":1}`+"\n", realPath(t, dir))
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d, stdout %q\nwant      %q", r.code, r.stdout, want)
	}
}

func TestETAJSONWithoutHistoryHasANullEstimate(t *testing.T) {
	s := newSandbox(t)
	dir := t.TempDir()

	r := s.run("eta", "--scenario", "code-review", "--cwd", dir, "--json")

	want := fmt.Sprintf(`{"sdk_status":"ok","exit_code":0,"scenario":"code-review","repo":%q,"basis":"none","eta_ms":null,"samples":0,"repos":0}`+"\n", realPath(t, dir))
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d, stdout %q\nwant      %q", r.code, r.stdout, want)
	}
}

func TestETATextLines(t *testing.T) {
	asked := t.TempDir()
	elsewhere := func(n int, ms ...int) []func(ts string) string {
		var runs []func(ts string) string
		for i := 0; i < n; i++ {
			runs = append(runs, etaRuns(t, fmt.Sprintf("r%d", i), t.TempDir(), ms[i%len(ms)])...)
		}
		return runs
	}
	cases := []struct {
		name string
		seed []func(ts string) string
		want string
	}{
		{"repo average", append(etaRuns(t, "x", asked, 150000, 150000, 150000, 150000, 150000, 150000),
			etaRuns(t, "y", asked, 162000, 162000, 162000, 162000, 162000, 162000)...), "ETA ~2m 36s (repo average, 12 runs)"},
		{"global average", elsewhere(8, 140000, 146000), "ETA ~2m 23s (global average, 8 repos)"},
		{"one run, under a minute", etaRuns(t, "x", asked, 59999), "ETA ~59s (repo average, 1 run)"},
		{"one repo, a whole minute", elsewhere(1, 60000), "ETA ~1m 0s (global average, 1 repo)"},
		{"no history", nil, "ETA unknown (no history)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t)
			if c.seed != nil {
				seedETA(t, s, c.seed...)
			}
			r := s.run("eta", "--scenario", "code-review", "--cwd", asked)
			if r.code != 0 || r.stdout != c.want+"\n" {
				t.Errorf("exit %d, stdout %q, want %q", r.code, r.stdout, c.want+"\n")
			}
		})
	}
}

func TestETACwdDefaultsToTheWorkingDirectory(t *testing.T) {
	s := newSandbox(t)
	here, other := t.TempDir(), t.TempDir()
	seedETA(t, s, etaRun(t, "e1", here, 30000), etaRun(t, "e2", other, 90000))
	cmd := exec.Command(msgateBin, "eta", "--scenario", "code-review", "--json")
	cmd.Dir = here
	cmd.Env = []string{"PATH=" + s.path, "HOME=" + t.TempDir(), "MSGATE_HOME=" + s.home}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	var j map[string]any
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%q", err, out.String())
	}
	if want := realPath(t, here); j["repo"] != want || j["basis"] != "repo" || j["eta_ms"] != float64(30000) {
		t.Errorf("got %v, want repo %s, basis repo, eta_ms 30000", j, want)
	}
}

func TestETARelativeCwdIsResolvedAgainstTheWorkingDirectory(t *testing.T) {
	s := newSandbox(t)
	parent := realPath(t, t.TempDir())
	project := filepath.Join(parent, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	seedETA(t, s, etaRun(t, "e1", project, 20000), etaRun(t, "e2", project, 40000))
	cmd := exec.Command(msgateBin, "eta", "--scenario", "code-review", "--cwd", "project", "--json")
	cmd.Dir = parent
	cmd.Env = []string{"PATH=" + s.path, "HOME=" + t.TempDir(), "MSGATE_HOME=" + s.home}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	var j map[string]any
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%q", err, out.String())
	}
	if j["repo"] != project || j["basis"] != "repo" || j["eta_ms"] != float64(30000) || j["samples"] != float64(2) {
		t.Errorf("got %v, want repo %s, basis repo, eta_ms 30000, samples 2", j, project)
	}
}

func TestETAReadsTheWholeHistory(t *testing.T) {
	s := newSandbox(t)
	dir := t.TempDir()
	old, recent := tsAgo(120*24*time.Hour), tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(old), etaRecord(t, "old", old, dir, 40000))
	writeMonthFile(t, s.home, monthOf(recent), etaRecord(t, "recent", recent, dir, 20000))

	j := s.run("eta", "--scenario", "code-review", "--cwd", dir, "--json").json(t)

	if j["basis"] != "repo" || j["eta_ms"] != float64(30000) || j["samples"] != float64(2) {
		t.Errorf("got %v, want basis repo, eta_ms 30000, samples 2 (the four-month-old run counts)", j)
	}
}

func TestETARepoKeyFallsBackToTheDirectoryWhenGitCannotRun(t *testing.T) {
	s := newSandbox(t).withoutProvider()
	repo := gitRepo(t)
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	seedETA(t, s, etaRun(t, "e1", sub, 50000))

	j := s.run("eta", "--scenario", "code-review", "--cwd", repo, "--json").json(t)

	// With git on PATH both directories share one key; without it each is its own.
	if want := realPath(t, repo); j["repo"] != want || j["basis"] != "global" || j["eta_ms"] != float64(50000) || j["samples"] != float64(1) || j["repos"] != float64(1) {
		t.Errorf("got %v, want repo %s, basis global, eta_ms 50000, samples 1, repos 1", j, want)
	}
}
