package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// checkFields fails for every key of want that got does not hold.
func checkFields(t *testing.T, what string, got map[string]any, want map[string]any) {
	t.Helper()
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s.%s = %v, want %v", what, k, got[k], w)
		}
	}
}

// assertTableRow checks a text listing of exactly a header and one row, and
// that the row holds every want.
func assertTableRow(t *testing.T, r result, wants ...string) {
	t.Helper()
	if r.code != 0 || len(r.lines()) != 2 {
		t.Fatalf("listing: exit %d stdout %q, want a header and one row", r.code, r.stdout)
	}
	for _, w := range wants {
		if !strings.Contains(r.lines()[1], w) {
			t.Errorf("row %q does not hold %q", r.lines()[1], w)
		}
	}
}

// seedRunFiles writes a run directory without running anything: its state
// and request files, plus output.md when output is non-nil. The run is also
// indexed under its request's session, as admission would have.
func seedRunFiles(t *testing.T, s *sandbox, id string, state, request map[string]any, output []byte) {
	t.Helper()
	seedRunFilesUnindexed(t, s, id, state, request, output)
	key, _ := request["session_id"].(string)
	if key == "" {
		key = "_"
	}
	appendIndex(t, s.home, key, id)
}

// seedRunFilesUnindexed is seedRunFiles for a run no session index lists.
func seedRunFilesUnindexed(t *testing.T, s *sandbox, id string, state, request map[string]any, output []byte) {
	t.Helper()
	dir := filepath.Join(s.home, "runs", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := map[string]any{
		"run_id": id, "conversation_id": "c-seed-" + id, "turn": 1, "state": "done", "background": false,
		"admitted_at": "2026-10-04T12:00:00Z", "started_at": "2026-10-04T12:00:01Z", "ended_at": "2026-10-04T12:00:02Z",
		"worker_pid": 0, "provider_pgid": 0, "provider_start_time": nil, "exit_code": 0, "outcome": "ok",
		"error_excerpt": nil, "unparsed_events": 0, "output_path": filepath.Join(dir, "output.md"), "run_dir": dir,
	}
	for k, v := range state {
		st[k] = v
	}
	req := map[string]any{"command": "exec", "provider": "codex", "purpose": "adhoc", "source": "cli", "session_id": ""}
	for k, v := range request {
		req[k] = v
	}
	for name, v := range map[string]any{"state.json": st, "request.json": req} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if output != nil {
		if err := os.WriteFile(filepath.Join(dir, "output.md"), output, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWaitExitsWithTheRecordedExit(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_EXIT", "1")
	if r := s.run("exec", "--run-id", "wx1", "q"); r.code != 1 {
		t.Fatalf("exec exit %d", r.code)
	}
	w := s.run("wait", "wx1")
	if w.code != 1 {
		t.Errorf("wait exit %d, want the recorded 1", w.code)
	}
	if want := filepath.Join(s.home, "runs", "wx1", "output.md"); w.stdout != want+"\n" {
		t.Errorf("wait stdout = %q, want the output path alone", w.stdout)
	}
}

func TestWaitOnASignalledForegroundRunExitsWith143(t *testing.T) {
	s := shutdownEnv(newSandbox(t), 500*time.Millisecond, 500*time.Millisecond).set("FAKECODEX_SLEEP_MS", "60000")
	p := s.start("wx2", "exec", "--run-id", "wx2", "q")
	waitForState(t, s, "wx2", "a running state with the provider group", hasProviderGroup)
	p.signal(syscall.SIGTERM)
	if r := p.wait(10 * time.Second); r.code != 143 {
		t.Fatalf("exec exit %d, want 143", r.code)
	}
	if w := s.run("wait", "wx2"); w.code != 143 {
		t.Errorf("wait exit %d, want the recorded 143", w.code)
	}
}

func TestWaitJSONPrintsTheRunLikeAForegroundRun(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	fg := s.run("exec", "--json", "--run-id", "wj1", "q")
	w := s.run("wait", "--json", "wj1")
	if w.code != 0 {
		t.Fatalf("wait exit %d, stderr %s", w.code, w.stderr)
	}
	got, want := w.json(t), fg.json(t)
	for _, k := range []string{"conversation_id", "run_id", "state", "outcome", "sdk_status", "provider_exit", "exit_code", "output_path", "run_dir"} {
		if got[k] != want[k] {
			t.Errorf("wait json %s = %v, the foreground run printed %v", k, got[k], want[k])
		}
	}
}

func TestWaitSeesAForegroundRunFromAnotherShell(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "1500")
	p := s.start("wf1", "exec", "--run-id", "wf1", "q")
	waitForState(t, s, "wf1", "a running state", hasProviderGroup)
	if r := s.run("status", "--json", "wf1"); r.code != 0 || r.json(t)["state"] != "running" {
		t.Errorf("status of the running foreground run: exit %d stdout %q", r.code, r.stdout)
	}
	if w := s.run("wait", "wf1"); w.code != 0 {
		t.Errorf("wait exit %d, stderr %s", w.code, w.stderr)
	}
	if fin := stateOf(t, s, "wf1"); fin["state"] != "done" {
		t.Errorf("wait returned while the state was %v", fin["state"])
	}
	p.wait(5 * time.Second)
}

func TestWaitTimeoutLeavesTheRunRunning(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "6000")
	cleanupJob(t, s, "wt1")
	if r := s.run("exec", "--background", "--run-id", "wt1", "q"); r.code != 0 {
		t.Fatalf("admission exit %d, stderr %s", r.code, r.stderr)
	}
	began := time.Now()
	w := s.run("wait", "--json", "--timeout", "1", "wt1")
	if w.code != 5 {
		t.Fatalf("wait exit %d, want 5; stderr %s", w.code, w.stderr)
	}
	if took := time.Since(began); took < time.Second || took > 4*time.Second {
		t.Errorf("wait took %v with --timeout 1", took)
	}
	checkFields(t, "json", w.json(t), map[string]any{"sdk_status": "wait_timeout", "exit_code": float64(5), "run_id": "wt1"})
	if st := stateOf(t, s, "wt1"); st["state"] != "running" {
		t.Errorf("state = %v, the run must be left running", st["state"])
	}
}

func TestStatusListsTheSessionsRunsNewestFirst(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	for _, c := range []struct{ id, session string }{{"st-a", "S"}, {"st-b", "S2"}, {"st-c", "S"}} {
		if r := s.run("exec", "--run-id", c.id, "--session-id", c.session, "--purpose", "cross-check", "q"); r.code != 0 {
			t.Fatalf("exec %s exit %d", c.id, r.code)
		}
		time.Sleep(1100 * time.Millisecond) // admitted_at has one-second resolution
	}
	r := s.run("status", "--session-id", "S", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	runs, _ := r.json(t)["runs"].([]any)
	var ids []string
	for _, e := range runs {
		ids = append(ids, e.(map[string]any)["run_id"].(string))
	}
	if strings.Join(ids, ",") != "st-c,st-a" {
		t.Errorf("run ids = %v, want [st-c st-a]", ids)
	}
	first := runs[0].(map[string]any)
	checkFields(t, "runs[0]", first, map[string]any{
		"state": "done", "purpose": "cross-check", "command": "exec", "session_id": "S", "background": false,
		"outcome": "ok", "sdk_status": "ok", "exit_code": float64(0),
	})
}

func TestStatusTextListsOneLinePerRun(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	seedRun(t, s, "stt-1", "--session-id", "S")
	assertTableRow(t, s.run("status", "--session-id", "S"), "stt-1", "done")
}

func TestStatusListsAtMostTwentyRuns(t *testing.T) {
	s := newSandbox(t)
	for i := 0; i < 23; i++ {
		seedRunFiles(t, s, fmt.Sprintf("many-%02d", i), map[string]any{"admitted_at": fmt.Sprintf("2026-10-04T12:00:%02dZ", i)}, nil, nil)
	}
	runs, _ := s.run("status", "--json").json(t)["runs"].([]any)
	if len(runs) != 20 {
		t.Fatalf("listed %d runs, want 20", len(runs))
	}
	if first, last := runs[0].(map[string]any)["run_id"], runs[19].(map[string]any)["run_id"]; first != "many-22" || last != "many-03" {
		t.Errorf("first = %v, last = %v, want many-22 and many-03", first, last)
	}
}

func TestStatusWithoutASessionMatchesRunsRecordedWithoutOne(t *testing.T) {
	s := newSandbox(t)
	seedRunFiles(t, s, "ns-1", nil, nil, nil)
	seedRunFiles(t, s, "ns-2", nil, map[string]any{"session_id": "S"}, nil)
	runs, _ := s.run("status", "--json").json(t)["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["run_id"] != "ns-1" {
		t.Errorf("runs = %v, want only ns-1", runs)
	}
}

func TestStatusOfOneRun(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
	seedRun(t, s, "one-1")
	j := s.run("status", "--json", "one-1").json(t)
	checkFields(t, "json", j, map[string]any{
		"run_id": "one-1", "state": "done", "outcome": "ok", "sdk_status": "ok", "provider_exit": float64(0),
		"turn": float64(1), "output_path": filepath.Join(s.home, "runs", "one-1", "output.md"),
	})
	if txt := s.run("status", "one-1"); txt.code != 0 || !strings.Contains(txt.stdout, "state: done") {
		t.Errorf("text status: exit %d stdout %q", txt.code, txt.stdout)
	}
}

func TestResultPrintsTheOutputFile(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_OUTPUT", "the answer\nsecond line")
	s.run("exec", "--run-id", "res1", "q")
	r := s.run("result", "res1")
	if r.code != 0 || r.stdout != "the answer\nsecond line" {
		t.Errorf("exit %d stdout %q", r.code, r.stdout)
	}
}

func TestResultPrintsALargeOutputFully(t *testing.T) {
	s := newSandbox(t)
	big := []byte(strings.Repeat("0123456789abcdef", 320*1024)) // 5 MiB
	seedRunFiles(t, s, "res-big", nil, nil, big)
	r := s.run("result", "res-big")
	if r.code != 0 || r.stdout != string(big) {
		t.Errorf("exit %d, printed %d bytes, want %d", r.code, len(r.stdout), len(big))
	}
}

func TestResultOfARunStillInProgressIsRefused(t *testing.T) {
	s := newSandbox(t)
	seedRunFiles(t, s, "res-live", map[string]any{"state": "running", "ended_at": nil, "exit_code": nil, "outcome": nil}, nil, []byte("partial"))
	holdLock(t, filepath.Join(s.home, "runs", "res-live", "lock")) // a worker is alive: the run is in progress, not lost
	r := s.run("result", "res-live")
	if r.code != 3 || r.stdout != "" || !strings.Contains(r.stderr, "res-live") {
		t.Errorf("exit %d stdout %q stderr %q, want 3, nothing printed and the run named", r.code, r.stdout, r.stderr)
	}
}

func TestResultOfARunWithoutAnOutputFilePrintsNothing(t *testing.T) {
	s := newSandbox(t)
	seedRunFiles(t, s, "res-none", map[string]any{"state": "failed", "exit_code": 127, "outcome": "error"}, nil, nil)
	r := s.run("result", "res-none")
	if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "no output") {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestConversationsListsProviderTurnsAndStatus(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	conv := startConversation(t, s, "cv1", "--purpose", "second-opinion")
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	if r := s.run("send", conv, "--run-id", "cv2", "again"); r.code != 0 {
		t.Fatalf("send exit %d, stderr %s", r.code, r.stderr)
	}
	r := s.run("conversations", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
	}
	list, _ := r.json(t)["conversations"].([]any)
	if len(list) != 1 {
		t.Fatalf("conversations = %v", list)
	}
	c := list[0].(map[string]any)
	checkFields(t, "conversation", c, map[string]any{
		"conversation_id": conv, "provider": "codex", "turns": float64(2), "status": "idle", "resumable": true,
	})
	if at, _ := c["last_activity"].(string); at == "" {
		t.Errorf("last_activity = %v", c["last_activity"])
	}
	assertTableRow(t, s.run("conversations"), conv, "idle")
}

func TestConversationWithAnActiveRunIsBusy(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "2500")
	cleanupJob(t, s, "cb1")
	if r := s.run("exec", "--background", "--run-id", "cb1", "q"); r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	list, _ := s.run("conversations", "--json").json(t)["conversations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["status"] != "busy" {
		t.Errorf("conversations = %v, want one busy conversation", list)
	}
}

func TestConversationsWithNoneListsAnEmptyArray(t *testing.T) {
	r := newSandbox(t).run("conversations", "--json")
	list, ok := r.json(t)["conversations"].([]any)
	if r.code != 0 || !ok || len(list) != 0 {
		t.Errorf("exit %d stdout %q, want an empty array", r.code, r.stdout)
	}
}

func TestReadersRefuseUnknownIDsWithExit4(t *testing.T) {
	s := newSandbox(t)
	seedRunFiles(t, s, "known", nil, nil, []byte("x"))
	for _, id := range []string{"nope", "../known", "r-20260101T000000Z-deadbeef"} {
		for _, cmd := range []string{"status", "wait", "result", "cancel"} {
			if r := s.run(cmd, id); r.code != 4 {
				t.Errorf("%s %s: exit %d, want 4 (stderr %q)", cmd, id, r.code, r.stderr)
			}
		}
	}
	r := s.run("wait", "--json", "nope")
	checkFields(t, "json", r.json(t), map[string]any{"sdk_status": "not_found", "exit_code": float64(4)})
}
