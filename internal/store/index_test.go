package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func mustIndex(t *testing.T, s *store.Store, session string, runs ...string) {
	t.Helper()
	for _, r := range runs {
		if err := s.IndexRun(session, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionRunIDsListNewestFirst(t *testing.T) {
	s := store.Open(t.TempDir())
	mustIndex(t, s, "S", "a", "b", "c")
	mustIndex(t, s, "S2", "x")
	got, err := s.SessionRunIDs("S")
	if err != nil || !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Errorf("SessionRunIDs(S) = %v, %v; want [c b a]", got, err)
	}
}

func TestSessionWithoutAnIndexHasNoRuns(t *testing.T) {
	s := store.Open(t.TempDir())
	got, err := s.SessionRunIDs("never-seen")
	if err != nil || len(got) != 0 {
		t.Errorf("SessionRunIDs = %v, %v; want none", got, err)
	}
}

func TestIndexFileIsOneRunIDPerLine(t *testing.T) {
	home := t.TempDir()
	s := store.Open(home)
	mustIndex(t, s, "S", "a", "b")
	path := filepath.Join(home, "index", "sessions", "S")
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "a\nb\n" {
		t.Fatalf("index file = %q, %v; want one id per line", b, err)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, %v; want %v", p, info, err, want)
		}
	}
}

func TestEmptySessionIDUsesTheFixedKey(t *testing.T) {
	home := t.TempDir()
	s := store.Open(home)
	mustIndex(t, s, "", "a")
	if _, err := os.Stat(filepath.Join(home, "index", "sessions", "_")); err != nil {
		t.Errorf("no index under the fixed key: %v", err)
	}
	if got, _ := s.SessionRunIDs(""); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("SessionRunIDs(\"\") = %v", got)
	}
}

// A session id is caller supplied, so it must never place the index outside
// its directory or collide with the fixed key.
func TestUnsafeSessionIDsStayInsideTheIndexDirectory(t *testing.T) {
	for _, id := range []string{"../escape", "a/b", ".", "..", ".hidden", "_", strings.Repeat("x", 300)} {
		home := t.TempDir()
		s := store.Open(home)
		mustIndex(t, s, id, "r1")
		mustIndex(t, s, "", "r2")
		if got, _ := s.SessionRunIDs(id); !reflect.DeepEqual(got, []string{"r1"}) {
			t.Errorf("session %q lists %v, want only its own run", id, got)
		}
		dir := filepath.Join(home, "index", "sessions")
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 2 {
			t.Errorf("session %q: index dir holds %v, %v; want two files", id, entries, err)
		}
		if _, err := os.Stat(filepath.Join(home, "escape")); err == nil {
			t.Errorf("session %q wrote outside the index directory", id)
		}
	}
}

func TestConcurrentIndexAppendsLoseNoRun(t *testing.T) {
	s := store.Open(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.IndexRun("S", fmt.Sprintf("run-%02d", i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := s.SessionRunIDs("S")
	if err != nil || len(got) != 40 {
		t.Errorf("indexed %d runs, %v; want 40", len(got), err)
	}
}
