package store_test

import (
	"os"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func TestOpenWorkerLogCreatesAUserOnlyFile(t *testing.T) {
	s := newRunDir(t, "wl1")
	f, err := s.OpenWorkerLog("wl1")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	info, err := os.Stat(s.WorkerLogPath("wl1"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("worker.log: %v, %v; want a 0600 file", info, err)
	}
}

func TestOpenWorkerLogNeedsTheRunDirectory(t *testing.T) {
	if f, err := store.Open(t.TempDir()).OpenWorkerLog("absent"); err == nil {
		f.Close()
		t.Error("opened a worker log for a run that has no directory")
	}
}

func TestTailWriterKeepsTheEndOfWhatWasWritten(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{"under the limit", []string{"abc", "def"}, "abcdef"},
		{"exactly the limit", []string{"12345", "67890"}, "1234567890"},
		{"over the limit drops the oldest", []string{"abcdef", "ghijkl"}, "cdefghijkl"},
		{"many small writes", []string{"aaaa", "bbbb", "cccc", "dddd"}, "bbccccdddd"},
		{"one write over the limit", []string{"0123456789ABC"}, "3456789ABC"},
		{"a big write after others", []string{"xy", "0123456789ABC"}, "3456789ABC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newRunDir(t, "wl2")
			f, err := s.OpenWorkerLog("wl2")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			w := store.NewTailWriter(f, 10)
			for _, text := range tc.writes {
				if n, err := w.Write([]byte(text)); err != nil || n != len(text) {
					t.Fatalf("Write(%q) = %d, %v; want %d, nil", text, n, err, len(text))
				}
			}
			got, err := os.ReadFile(s.WorkerLogPath("wl2"))
			if err != nil || string(got) != tc.want {
				t.Errorf("worker.log = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}
