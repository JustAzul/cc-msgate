package store_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func newRunDir(t *testing.T, id string) *store.Store {
	t.Helper()
	s := store.Open(t.TempDir())
	if err := s.CreateRunDir(id); err != nil {
		t.Fatal(err)
	}
	return s
}

func heldState(t *testing.T, s *store.Store, id string) bool {
	t.Helper()
	held, err := s.RunLockHeld(id)
	if err != nil {
		t.Fatal(err)
	}
	return held
}

func TestRunLockIsHeldUntilClosed(t *testing.T) {
	s := newRunDir(t, "r1")
	if heldState(t, s, "r1") {
		t.Fatal("a run that never took its lock reads as held")
	}
	lock, err := s.LockRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if !heldState(t, s, "r1") {
		t.Error("the lock is not observable as held while the run owns it")
	}
	if _, err := s.LockRun("r1"); !errors.Is(err, store.ErrRunLockHeld) {
		t.Errorf("second LockRun = %v, want ErrRunLockHeld", err)
	}
	if info, err := os.Stat(filepath.Join(s.RunDir("r1"), "lock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("lock file = %v, %v; want mode 0600", info, err)
	}
	lock.Close()
	if heldState(t, s, "r1") {
		t.Error("the lock is still held after Close")
	}
}

func TestRunLockProbeDoesNotCreateTheFile(t *testing.T) {
	s := newRunDir(t, "r1")
	heldState(t, s, "r1")
	if _, err := os.Stat(filepath.Join(s.RunDir("r1"), "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("probing created the lock file: %v", err)
	}
}

func TestRunLockIsNotInheritedByChildren(t *testing.T) {
	s := newRunDir(t, "r1")
	lock, err := s.LockRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	if !heldState(t, s, "r1") {
		t.Fatal("the lock is not held while the run owns it")
	}
	lock.Close()
	if heldState(t, s, "r1") {
		t.Error("a child process inherited the run lock")
	}
}

func TestCancelRequestIsAFilePresence(t *testing.T) {
	s := newRunDir(t, "r1")
	if s.CancelRequested("r1") {
		t.Fatal("cancel requested before any request file exists")
	}
	if err := os.WriteFile(s.CancelRequestPath("r1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.CancelRequested("r1") {
		t.Error("the request file is not seen")
	}
	if filepath.Dir(s.CancelRequestPath("r1")) != s.RunDir("r1") {
		t.Errorf("request path %q is outside the run directory", s.CancelRequestPath("r1"))
	}
}
