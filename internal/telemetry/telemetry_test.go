package telemetry_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func sample(runID string) telemetry.Record {
	return telemetry.Record{V: 1, Kind: "run", RunID: runID, TS: "2026-09-30T23:59:50Z", Provider: "codex", Command: "exec"}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestAppendUsesTheUTCMonthOfTheAppend(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 10, 1, 0, 0, 5, 0, time.UTC)
	if err := telemetry.Append(home, sample("r1"), at, telemetry.Options{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "telemetry", "2026-10.jsonl")
	lines := readLines(t, file)
	if len(lines) != 1 {
		t.Fatalf("lines = %q", lines)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil || m["run_id"] != "r1" {
		t.Fatalf("line = %q (%v)", lines[0], err)
	}
	if m["attrs"] == nil || !reflect.DeepEqual(m["attrs"], map[string]any{}) {
		t.Errorf("attrs = %#v, want {}", m["attrs"])
	}
	// A zone-shifted clock still lands in the UTC month.
	zone := time.FixedZone("x", -3*3600)
	if err := telemetry.Append(home, sample("r2"), time.Date(2026, 9, 30, 22, 0, 0, 0, zone), telemetry.Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readLines(t, file); len(got) != 2 {
		t.Errorf("22:00 at -03:00 on Sep 30 is Oct 1 UTC; %s has %d lines", file, len(got))
	}
}

func TestAppendModes(t *testing.T) {
	home := t.TempDir()
	if err := telemetry.Append(home, sample("r1"), time.Now(), telemetry.Options{}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "telemetry")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("telemetry dir mode %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", e.Name(), info.Mode().Perm())
		}
	}
	if len(names) != 2 {
		t.Errorf("entries = %v, want the lock and one month file", names)
	}
}

func TestAppendStartsOnNewLineAfterTornTail(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "telemetry")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "2026-10.jsonl")
	if err := os.WriteFile(file, []byte(`{"partial`), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if err := telemetry.Append(home, sample("r1"), at, telemetry.Options{}); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, file)
	if len(lines) != 2 || lines[0] != `{"partial` {
		t.Fatalf("lines = %q", lines)
	}
	if err := json.Unmarshal([]byte(lines[1]), &map[string]any{}); err != nil {
		t.Errorf("new record does not parse: %v", err)
	}
}

func TestAppendAddsNoBlankLineAfterCleanTail(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b"} {
		if err := telemetry.Append(home, sample(id), at, telemetry.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	for i, l := range readLines(t, filepath.Join(home, "telemetry", "2026-10.jsonl")) {
		if l == "" {
			t.Errorf("line %d is blank", i)
		}
	}
}

func TestAppendGivesUpWhenTheLockIsHeld(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "telemetry")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = telemetry.Append(home, sample("r1"), time.Now(), telemetry.Options{LockWait: 300 * time.Millisecond})
	if !errors.Is(err, telemetry.ErrLockTimeout) {
		t.Fatalf("err = %v, want ErrLockTimeout", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("waited %v", d)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a record file was created while the lock was held: %v", entries)
	}
	// Once released, the append succeeds.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Append(home, sample("r2"), time.Now(), telemetry.Options{LockWait: 300 * time.Millisecond}); err != nil {
		t.Errorf("after release: %v", err)
	}
}

func TestAppendReportsAnUnwritableDirectory(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "telemetry")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	err := telemetry.Append(home, sample("r1"), time.Now(), telemetry.Options{})
	if err == nil || errors.Is(err, telemetry.ErrLockTimeout) {
		t.Fatalf("err = %v, want a write error", err)
	}
}
