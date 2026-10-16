package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

var reserveAt = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func newConversation(id string) store.Conversation {
	return store.Conversation{ConversationID: id, Provider: "codex", Cwd: "/work/a", Turns: []string{}, CreatedAt: "t0", UpdatedAt: "t0"}
}

func TestReserveNewMakesFirstTurnActive(t *testing.T) {
	s := store.Open(t.TempDir())
	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadConversation("c1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Turns, []string{"r1"}) || got.ActiveRunID == nil || *got.ActiveRunID != "r1" {
		t.Errorf("turns=%v active=%v", got.Turns, got.ActiveRunID)
	}
}

func TestReserveAllocatesNextTurnOnceIdle(t *testing.T) {
	s := store.Open(t.TempDir())
	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err != nil {
		t.Fatal(err)
	}
	var busy *store.BusyError
	if _, err := s.Reserve("c1", "r2", reserveAt); !errors.As(err, &busy) || busy.ActiveRunID != "r1" {
		t.Fatalf("reserve while active: %v, want BusyError naming r1", err)
	}
	if err := s.UpdateConversation("c1", func(c *store.Conversation) { c.ActiveRunID = nil }); err != nil {
		t.Fatal(err)
	}
	turn, err := s.Reserve("c1", "r2", reserveAt)
	if err != nil || turn != 2 {
		t.Fatalf("turn=%d err=%v, want 2", turn, err)
	}
	got, _ := s.ReadConversation("c1")
	if !reflect.DeepEqual(got.Turns, []string{"r1", "r2"}) || got.ActiveRunID == nil || *got.ActiveRunID != "r2" {
		t.Errorf("turns=%v active=%v", got.Turns, got.ActiveRunID)
	}
}

func TestReserveUnknownConversation(t *testing.T) {
	s := store.Open(t.TempDir())
	if _, err := s.Reserve("nope", "r1", reserveAt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want not-exist", err)
	}
}

func TestSimultaneousReservationsAdmitExactlyOne(t *testing.T) {
	s := store.Open(t.TempDir())
	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateConversation("c1", func(c *store.Conversation) { c.ActiveRunID = nil }); err != nil {
		t.Fatal(err)
	}
	const n = 24
	var wg sync.WaitGroup
	results := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = store.Open(s.Home).Reserve("c1", "r-new-"+string(rune('a'+i)), reserveAt)
		}(i)
	}
	close(start)
	wg.Wait()
	won := 0
	for _, err := range results {
		var busy *store.BusyError
		switch {
		case err == nil:
			won++
		case !errors.As(err, &busy):
			t.Errorf("unexpected error %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d reservations admitted, want exactly 1", won)
	}
	got, _ := s.ReadConversation("c1")
	if len(got.Turns) != 2 {
		t.Errorf("turns = %v, want r1 plus one winner", got.Turns)
	}
}

func TestReleaseUndoesAReservation(t *testing.T) {
	s := store.Open(t.TempDir())
	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err != nil {
		t.Fatal(err)
	}
	_ = s.UpdateConversation("c1", func(c *store.Conversation) { c.ActiveRunID = nil })
	if _, err := s.Reserve("c1", "r2", reserveAt); err != nil {
		t.Fatal(err)
	}
	if err := s.Release("c1", "r2"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ReadConversation("c1")
	if !reflect.DeepEqual(got.Turns, []string{"r1"}) || got.ActiveRunID != nil {
		t.Errorf("after release turns=%v active=%v", got.Turns, got.ActiveRunID)
	}
}

func TestReleaseOfTheOnlyTurnRemovesTheConversation(t *testing.T) {
	s := store.Open(t.TempDir())
	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err != nil {
		t.Fatal(err)
	}
	if err := s.Release("c1", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(s.Home, "conversations", "c1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("conversation record still present: %v", err)
	}
}
