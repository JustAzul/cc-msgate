package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

var pruneNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) *string {
	s := pruneNow.AddDate(0, 0, -d).Format(time.RFC3339)
	return &s
}

// seedRun writes a run directory with a state and a 100-byte output.
func seedPruneRun(t *testing.T, s *store.Store, id, state string, endedAt *string) {
	t.Helper()
	if err := s.CreateRunDir(id); err != nil {
		t.Fatal(err)
	}
	st := store.State{RunID: id, ConversationID: "c-" + id, State: state, EndedAt: endedAt, AdmittedAt: pruneNow.Format(time.RFC3339)}
	if err := s.WriteState(id, st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.OutputPath(id), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pruneOpts(days int) store.PruneOptions {
	return store.PruneOptions{OlderThan: time.Duration(days) * 24 * time.Hour, Now: func() time.Time { return pruneNow }}
}

func mustPrune(t *testing.T, s *store.Store, o store.PruneOptions) store.PruneResult {
	t.Helper()
	res, err := s.Prune(o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func runsLeft(t *testing.T, s *store.Store) []string {
	t.Helper()
	ids, err := s.RunIDs()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	return ids
}

func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		n += info.Size()
	}
	return n
}

func TestPruneRemovesOnlyOldTerminalRuns(t *testing.T) {
	s := store.Open(t.TempDir())
	bad := "not a time"
	for _, c := range []struct {
		id, state string
		ended     *string
	}{
		{"old-done", "done", daysAgo(40)}, {"old-failed", "failed", daysAgo(31)}, {"old-cancelled", "cancelled", daysAgo(90)},
		{"old-timeout", "timeout", daysAgo(45)}, {"old-lost", "lost", daysAgo(45)},
		{"young-done", "done", daysAgo(29)}, {"running", "running", nil}, {"queued", "queued", nil},
		{"no-ended-at", "done", nil}, {"bad-ended-at", "failed", &bad},
	} {
		seedPruneRun(t, s, c.id, c.state, c.ended)
	}
	if err := s.CreateRunDir("no-state"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRunDir("bad-state"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.RunDir("bad-state"), "state.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, id := range []string{"old-done", "old-failed", "old-cancelled", "old-timeout", "old-lost"} {
		want += dirBytes(t, s.RunDir(id))
	}

	res := mustPrune(t, s, pruneOpts(30))

	wantRes := store.PruneResult{Removed: 5, Kept: 3, Skipped: 4, BytesFreed: want}
	if res != wantRes {
		t.Errorf("result = %+v, want %+v", res, wantRes)
	}
	wantLeft := []string{"bad-ended-at", "bad-state", "no-ended-at", "no-state", "queued", "running", "young-done"}
	if got := runsLeft(t, s); !reflect.DeepEqual(got, wantLeft) {
		t.Errorf("runs left = %v, want %v", got, wantLeft)
	}
}

func TestPrunedRunsAreNotFound(t *testing.T) {
	s := store.Open(t.TempDir())
	seedPruneRun(t, s, "old", "done", daysAgo(40))
	mustPrune(t, s, pruneOpts(30))
	if s.RunExists("old") {
		t.Error("the run directory is still there")
	}
	if _, err := s.ReadState("old"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadState = %v, want not-exist", err)
	}
}

func TestPruneKeepsARunWhoseLockIsHeld(t *testing.T) {
	s := store.Open(t.TempDir())
	seedPruneRun(t, s, "held", "done", daysAgo(40))
	lock, err := s.LockRun("held")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	res := mustPrune(t, s, pruneOpts(30))
	if res.Removed != 0 || res.Kept != 1 || !s.RunExists("held") {
		t.Errorf("result %+v, exists %v; want the held run kept", res, s.RunExists("held"))
	}
}

// A run that stops qualifying between the first look and the lock is kept.
func TestPruneRechecksUnderTheLock(t *testing.T) {
	cases := map[string]func(t *testing.T, s *store.Store, id string){
		"turns non-terminal": func(t *testing.T, s *store.Store, id string) {
			st, _ := s.ReadState(id)
			st.State, st.EndedAt = "running", nil
			mustWriteState(t, s, id, st)
		},
		"turns younger": func(t *testing.T, s *store.Store, id string) {
			st, _ := s.ReadState(id)
			st.EndedAt = daysAgo(1)
			mustWriteState(t, s, id, st)
		},
		"becomes the active turn": func(t *testing.T, s *store.Store, id string) {
			seedConversation(t, s, "c-"+id, &id)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := store.Open(t.TempDir())
			seedPruneRun(t, s, "r1", "done", daysAgo(40))
			o := pruneOpts(30)
			o.BeforeLock = func(id string) { change(t, s, id) }
			res := mustPrune(t, s, o)
			if res.Removed != 0 || res.Kept != 1 || !s.RunExists("r1") {
				t.Errorf("result %+v, exists %v; want the run kept", res, s.RunExists("r1"))
			}
		})
	}
}

func mustWriteState(t *testing.T, s *store.Store, id string, st store.State) {
	t.Helper()
	if err := s.WriteState(id, st); err != nil {
		t.Fatal(err)
	}
}

func seedConversation(t *testing.T, s *store.Store, id string, active *string) {
	t.Helper()
	c := store.Conversation{ConversationID: id, Provider: "codex", Cwd: "/work", ActiveRunID: active, Turns: []string{}}
	if err := s.CreateConversation(c); err != nil {
		t.Fatal(err)
	}
}

func TestPruneKeepsARunAConversationNamesAsActive(t *testing.T) {
	s := store.Open(t.TempDir())
	seedPruneRun(t, s, "named", "done", daysAgo(40))
	seedPruneRun(t, s, "other", "done", daysAgo(40))
	named := "named"
	seedConversation(t, s, "c-elsewhere", &named) // a conversation other than the run's own
	res := mustPrune(t, s, pruneOpts(30))
	if res.Removed != 1 || res.Kept != 1 || !s.RunExists("named") || s.RunExists("other") {
		t.Errorf("result %+v; want only the run no marker names removed", res)
	}
}

func TestPruneDryRunRemovesNothing(t *testing.T) {
	s := store.Open(t.TempDir())
	seedPruneRun(t, s, "old", "done", daysAgo(40))
	seedPruneRun(t, s, "new", "done", daysAgo(1))
	mustIndex(t, s, "S", "old", "new")
	want := dirBytes(t, s.RunDir("old"))
	o := pruneOpts(30)
	o.DryRun = true
	res := mustPrune(t, s, o)
	if res.Removed != 1 || res.Kept != 1 || res.BytesFreed != want {
		t.Errorf("result %+v; want one run counted with %d bytes", res, want)
	}
	if got := runsLeft(t, s); !reflect.DeepEqual(got, []string{"new", "old"}) {
		t.Errorf("runs left = %v", got)
	}
	for _, id := range []string{"old", "new"} {
		if _, err := os.Stat(filepath.Join(s.RunDir(id), "lock")); err == nil {
			t.Errorf("a dry run created the lock file of %s", id)
		}
	}
	if got, _ := s.SessionRunIDs("S"); len(got) != 2 {
		t.Errorf("a dry run changed the index: %v", got)
	}
}

func TestPruneLeavesTelemetryAndConversationsAlone(t *testing.T) {
	home := t.TempDir()
	s := store.Open(home)
	seedPruneRun(t, s, "old", "done", daysAgo(40))
	seedConversation(t, s, "c-old", nil)
	if err := os.MkdirAll(filepath.Join(home, "telemetry"), 0o700); err != nil {
		t.Fatal(err)
	}
	tel := filepath.Join(home, "telemetry", "2026-09.jsonl")
	if err := os.WriteFile(tel, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustPrune(t, s, pruneOpts(30))
	for _, p := range []string{tel, filepath.Join(home, "conversations", "c-old.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestPruneDropsAnIndexOnceNoneOfItsRunsRemain(t *testing.T) {
	s := store.Open(t.TempDir())
	seedPruneRun(t, s, "old-1", "done", daysAgo(40))
	seedPruneRun(t, s, "old-2", "done", daysAgo(40))
	seedPruneRun(t, s, "keep", "done", daysAgo(1))
	mustIndex(t, s, "gone", "old-1", "old-2")
	mustIndex(t, s, "mixed", "old-1", "keep")
	mustPrune(t, s, pruneOpts(30))
	if got, _ := s.SessionRunIDs("gone"); len(got) != 0 {
		t.Errorf("index of removed runs = %v, want none", got)
	}
	if _, err := os.Stat(filepath.Join(s.Home, "index", "sessions", "gone")); err == nil {
		t.Error("the emptied index file is still there")
	}
	if got, _ := s.SessionRunIDs("mixed"); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Errorf("index of mixed session = %v, want [keep]", got)
	}
}

func TestPruneStopsWhenItsBudgetIsSpent(t *testing.T) {
	s := store.Open(t.TempDir())
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		seedPruneRun(t, s, id, "done", daysAgo(40))
	}
	tick := pruneNow
	o := pruneOpts(30)
	o.Budget = 3 * time.Second
	o.Now = func() time.Time { tick = tick.Add(time.Second); return tick }
	first := mustPrune(t, s, o)
	if !first.Stopped || first.Removed == 0 || first.Removed >= 6 {
		t.Fatalf("first pass %+v; want it stopped with some runs removed", first)
	}
	second := mustPrune(t, s, pruneOpts(30))
	if second.Stopped || first.Removed+second.Removed != 6 || len(runsLeft(t, s)) != 0 {
		t.Errorf("second pass %+v after %+v; want the rest removed", second, first)
	}
}

func TestPruneSweepsDirectoriesAnEarlierPassLeftHalfRemoved(t *testing.T) {
	s := store.Open(t.TempDir())
	left := filepath.Join(s.Home, "runs", ".prune-old")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	mustPrune(t, s, pruneOpts(30))
	if _, err := os.Stat(left); err == nil {
		t.Error("the half-removed directory is still there")
	}
}
