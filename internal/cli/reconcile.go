package cli

import (
	"errors"
	"os"
	"time"

	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// defaultQueuedGrace is how long a queued run may go without a worker holding
// its lock before it counts as lost: past the admission wait, so an admission
// in progress is never misread.
const defaultQueuedGrace = 30 * time.Second

// queuedGrace is defaultQueuedGrace unless the test-only override
// MSGATE_TEST_QUEUED_GRACE_MS is set.
func queuedGrace(getenv func(string) string) time.Duration {
	if d, ok := envMillis(getenv, "MSGATE_TEST_QUEUED_GRACE_MS"); ok {
		return d
	}
	return defaultQueuedGrace
}

// observeRun is how every reader sees a run: its recorded state, after a
// non-terminal run whose worker is gone has been settled as lost.
func observeRun(ctx *Context, st *store.Store, runID string) (store.State, error) {
	state, err := st.ReadState(runID)
	if err != nil || !mayBeLost(ctx, state) {
		return state, err
	}
	held, err := st.RunLockHeld(runID)
	if err != nil {
		ctx.Warnf("could not probe the lock of run %s: %v", runID, err)
		return state, nil
	}
	if held {
		return state, nil
	}
	return settleLost(ctx, st, runID)
}

// mayBeLost reports whether a run in this state is lost when no worker holds
// its lock: it is running, or it has been queued past the admission window.
func mayBeLost(ctx *Context, s store.State) bool {
	switch s.State {
	case "running":
		return true
	case "queued":
		at, err := time.Parse(time.RFC3339, s.AdmittedAt)
		return err == nil && ctx.Now().Sub(at) > queuedGrace(ctx.Getenv)
	}
	return false
}

// settleLost marks a run lost. It takes the run lock first, so a reader racing
// it either waits out the settlement or sees the lock held, and re-reads the
// state under the lock: a worker that finished since the caller's probe, or a
// reader that settled the run first, leaves nothing to do.
func settleLost(ctx *Context, st *store.Store, runID string) (store.State, error) {
	lock, err := st.LockRun(runID)
	if errors.Is(err, store.ErrRunLockHeld) {
		return st.ReadState(runID) // a worker or another reader owns the run now
	}
	if err != nil {
		return store.State{}, err
	}
	defer lock.Close()
	cur, err := st.ReadState(runID)
	if err != nil || !mayBeLost(ctx, cur) {
		return cur, err
	}
	return runner.MarkLost(settleJob(ctx, st, cur)).State, nil
}

// settleJob is the runner job through which a process other than the run's
// worker finalizes the run.
func settleJob(ctx *Context, st *store.Store, state store.State) runner.Job {
	var req requestRecord
	if err := st.ReadRequest(state.RunID, &req); err != nil {
		ctx.Warnf("could not read the request of run %s: %v", state.RunID, err)
	}
	return runner.Job{
		Store: st, State: state, Record: telemetryRecord(req, state.Background),
		Warn: func(msg string) { ctx.Warnf("%s", msg) }, Now: ctx.Now,
	}
}

// readConversationReconciled reads a conversation after settling its active
// turn if that turn is lost, so a lost turn never leaves it busy.
func readConversationReconciled(ctx *Context, st *store.Store, conversationID string) (store.Conversation, error) {
	conv, err := st.ReadConversation(conversationID)
	if err != nil || conv.ActiveRunID == nil {
		return conv, err
	}
	if _, err := observeRun(ctx, st, *conv.ActiveRunID); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			ctx.Warnf("could not check the active run %s: %v", *conv.ActiveRunID, err)
		}
		return conv, nil
	}
	return st.ReadConversation(conversationID)
}
