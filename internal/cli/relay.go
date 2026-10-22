package cli

import (
	"errors"
	"os"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// relayRecord is the relay of a run's request.json: the child session the
// run's prompt was passed on to, and the name its parent knows it by.
type relayRecord struct {
	ProviderSessionID string `json:"provider_session_id"`
	Address           string `json:"address"`
}

// providerSession is a session its provider has read, with the provider and
// the reader that read it.
type providerSession struct {
	prov    provider.Provider
	reader  provider.SessionReader
	id      string
	session provider.Session
}

// sessionRoute is where a send of a provider session runs: the session whose
// id the provider resumes, and what the provider reports of it. relay is set
// when the id named a child session, which the provider cannot resume itself:
// the turn is then the parent's. holder is the conversation that already holds
// the session to resume, when the route is a relay and one does.
type sessionRoute struct {
	id      string
	session provider.Session
	relay   *relayRecord
	holder  *store.Conversation
}

// routeSession decides where a send of found runs. A child session whose
// parent the provider reads, and which is itself no child, is reached through
// that parent; every other session is its own route.
func routeSession(ctx *Context, st *store.Store, p parsedTurn, found providerSession) (sessionRoute, int) {
	own := sessionRoute{id: found.id, session: found.session}
	parent := found.session.Parent
	if _, relays := found.prov.(provider.Relayer); parent == nil || !relays {
		return own, 0
	}
	session, ok, err := found.reader.ReadSession(ctx.Env, parent.SessionID)
	if err != nil {
		return sessionRoute{}, ctx.Fail(ExitInternal, "reading the provider session: %v", err)
	}
	if !ok || session.Parent != nil {
		return own, 0
	}
	holder, err := holderOf(st, p.provider, parent.SessionID)
	if err != nil {
		return sessionRoute{}, ctx.Fail(ExitInternal, "reading the conversation: %v", err)
	}
	return sessionRoute{
		id: parent.SessionID, session: session, holder: holder,
		relay: &relayRecord{ProviderSessionID: found.id, Address: parent.Address},
	}, 0
}

// holderOf is the conversation of the named provider that holds the session,
// or nil when none does.
func holderOf(st *store.Store, providerName, sessionID string) (*store.Conversation, error) {
	conv, err := st.FindByProviderSession(providerName, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

// jobPrompt is the prompt the provider receives for a run: the run's own, or,
// for a relayed run, the one that asks the parent to pass it on.
func jobPrompt(prov provider.Provider, relay *relayRecord, prompt []byte) []byte {
	relayer, ok := prov.(provider.Relayer)
	if relay == nil || !ok {
		return prompt
	}
	return relayer.RelayPrompt(relay.Address, prompt)
}
