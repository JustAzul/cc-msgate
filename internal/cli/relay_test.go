package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// relayProvider is a provider that can relay a prompt to a child session; the
// command it plans does nothing.
type relayProvider struct{ sessionProvider }

func (relayProvider) Name() string { return "relayprov" }
func (relayProvider) RelayPrompt(address string, prompt []byte) []byte {
	return append([]byte("to "+address+": "), prompt...)
}

func init() { provider.Register(relayProvider{}) }

const (
	relayParent = "00000000-0000-4000-8000-000000000004"
	relayChild  = "00000000-0000-4000-8000-000000000005"
)

// A relayed send that loses the adoption of the parent to a conversation that
// comes to hold it runs on that conversation, and still as a relayed turn.
func TestRelayedTurnThatLosesTheAdoptionOfTheParentIsStillRelayed(t *testing.T) {
	home := t.TempDir()
	st := store.Open(home)
	holder := store.Conversation{
		ConversationID: "c-held", Provider: "relayprov", ProviderSessionID: nullable(relayParent), Cwd: t.TempDir(),
		Defaults:  store.Defaults{Purpose: "review", Sandbox: "read-only", SandboxSource: "default"},
		CreatedAt: "t0", UpdatedAt: "t0",
	}
	if err := st.ReserveNew(holder, "r1", heldNow); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateConversation("c-held", func(c *store.Conversation) { c.ActiveRunID = nil }); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	ctx := &Context{
		Env: []string{"MSGATE_HOME=" + home}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return heldNow }, IDs: map[string]string{},
	}
	p, _, done := parseTurnArgs(ctx, sendSpec, []string{"--provider", "relayprov", "--run-id", "r2", relayChild, "go on"})
	if done {
		t.Fatal("the arguments were not parsed")
	}
	p.positional = p.positional[1:]
	// The route found no holder for the parent; one appeared before the adoption.
	route := sessionRoute{
		id: relayParent, session: provider.Session{Cwd: t.TempDir()},
		relay: &relayRecord{ProviderSessionID: relayChild, Address: "helper"},
	}

	adoptSession(ctx, st, p, relayProvider{}, route) // the planned command may not run here; the run's records are what is checked
	state, err := st.ReadState("r2")
	if err != nil {
		t.Fatal(err)
	}
	if state.ConversationID != "c-held" || state.Turn != 2 {
		t.Errorf("run r2 = conversation %s turn %d, want c-held turn 2", state.ConversationID, state.Turn)
	}
	var req requestRecord
	if err := st.ReadRequest("r2", &req); err != nil {
		t.Fatal(err)
	}
	if want := (&relayRecord{ProviderSessionID: relayChild, Address: "helper"}); req.Relay == nil || *req.Relay != *want {
		t.Errorf("request relay = %+v, want %+v", req.Relay, want)
	}
}
