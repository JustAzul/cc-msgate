package store_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func TestUpdateProvidersKeepsEveryConcurrentWrite(t *testing.T) {
	s := store.Open(filepath.Join(t.TempDir(), "home"))
	const writers = 8
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.Open(s.Home).UpdateProviders(func(c *store.ProviderCache) {
				c.Providers[fmt.Sprintf("p%d", i)] = store.ProviderEntry{Available: true, Source: "check"}
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	got, err := s.ReadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Providers) != writers {
		t.Errorf("version %d, %d providers; want version 1 and %d providers", got.Version, len(got.Providers), writers)
	}
}

func TestProvidersRoundTripKeepsEveryField(t *testing.T) {
	s := store.Open(filepath.Join(t.TempDir(), "home"))
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	until := at.Add(time.Hour)
	want := store.ProviderEntry{Available: false, Reason: "rate-limited", CheckedAt: at, ResetsAt: &until, Source: "mark", Skipped: []string{"app-server: timed out"}}
	if err := s.UpdateProviders(func(c *store.ProviderCache) { c.Providers["codex"] = want }); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadProviders()
	if err != nil {
		t.Fatal(err)
	}
	e := got.Providers["codex"]
	if e.Available || e.Reason != want.Reason || !e.CheckedAt.Equal(at) || e.ResetsAt == nil || !e.ResetsAt.Equal(until) || e.Source != "mark" || len(e.Skipped) != 1 || e.Skipped[0] != want.Skipped[0] {
		t.Errorf("entry = %+v, want %+v", e, want)
	}
}

func TestReadProvidersMissingOrUnusableIsAnEmptyCache(t *testing.T) {
	cases := map[string][]byte{"missing": nil, "not json": []byte("{"), "other version": []byte(`{"version":2,"providers":{"codex":{}}}`)}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if content != nil {
				if err := os.WriteFile(filepath.Join(home, "providers.json"), content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := store.Open(home).ReadProviders()
			if err != nil || got.Version != 1 || got.Providers == nil || len(got.Providers) != 0 {
				t.Errorf("got %+v, %v; want an empty version 1 cache and no error", got, err)
			}
		})
	}
}

func TestLockProvidersRefresh(t *testing.T) {
	s := store.Open(filepath.Join(t.TempDir(), "home"))
	release, err := s.LockProvidersRefresh()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LockProvidersRefresh(); !errors.Is(err, store.ErrProvidersRefreshLockHeld) {
		t.Errorf("second lock: %v, want ErrProvidersRefreshLockHeld", err)
	}
	release()
	again, err := s.LockProvidersRefresh()
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}
