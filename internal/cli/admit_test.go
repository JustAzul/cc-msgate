package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

const heldSession = "00000000-0000-4000-8000-000000000004"

// sessionProvider reads one session, whose model and directory differ from the
// conversation that holds it, and plans a command that does nothing.
type sessionProvider struct{ sessionCwd string }

func (sessionProvider) Name() string { return "sessionprov" }
func (sessionProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Commands: []string{"resume"}, Sandboxes: []string{"read-only"}}
}
func (sessionProvider) ReservedFlag(string) bool { return false }
func (sessionProvider) BuildPlan(provider.Request) (provider.Plan, error) {
	return provider.Plan{Argv: []string{"true"}}, nil
}
func (sessionProvider) ParseEvent([]byte) (provider.Event, bool) { return provider.Event{}, true }
func (sessionProvider) VersionArgs() []string                    { return nil }
func (p sessionProvider) ReadSession(_ []string, id string) (provider.Session, bool, error) {
	return provider.Session{Cwd: p.sessionCwd, Model: "session-model", Effort: "high"}, id == heldSession, nil
}

func init() { provider.Register(sessionProvider{sessionCwd: "/"}) }

var heldNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// idleHolder stores an idle conversation of sessionprov that holds heldSession.
func idleHolder(t *testing.T, st *store.Store, cwd string) {
	t.Helper()
	held := store.Conversation{
		ConversationID: "c-held", Provider: "sessionprov", ProviderSessionID: nullable(heldSession), Cwd: cwd,
		Defaults: store.Defaults{
			Purpose: "review", Model: "holder-model", Sandbox: "read-only",
			ModelSource: "flag", EffortSource: "provider-default", SandboxSource: "default",
		},
		CreatedAt: "t0", UpdatedAt: "t0",
	}
	if err := st.ReserveNew(held, "r1", heldNow); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateConversation("c-held", func(c *store.Conversation) { c.ActiveRunID = nil }); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptionThatFindsAnIdleHolderRunsWithTheHoldersDefaults(t *testing.T) {
	home, holderCwd := t.TempDir(), t.TempDir()
	st := store.Open(home)
	idleHolder(t, st, holderCwd)
	var out, errb bytes.Buffer
	ctx := &Context{
		Env: []string{"MSGATE_HOME=" + home}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return heldNow }, IDs: map[string]string{},
	}
	p, _, done := parseTurnArgs(ctx, sendSpec, []string{"--provider", "sessionprov", "--run-id", "r2", heldSession, "go on"})
	if done {
		t.Fatal("the arguments were not parsed")
	}
	p.positional = p.positional[1:]

	// The scan that precedes adoption found no holder; one appeared since.
	sendToProviderSession(ctx, st, p, heldSession)

	if ids, _ := st.ConversationIDs(); len(ids) != 1 {
		t.Errorf("conversations = %v, want only c-held", ids)
	}
	state, err := st.ReadState("r2")
	if err != nil {
		t.Fatal(err)
	}
	var req requestRecord
	if err := st.ReadRequest("r2", &req); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%s turn %d, model %s (%s), effort set %v, cwd %s, purpose %s",
		state.ConversationID, state.Turn, deref(req.Model), req.ModelSource, req.Effort != nil, req.Cwd, req.Purpose)
	want := fmt.Sprintf("c-held turn 2, model holder-model (flag), effort set false, cwd %s, purpose review", holderCwd)
	if got != want {
		t.Errorf("run r2 = %s\nwant   %s", got, want)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestAdoptionThatFindsABusyHolderIsRefusedOnTheHolder(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	idleHolder(t, st, t.TempDir())
	if _, err := st.Reserve("c-held", "r-active", heldNow); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	ctx := &Context{
		Env: []string{"MSGATE_HOME=" + home}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return heldNow }, IDs: map[string]string{},
	}
	p, _, _ := parseTurnArgs(ctx, sendSpec, []string{"--provider", "sessionprov", "--run-id", "r2", heldSession, "go on"})
	p.positional = p.positional[1:]

	code := sendToProviderSession(ctx, st, p, heldSession)

	if code != ExitBusy || ctx.IDs["conversation_id"] != "c-held" || ctx.IDs["run_id"] != "" {
		t.Errorf("exit %d ids %v, want busy on c-held with no run", code, ctx.IDs)
	}
	if ids, _ := st.ConversationIDs(); len(ids) != 1 {
		t.Errorf("conversations = %v, want only c-held", ids)
	}
	if _, err := st.ReadState("r2"); err == nil {
		t.Error("a refused send left a run")
	}
}

// A conversation that never came to exist is not named in the error.
func TestFailedAdmissionOfANewConversationDoesNotNameIt(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "runs"), nil, 0o600); err != nil { // no run directory can be made
		t.Fatal(err)
	}
	st := store.Open(home)
	var out, errb bytes.Buffer
	ctx := &Context{
		Env: []string{"MSGATE_HOME=" + home}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return heldNow }, IDs: map[string]string{}, JSON: true,
	}
	p, _, _ := parseTurnArgs(ctx, sendSpec, []string{"--provider", "sessionprov", "--run-id", "r2", heldSession, "go on"})
	p.positional = p.positional[1:]

	code := sendToProviderSession(ctx, st, p, heldSession)

	if code != ExitInternal {
		t.Fatalf("exit %d stderr %q, want %d", code, errb.String(), ExitInternal)
	}
	if id, ok := ctx.IDs["conversation_id"]; ok {
		t.Errorf("ids name conversation %s, which was not created", id)
	}
	if strings.Contains(out.String(), "conversation_id") {
		t.Errorf("the JSON error names a conversation: %s", out.String())
	}
	if ids, _ := st.ConversationIDs(); len(ids) != 0 {
		t.Errorf("conversations = %v, want none", ids)
	}
}

func pendingTurn(t *testing.T, st *store.Store) {
	t.Helper()
	pending := store.Conversation{ConversationID: "c-pending", Provider: "sessionprov", Cwd: "/", CreatedAt: "t0", UpdatedAt: "t0"}
	if err := st.ReserveNew(pending, "r-running", heldNow); err != nil {
		t.Fatal(err)
	}
}

func adoptingHeldSession() store.Conversation {
	return store.Conversation{ConversationID: "c-new", Provider: "sessionprov", ProviderSessionID: nullable(heldSession), Cwd: "/", CreatedAt: "t0", UpdatedAt: "t0"}
}

type reserved struct {
	holder store.Conversation
	turn   int
	err    error
}

func awaitReserved(t *testing.T, done chan reserved) reserved {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the adoption never saw the turn report its session")
		return reserved{}
	}
}

// signal notes on ch, without blocking, that something happened.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func TestAdoptionWaitsForARunningTurnToReportItsSession(t *testing.T) {
	st := store.Open(t.TempDir())
	pendingTurn(t, st)
	seen := make(chan struct{}, 1)
	wait := adoptionWait{deadline: time.Minute, poll: time.Millisecond, onPending: func() { signal(seen) }}
	done := make(chan reserved, 1)
	go func() {
		var r reserved
		r.holder, r.turn, r.err = reserveAdopting(st, adoptingHeldSession(), "r-adopt", heldNow, wait)
		done <- r
	}()

	select {
	case <-seen: // the adoption met the silent turn, and has not created
	case <-time.After(10 * time.Second):
		t.Fatal("the adoption never met the running turn")
	}
	if ids, _ := st.ConversationIDs(); len(ids) != 1 {
		t.Fatalf("conversations = %v while the turn is silent, want only c-pending", ids)
	}
	if holder, err := st.PersistProviderSession("c-pending", heldSession, "t1"); err != nil || holder.ConversationID != "" {
		t.Fatalf("persisting: holder %q err %v", holder.ConversationID, err)
	}

	r := awaitReserved(t, done)
	if r.err != nil || r.holder.ConversationID != "c-pending" {
		t.Errorf("adoption = holder %q err %v, want the running turn's conversation", r.holder.ConversationID, r.err)
	}
	if ids, _ := st.ConversationIDs(); len(ids) != 1 {
		t.Errorf("conversations = %v, want only c-pending", ids)
	}
}

func TestAdoptionCreatesOnceTheWaitIsSpentOnASilentTurn(t *testing.T) {
	st := store.Open(t.TempDir())
	pendingTurn(t, st)

	holder, turn, err := reserveAdopting(st, adoptingHeldSession(), "r-adopt", heldNow, adoptionWait{deadline: 30 * time.Millisecond, poll: time.Millisecond})

	if err != nil || holder.ConversationID != "" || turn != 1 {
		t.Errorf("holder %q turn %d err %v, want c-new created as turn 1", holder.ConversationID, turn, err)
	}
	if ids, _ := st.ConversationIDs(); len(ids) != 2 {
		t.Errorf("conversations = %v, want c-pending and c-new", ids)
	}
}
