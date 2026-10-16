package store_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func TestLockPrices(t *testing.T) {
	s := store.Open(filepath.Join(t.TempDir(), "home"))
	release, err := s.LockPrices()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LockPrices(); !errors.Is(err, store.ErrPricesLockHeld) {
		t.Errorf("second lock: %v, want ErrPricesLockHeld", err)
	}
	release()
	again, err := s.LockPrices()
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}

func TestLockPricesCreatesTheHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "a", "home")
	release, err := store.Open(home).LockPrices()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("home: %v, %v; want a 0700 directory", info, err)
	}
	if info, err := os.Stat(filepath.Join(home, "prices.lock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("prices.lock: %v, %v; want a 0600 file", info, err)
	}
}

func TestReadPricesMissing(t *testing.T) {
	s := store.Open(filepath.Join(t.TempDir(), "missing"))
	if _, err := s.ReadPrices(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestWritePricesCreatesHomeAndFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "home")
	s := store.Open(home)
	want := []byte(`{"v": 1}` + "\n")
	if err := s.WritePrices(want); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("home: %v, %v; want a 0700 directory", info, err)
	}
	path := filepath.Join(home, "prices.json")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("prices.json: %v, %v; want a 0600 file", info, err)
	}
	got, err := s.ReadPrices()
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("ReadPrices = %q, %v; want %q", got, err, want)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Errorf("home holds %d entries, want only prices.json (no temp file left)", len(entries))
	}
}

func TestWritePricesReplacesAtomically(t *testing.T) {
	s := store.Open(t.TempDir())
	for _, body := range []string{"first\n", "second, longer\n", "3\n"} {
		if err := s.WritePrices([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ReadPrices(); err != nil || string(got) != body {
			t.Errorf("ReadPrices = %q, %v; want %q", got, err, body)
		}
	}
}
