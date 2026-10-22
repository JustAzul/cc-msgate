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

func TestReserveNewThatCannotWriteTheRecordLeavesNothingBehind(t *testing.T) {
	home := t.TempDir()
	s := store.Open(home)
	dir := filepath.Join(home, "conversations")
	// A directory where the record belongs: the lock is taken, the write fails.
	if err := os.MkdirAll(filepath.Join(dir, "c1.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err == nil {
		t.Fatal("ReserveNew succeeded without being able to write the record")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"c1.json", "c1.lock"}; !reflect.DeepEqual(names, want) {
		t.Errorf("conversations dir holds %v, want %v: no record, temp file or partial write", names, want)
	}
	if inner, _ := os.ReadDir(filepath.Join(dir, "c1.json")); len(inner) != 0 {
		t.Errorf("the obstructing directory now holds %d entries", len(inner))
	}
	if ids, _ := s.ConversationIDs(); len(ids) != 0 {
		t.Errorf("conversations = %v, want none", ids)
	}
}

type adoption struct {
	holder store.Conversation
	turn   int
	err    error
}

// adoptSimultaneously has n adopters, each with a conversation of its own,
// adopt the same session at once.
func adoptSimultaneously(s *store.Store, n int, session string) []adoption {
	var wg sync.WaitGroup
	out := make([]adoption, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := newConversation("c-" + string(rune('a'+i)))
			c.ProviderSessionID = ptr(session)
			<-start
			out[i].holder, out[i].turn, out[i].err = store.Open(s.Home).ReserveAdopted(c, "r-"+string(rune('a'+i)), reserveAt)
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

func TestSimultaneousAdoptionsCreateExactlyOneConversation(t *testing.T) {
	s := store.Open(t.TempDir())
	const n = 24
	var created []string
	joined := map[string]int{} // by the holder they were given
	for i, a := range adoptSimultaneously(s, n, "00000000-0000-4000-8000-000000000004") {
		switch {
		case a.err != nil:
			t.Errorf("adopter %d: %v", i, a.err)
		case a.holder.ConversationID != "":
			joined[a.holder.ConversationID]++
		case a.turn != 1:
			t.Errorf("adopter %d created a conversation as turn %d", i, a.turn)
		default:
			created = append(created, "c-"+string(rune('a'+i)))
		}
	}
	if len(created) != 1 || joined[created[0]] != n-1 {
		t.Fatalf("created %v, joined %v, want one creator and %d adopters joining it", created, joined, n-1)
	}
	ids, err := s.ConversationIDs()
	if err != nil || !reflect.DeepEqual(ids, created) {
		t.Errorf("conversations = %v (%v), want only %v", ids, err, created)
	}
}

func ptr(s string) *string { return &s }

// A reader that finds a new conversation must find its first turn reserved,
// and a reserver that gets there first must not cost the creator its record.
func TestNewConversationIsNeverVisibleWithoutItsFirstTurn(t *testing.T) {
	const rounds = 300
	for i := 0; i < rounds; i++ {
		s := store.Open(t.TempDir())
		id := "c-new"
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for { // the creator always writes the record, so this ends, and asserts once
				c, err := s.ReadConversation(id)
				if err != nil {
					continue
				}
				if !reflect.DeepEqual(c.Turns, []string{"r-first"}) || c.ActiveRunID == nil || *c.ActiveRunID != "r-first" {
					t.Errorf("round %d: a reader found turns=%v active=%v, want the first turn reserved", i, c.Turns, c.ActiveRunID)
					return
				}
				var busy *store.BusyError
				if _, err := s.Reserve(id, "r-other", reserveAt); !errors.As(err, &busy) {
					t.Errorf("round %d: a reserver was admitted onto a new conversation: %v", i, err)
				}
				return
			}
		}()
		err := s.ReserveNew(newConversation(id), "r-first", reserveAt)
		wg.Wait()
		if err != nil {
			t.Fatalf("round %d: creating the conversation: %v", i, err)
		}
		got, err := s.ReadConversation(id)
		if err != nil || !reflect.DeepEqual(got.Turns, []string{"r-first"}) {
			t.Fatalf("round %d: conversation = %+v (%v), want it holding only r-first", i, got, err)
		}
	}
}

const adoptedSession = "00000000-0000-4000-8000-000000000004"

// pendingConversation stores c1, a conversation of codex whose first turn is
// running and has not reported its provider session.
func pendingConversation(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.ReserveNew(newConversation("c1"), "r1", reserveAt); err != nil {
		t.Fatal(err)
	}
}

func adopting(id string) store.Conversation {
	c := newConversation(id)
	c.ProviderSessionID = ptr(adoptedSession)
	return c
}

func TestAdoptionRefusesWhileATurnHasNotReportedItsSession(t *testing.T) {
	s := store.Open(t.TempDir())
	pendingConversation(t, s)

	holder, _, err := s.ReserveAdopted(adopting("c2"), "r2", reserveAt)

	if !errors.Is(err, store.ErrSessionPending) || holder.ConversationID != "" {
		t.Errorf("holder %q err %v, want ErrSessionPending and no holder", holder.ConversationID, err)
	}
	if ids, _ := s.ConversationIDs(); !reflect.DeepEqual(ids, []string{"c1"}) {
		t.Errorf("conversations = %v, want only c1", ids)
	}
}

func TestAdoptionPastThePendingWaitCreates(t *testing.T) {
	s := store.Open(t.TempDir())
	pendingConversation(t, s)

	holder, turn, err := s.ReserveAdoptedRegardless(adopting("c2"), "r2", reserveAt)

	if err != nil || holder.ConversationID != "" || turn != 1 {
		t.Errorf("holder %q turn %d err %v, want c2 created as turn 1", holder.ConversationID, turn, err)
	}
	if ids, _ := s.ConversationIDs(); len(ids) != 2 {
		t.Errorf("conversations = %v, want c1 and c2", ids)
	}
}

func TestAdoptionFindsThePendingConversationOnceItReportsItsSession(t *testing.T) {
	s := store.Open(t.TempDir())
	pendingConversation(t, s)
	if holder, err := s.PersistProviderSession("c1", adoptedSession, "t1"); err != nil || holder.ConversationID != "" {
		t.Fatalf("persisting: holder %q err %v", holder.ConversationID, err)
	}

	holder, _, err := s.ReserveAdopted(adopting("c2"), "r2", reserveAt)

	if err != nil || holder.ConversationID != "c1" {
		t.Errorf("holder %q err %v, want c1", holder.ConversationID, err)
	}
	if ids, _ := s.ConversationIDs(); !reflect.DeepEqual(ids, []string{"c1"}) {
		t.Errorf("conversations = %v, want only c1", ids)
	}
}

func TestPersistingASessionAnotherConversationHoldsWritesNothing(t *testing.T) {
	s := store.Open(t.TempDir())
	pendingConversation(t, s)
	if _, _, err := s.ReserveAdoptedRegardless(adopting("c2"), "r2", reserveAt); err != nil {
		t.Fatal(err)
	}

	holder, err := s.PersistProviderSession("c1", adoptedSession, "t1")

	if err != nil || holder.ConversationID != "c2" {
		t.Errorf("holder %q err %v, want c2", holder.ConversationID, err)
	}
	if c, _ := s.ReadConversation("c1"); c.ProviderSessionID != nil {
		t.Errorf("c1 holds %s, want no session", *c.ProviderSessionID)
	}
}

func TestPersistingTheSessionAConversationAlreadyHoldsSucceeds(t *testing.T) {
	s := store.Open(t.TempDir())
	if _, _, err := s.ReserveAdopted(adopting("c2"), "r2", reserveAt); err != nil {
		t.Fatal(err)
	}

	holder, err := s.PersistProviderSession("c2", adoptedSession, "t9")

	got, _ := s.ReadConversation("c2")
	if err != nil || holder.ConversationID != "" || got.UpdatedAt != "t9" {
		t.Errorf("holder %q err %v updated_at %q, want it recorded again", holder.ConversationID, err, got.UpdatedAt)
	}
}
