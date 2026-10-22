package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// BusyError means a turn of the conversation is already queued or running.
type BusyError struct{ ActiveRunID string }

func (e *BusyError) Error() string {
	return fmt.Sprintf("a turn of this conversation is still active (run %s)", e.ActiveRunID)
}

// lockConversation takes the conversation's exclusive lock, blocking until it
// is free, and returns the function that releases it. The lock descriptor is
// close-on-exec, so no child process inherits it.
func (s *Store) lockConversation(conversationID string) (func(), error) {
	return s.lockFile(s.conversationLockPath(conversationID))
}

// lockFile takes the exclusive lock held on the file at path, which lives in
// the conversations directory, and returns the function that releases it.
func (s *Store) lockFile(path string) (func(), error) {
	if err := os.MkdirAll(s.conversationsDir(), dirMode); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil // closing the descriptor drops the lock
}

// ReserveNew creates the conversation with runID as its first, active turn,
// in one write under the conversation's lock: no reader can find the record
// before its first turn is reserved, and a failed write leaves no record
// behind (the write replaces the file whole or not at all).
func (s *Store) ReserveNew(c Conversation, runID string, at time.Time) error {
	unlock, err := s.lockConversation(c.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	c.Turns = []string{runID}
	c.ActiveRunID = &runID
	c.UpdatedAt = at.UTC().Format(time.RFC3339)
	return s.CreateConversation(c)
}

// ReserveAdopted creates c, which holds the provider session c.ProviderSessionID
// from its creation, with runID as its first, active turn. The check that no
// conversation of the provider holds the session yet and the creation happen
// under one lock keyed on the provider and the session, so of any number of
// simultaneous adopters exactly one creates. Every other gets the holder back
// and creates nothing: holder is the zero Conversation when c was created, and
// turn is 1 then; with a holder the caller continues on it (Reserve).
// While a conversation of the provider has a turn running that has not yet
// reported its session, that turn may be the session's own: nothing is created
// and the error is ErrSessionPending, for the caller to try again later.
func (s *Store) ReserveAdopted(c Conversation, runID string, at time.Time) (holder Conversation, turn int, err error) {
	return s.adopt(c, runID, at, s.refusePending)
}

// ReserveAdoptedRegardless is ReserveAdopted without the refusal while a turn
// has not reported its session: for an adopter that has waited long enough.
func (s *Store) ReserveAdoptedRegardless(c Conversation, runID string, at time.Time) (holder Conversation, turn int, err error) {
	return s.adopt(c, runID, at, func(string) error { return nil })
}

// refusePending is ErrSessionPending when a conversation of the provider has
// a turn running that has not reported its provider session id.
func (s *Store) refusePending(providerName string) error {
	pending, err := s.hasPending(providerName)
	if err == nil && pending {
		return ErrSessionPending
	}
	return err
}

func (s *Store) adopt(c Conversation, runID string, at time.Time, mayCreate func(providerName string) error) (holder Conversation, turn int, err error) {
	if c.ProviderSessionID == nil {
		return Conversation{}, 0, errors.New("an adopted conversation holds a provider session")
	}
	unlock, err := s.lockFile(s.adoptionLockPath(c.Provider, *c.ProviderSessionID))
	if err != nil {
		return Conversation{}, 0, err
	}
	defer unlock()
	holder, err = s.FindByProviderSession(c.Provider, *c.ProviderSessionID)
	if err == nil {
		return holder, 0, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Conversation{}, 0, err
	}
	if err := mayCreate(c.Provider); err != nil {
		return Conversation{}, 0, err
	}
	return Conversation{}, 1, s.ReserveNew(c, runID, at)
}

// ErrSessionPending means a conversation of the provider has a turn running
// that has not yet reported its provider session id, so the session an adoption
// is after may be that turn's own.
var ErrSessionPending = errors.New("a turn of the provider has not reported its session yet")

// PersistProviderSession records sessionID as the provider session of the
// conversation, unless another conversation of its provider already holds it:
// then nothing is written and that holder is returned. It takes the adoption
// lock of the session first, the order ReserveAdopted uses, so a session is
// never recorded on one conversation while another is created to hold it.
func (s *Store) PersistProviderSession(conversationID, sessionID, stamp string) (holder Conversation, err error) {
	c, err := s.ReadConversation(conversationID)
	if err != nil {
		return Conversation{}, err
	}
	unlock, err := s.lockFile(s.adoptionLockPath(c.Provider, sessionID))
	if err != nil {
		return Conversation{}, err
	}
	defer unlock()
	holder, err = s.FindByProviderSession(c.Provider, sessionID)
	switch {
	case err == nil && holder.ConversationID != conversationID:
		return holder, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return Conversation{}, err
	}
	return Conversation{}, s.UpdateConversation(conversationID, func(c *Conversation) {
		c.ProviderSessionID = &sessionID
		c.UpdatedAt = stamp
	})
}

// hasPending reports whether a conversation of the provider has a turn running
// that has not reported its provider session id.
func (s *Store) hasPending(providerName string) (bool, error) {
	ids, err := s.ConversationIDs()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		c, err := s.ReadConversation(id)
		if errors.Is(err, os.ErrNotExist) {
			continue // released since the listing
		}
		if err != nil {
			return false, err
		}
		if c.Provider == providerName && c.ActiveRunID != nil && c.ProviderSessionID == nil {
			return true, nil
		}
	}
	return false, nil
}

// Reserve admits runID as the next turn of an existing conversation. Under the
// conversation lock it refuses a busy conversation with a *BusyError, then
// records runID as the active turn, appends it to the turn list and returns
// its turn number.
func (s *Store) Reserve(conversationID, runID string, at time.Time) (int, error) {
	unlock, err := s.lockConversation(conversationID)
	if err != nil {
		return 0, err
	}
	defer unlock()
	c, err := s.ReadConversation(conversationID)
	if err != nil {
		return 0, err
	}
	if c.ActiveRunID != nil {
		return 0, &BusyError{ActiveRunID: *c.ActiveRunID}
	}
	c.Turns = append(c.Turns, runID)
	c.ActiveRunID = &runID
	c.UpdatedAt = at.UTC().Format(time.RFC3339)
	if err := s.writeJSON(s.conversationPath(conversationID), c); err != nil {
		return 0, err
	}
	return len(c.Turns), nil
}

// Release undoes a reservation whose run never started: it drops runID from
// the turn list and clears the active marker if it names runID. A
// conversation left with no turns is removed.
func (s *Store) Release(conversationID, runID string) error {
	unlock, err := s.lockConversation(conversationID)
	if err != nil {
		return err
	}
	defer unlock()
	c, err := s.ReadConversation(conversationID)
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(c.Turns))
	for _, id := range c.Turns {
		if id != runID {
			kept = append(kept, id)
		}
	}
	c.Turns = kept
	if c.ActiveRunID != nil && *c.ActiveRunID == runID {
		c.ActiveRunID = nil
	}
	if len(c.Turns) == 0 {
		return os.Remove(s.conversationPath(conversationID))
	}
	return s.writeJSON(s.conversationPath(conversationID), c)
}
