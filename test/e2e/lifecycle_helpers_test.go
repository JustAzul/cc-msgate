package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// proc is a msgate process started in the background of a test.
type proc struct {
	t      *testing.T
	cmd    *exec.Cmd
	out    bytes.Buffer
	errb   bytes.Buffer
	done   chan struct{}
	waited result
	start  time.Time
}

// start launches msgate without waiting for it, with the same environment
// run builds. The process, and any provider group recorded for runID, are
// killed at cleanup so a failing test leaves nothing behind.
func (s *sandbox) start(runID string, args ...string) *proc {
	s.t.Helper()
	p := &proc{t: s.t, done: make(chan struct{})}
	p.cmd = exec.Command(msgateBin, args...)
	p.cmd.Dir = s.t.TempDir()
	p.cmd.Env = []string{"PATH=" + s.path, "HOME=" + s.t.TempDir(), "MSGATE_HOME=" + s.home}
	for k, v := range s.env {
		p.cmd.Env = append(p.cmd.Env, k+"="+v)
	}
	p.cmd.Stdin = strings.NewReader(s.stdin)
	p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.errb
	p.start = time.Now()
	if err := p.cmd.Start(); err != nil {
		s.t.Fatalf("start %v: %v", args, err)
	}
	go func() {
		err := p.cmd.Wait()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		p.waited = result{p.out.String(), p.errb.String(), code}
		close(p.done)
	}()
	s.t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			p.cmd.Process.Kill()
			<-p.done
		}
		if runID != "" {
			killRecordedGroup(s.home, runID)
		}
	})
	return p
}

func (p *proc) pid() int { return p.cmd.Process.Pid }

// wait returns the finished process's result, failing the test if it runs
// longer than limit.
func (p *proc) wait(limit time.Duration) result {
	p.t.Helper()
	select {
	case <-p.done:
		return p.waited
	case <-time.After(limit):
		p.t.Fatalf("msgate still running after %v (stderr so far: %q)", limit, p.errb.String())
		return result{}
	}
}

func (p *proc) signal(sig syscall.Signal) {
	p.t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		p.t.Fatalf("signal %v: %v", sig, err)
	}
}

// killRecordedGroup SIGKILLs the provider group a run's state recorded.
func killRecordedGroup(home, runID string) {
	st, err := os.ReadFile(filepath.Join(home, "runs", runID, "state.json"))
	if err != nil {
		return
	}
	var m map[string]any
	if jsonUnmarshal(string(st), &m) != nil {
		return
	}
	if pgid, _ := m["provider_pgid"].(float64); pgid > 1 {
		syscall.Kill(-int(pgid), syscall.SIGKILL)
	}
}

// waitForState polls a run's state.json until ok accepts it.
func waitForState(t *testing.T, s *sandbox, runID string, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(s.home, "runs", runID, "state.json"))
		if err == nil {
			var m map[string]any
			if jsonUnmarshal(string(b), &m) == nil {
				last = m
				if ok(m) {
					return m
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state.json never showed %s; last seen: %v", what, last)
	return nil
}

func hasProviderGroup(m map[string]any) bool {
	pgid, _ := m["provider_pgid"].(float64)
	st, _ := m["provider_start_time"].(string)
	return m["state"] == "running" && pgid > 0 && st != ""
}

// lockHeldElsewhere probes a flock-based lock file without blocking.
func lockHeldElsewhere(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening lock %s: %v", path, err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return false
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true
	}
	t.Fatalf("flock %s: %v", path, err)
	return false
}

// liveInGroup lists the pids of non-zombie processes in a process group. A
// zombie is gone for this purpose: in a container whose init does not reap,
// every killed orphan stays a zombie.
func liveInGroup(t *testing.T, pgid int) []int {
	t.Helper()
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		return liveInGroupPS(t, pgid)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var live []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		text := string(b)
		fields := strings.Fields(text[strings.LastIndexByte(text, ')')+1:])
		if len(fields) < 3 {
			continue
		}
		if fields[0] != "Z" && fields[2] == strconv.Itoa(pgid) {
			live = append(live, pid)
		}
	}
	return live
}

func liveInGroupPS(t *testing.T, pgid int) []int {
	t.Helper()
	out, err := exec.Command("ps", "-ax", "-o", "pid=,pgid=,stat=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var live []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[2], "Z") || f[1] != strconv.Itoa(pgid) {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		live = append(live, pid)
	}
	return live
}

// assertGroupGone fails unless no live process remains in the group within a
// short settling time.
func assertGroupGone(t *testing.T, pgid int) {
	t.Helper()
	var live []int
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if live = liveInGroup(t, pgid); len(live) == 0 {
			return
		}
	}
	t.Errorf("process group %d still has live processes: %v", pgid, live)
}

func stateOf(t *testing.T, s *sandbox, runID string) map[string]any {
	t.Helper()
	return readJSONFile(t, filepath.Join(s.home, "runs", runID, "state.json"))
}
