package store

import (
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
	if err := os.MkdirAll(s.conversationsDir(), dirMode); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.conversationLockPath(conversationID), os.O_RDWR|os.O_CREATE, fileMode)
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

// ReserveNew creates the conversation with runID as its first, active turn. If
// the turn cannot be reserved the conversation is not left behind: a record
// with no turn would hold its provider session for nothing.
func (s *Store) ReserveNew(c Conversation, runID string, at time.Time) error {
	c.Turns = []string{}
	c.ActiveRunID = nil
	if err := s.CreateConversation(c); err != nil {
		return err
	}
	_, err := s.Reserve(c.ConversationID, runID, at)
	if err != nil {
		_ = os.Remove(s.conversationPath(c.ConversationID)) // the reservation error is the one to report
	}
	return err
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
