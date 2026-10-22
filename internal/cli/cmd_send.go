package cli

import (
	"errors"
	"os"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/settings"
	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "send", Summary: "run the next turn of a conversation", Run: runSend})
}

// errNoSendTarget means an id can name no conversation, run or provider
// session, so the provider is not asked about it.
var errNoSendTarget = errors.New("the id names no conversation, run or provider session")

var sendSpec = turnSpec{name: "send", usage: "[flags] <conversation_id | run_id> <prompt | -> [-- native flags]"}

func runSend(ctx *Context, args []string) int {
	p, exit, done := parseTurnArgs(ctx, sendSpec, args)
	if done {
		return exit
	}
	if len(p.positional) == 0 {
		return ctx.Fail(ExitUsage, "send needs the conversation id or a run id of the conversation")
	}
	target := p.positional[0]
	p.positional = p.positional[1:]

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	conv, byRun, err := findConversation(st, target, p.provider)
	switch {
	case errors.Is(err, errNoSendTarget):
		return sendNotFound(ctx, target)
	case errors.Is(err, os.ErrNotExist):
		return sendToProviderSession(ctx, st, p, target)
	case err != nil:
		return ctx.Fail(ExitInternal, "reading the conversation: %v", err)
	}
	ctx.IDs["conversation_id"] = conv.ConversationID
	readerGate(ctx)
	if byRun && !st.RunExists(target) {
		return sendNotFound(ctx, target)
	}
	return sendToConversation(ctx, st, p, conv, nil)
}

// sendToConversation runs the next turn of conv, relayed to a child session of
// the conversation's when relay is set.
func sendToConversation(ctx *Context, st *store.Store, p parsedTurn, conv store.Conversation, relay *relayRecord) int {
	return sendTurn(ctx, st, p, conv, relay, func() ([]byte, int) { return readPrompt(ctx, p) })
}

// sendToHolder runs the next turn of conv, which came to hold the provider
// session after this send resolved it, with the prompt already read: the turn
// is exactly what a send of conv would have been.
func sendToHolder(ctx *Context, st *store.Store, p parsedTurn, conv store.Conversation, relay *relayRecord, prompt []byte) int {
	delete(ctx.IDs, "run_id")
	ctx.IDs["conversation_id"] = conv.ConversationID
	return sendTurn(ctx, st, p, conv, relay, func() ([]byte, int) { return prompt, 0 })
}

// sendTurn runs the next turn of conv with the prompt that readPrompt yields
// once the turn has passed its checks.
func sendTurn(ctx *Context, st *store.Store, p parsedTurn, conv store.Conversation, relay *relayRecord, readPrompt func() ([]byte, int)) int {
	prov, code := conversationProvider(ctx, conv, &p)
	if code != 0 {
		return code
	}
	if !p.given["purpose"] {
		p.purpose = conv.Defaults.Purpose
	}
	eff := turnSettings(st, conv, p)
	if code := checkedTurn(ctx, prov, p, eff); code != 0 {
		return code
	}
	prompt, code := readPrompt()
	if code != 0 {
		return code
	}
	cwd, code := turnCwd(ctx, conv, p)
	if code != 0 {
		return code
	}

	// Re-read so the checks below see the conversation as it is now.
	var err error
	if conv, err = readConversationReconciled(ctx, st, conv.ConversationID); err != nil {
		return ctx.Fail(ExitInternal, "reading the conversation: %v", err)
	}
	if conv.ActiveRunID != nil {
		return ctx.Fail(ExitBusy, "conversation %s is busy: run %s is still queued or running", conv.ConversationID, *conv.ActiveRunID)
	}
	if conv.ProviderSessionID == nil {
		return ctx.Fail(ExitNotResumable, "conversation %s is not resumable: the provider never reported a provider session id for it", conv.ConversationID)
	}

	return runTurn(ctx, turn{
		command: "send", flags: p, prov: prov, eff: eff, cwd: cwd, prompt: prompt, st: st, existing: &conv, relay: relay,
		req: provider.Request{
			Command: "resume", Cwd: cwd, Model: eff.Model, Effort: eff.Effort, Sandbox: eff.Sandbox,
			SessionID: *conv.ProviderSessionID, Passthrough: p.passthrough,
		},
	})
}

// conversationProvider is the provider of conv, which p must not contradict;
// p then names it.
func conversationProvider(ctx *Context, conv store.Conversation, p *parsedTurn) (provider.Provider, int) {
	if p.given["provider"] && p.provider != conv.Provider {
		return nil, ctx.Fail(ExitUsage, "conversation %s uses provider %q, not %q", conv.ConversationID, conv.Provider, p.provider)
	}
	p.provider = conv.Provider
	prov, ok := provider.Get(p.provider)
	if !ok {
		return nil, ctx.Fail(ExitUsage, "unknown provider %q (known: %v)", p.provider, provider.Names())
	}
	return prov, 0
}

// checkedPrompt refuses a turn the provider cannot run with the effective
// settings and the native flags, then reads the turn's one prompt.
func checkedPrompt(ctx *Context, prov provider.Provider, p parsedTurn, eff settings.Resolved) ([]byte, int) {
	if code := checkedTurn(ctx, prov, p, eff); code != 0 {
		return nil, code
	}
	return readPrompt(ctx, p)
}

// checkedTurn refuses a turn the provider cannot run with the effective
// settings and the native flags, or that does not name exactly one prompt.
func checkedTurn(ctx *Context, prov provider.Provider, p parsedTurn, eff settings.Resolved) int {
	if err := prov.Capabilities().Unsupported(provider.Needs{Command: "resume", Sandbox: eff.Sandbox}); err != nil {
		return ctx.Fail(ExitUsage, "provider %q %v", p.provider, err)
	}
	if flagText, reserved := reservedPassthrough(prov, p.passthrough); reserved {
		return ctx.Fail(ExitUsage, "native flag %q is reserved by msgate and cannot be passed after --", flagText)
	}
	if len(p.positional)+boolToInt(p.promptFile != "") != 1 {
		return ctx.Fail(ExitUsage, "exactly one prompt source is required: a positional prompt, - for stdin, or --prompt-file")
	}
	return 0
}

// sendToProviderSession runs a turn for a session the provider holds that no
// conversation does. A child session is reached through its parent: the turn
// is the parent's, relayed to the child. Any other session is adopted by a new
// conversation, taking its directory, model and effort unless the flags say
// otherwise. An id the provider does not know is exit 4.
func sendToProviderSession(ctx *Context, st *store.Store, p parsedTurn, id string) int {
	found, code := readProviderSession(ctx, p, id)
	if code != 0 {
		return code
	}
	route, code := routeSession(ctx, st, p, found)
	if code != 0 {
		return code
	}
	if route.holder != nil {
		ctx.IDs["conversation_id"] = route.holder.ConversationID
		return sendToConversation(ctx, st, p, *route.holder, route.relay)
	}
	return adoptSession(ctx, st, p, found.prov, route)
}

// readProviderSession reads the session id from the provider named by p; a
// provider that cannot read sessions, or has none by that id, is exit 4.
func readProviderSession(ctx *Context, p parsedTurn, id string) (providerSession, int) {
	prov, ok := provider.Get(p.provider)
	if !ok {
		return providerSession{}, sendNotFound(ctx, id)
	}
	reader, ok := prov.(provider.SessionReader)
	if !ok {
		return providerSession{}, sendNotFound(ctx, id)
	}
	session, found, err := reader.ReadSession(ctx.Env, id)
	if err != nil {
		return providerSession{}, ctx.Fail(ExitInternal, "reading the provider session: %v", err)
	}
	if !found {
		return providerSession{}, sendNotFound(ctx, id)
	}
	return providerSession{prov: prov, reader: reader, id: id, session: session}, 0
}

// adoptSession runs the first turn of a new conversation that takes over
// route's session; a conversation that comes to hold it first runs the turn
// instead.
func adoptSession(ctx *Context, st *store.Store, p parsedTurn, prov provider.Provider, route sessionRoute) int {
	eff := adoptedSettings(p, route.session)
	prompt, code := checkedPrompt(ctx, prov, p, eff)
	if code != 0 {
		return code
	}

	cwd, code := adoptedCwd(ctx, p, route.id, route.session)
	if code != 0 {
		return code
	}

	return runTurn(ctx, turn{
		command: "send", flags: p, prov: prov, eff: eff, cwd: cwd, prompt: prompt, st: st, adopts: route.id, relay: route.relay,
		onHolder: func(holder store.Conversation) int { return sendToHolder(ctx, st, p, holder, route.relay, prompt) },
		req: provider.Request{
			Command: "resume", Cwd: cwd, Model: eff.Model, Effort: eff.Effort, Sandbox: eff.Sandbox,
			SessionID: route.id, Passthrough: p.passthrough,
		},
	})
}

func sendNotFound(ctx *Context, id string) int {
	return ctx.Fail(ExitNotFound, "no conversation, run or provider session %q", id)
}

// adoptedSettings are the flags' settings, with the session's own model and
// effort standing in for any the flags leave unset.
func adoptedSettings(p parsedTurn, session provider.Session) settings.Resolved {
	eff := settings.Resolve(settings.Flags{Model: p.model, Effort: p.effort, Sandbox: p.sandbox, EffortSource: p.effortSource}, p.settingsSource)
	if eff.Model == "" && session.Model != "" {
		eff.Model, eff.ModelSource = session.Model, settings.SourceProviderSession
	}
	if eff.Effort == "" && session.Effort != "" {
		eff.Effort, eff.EffortSource = session.Effort, settings.SourceProviderSession
	}
	return eff
}

// adoptedCwd is --cwd when given, else the directory the session records,
// which must still exist.
func adoptedCwd(ctx *Context, p parsedTurn, id string, session provider.Session) (string, int) {
	if p.given["cwd"] {
		return resolveCwd(ctx, p.cwd, p.dryRun)
	}
	if session.Cwd == "" {
		return "", ctx.Fail(ExitUsage, "the provider session %s records no working directory; pass --cwd", id)
	}
	if info, err := os.Stat(session.Cwd); err != nil || !info.IsDir() {
		return "", ctx.Fail(ExitUsage, "the provider session's working directory %q no longer exists; pass --cwd to run this turn elsewhere", session.Cwd)
	}
	return session.Cwd, 0
}

// findConversation resolves an id that names a conversation, one of its runs,
// or the provider session of a conversation of the named provider, in that
// order; byRun says the id named a run. The error wraps os.ErrNotExist when
// no conversation holds the id, and is errNoSendTarget when the id cannot name
// a session either: it is no run id, or it names a run whose conversation is
// gone.
func findConversation(st *store.Store, id, providerName string) (conv store.Conversation, byRun bool, err error) {
	if store.ValidateRunID(id) != nil {
		return store.Conversation{}, false, errNoSendTarget
	}
	conv, err = st.ReadConversation(id)
	if !errors.Is(err, os.ErrNotExist) {
		return conv, false, err
	}
	state, err := st.ReadState(id)
	if err == nil {
		conv, err = st.ReadConversation(state.ConversationID)
		if errors.Is(err, os.ErrNotExist) {
			err = errNoSendTarget
		}
		return conv, true, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return store.Conversation{}, false, err
	}
	conv, err = st.FindByProviderSession(providerName, id)
	return conv, false, err
}

// turnSettings merges this turn's explicit flags over the conversation's
// stored defaults. A stored value keeps the source its first turn recorded.
func turnSettings(st *store.Store, conv store.Conversation, p parsedTurn) settings.Resolved {
	first := firstTurnSources(st, conv)
	pick := func(name, flagValue, passedSource, stored, storedSource, unset, unsetSource string) (string, string) {
		switch {
		case p.given[name] && flagValue != "":
			return flagValue, passedSource
		case stored == "":
			return unset, unsetSource
		}
		return stored, storedSource
	}
	var r settings.Resolved
	r.Model, r.ModelSource = pick("model", p.model, p.settingsSource, conv.Defaults.Model, first.model, "", settings.SourceProviderDefault)
	r.Effort, r.EffortSource = pick("effort", p.effort, firstNonEmpty(p.effortSource, p.settingsSource), conv.Defaults.Effort, first.effort, "", settings.SourceProviderDefault)
	r.Sandbox, r.SandboxSource = pick("sandbox", p.sandbox, p.settingsSource, conv.Defaults.Sandbox, first.sandbox, settings.DefaultSandbox, settings.SourceDefault)
	return r
}

type settingSources struct{ model, effort, sandbox string }

// firstTurnSources says where the conversation's model, effort and sandbox
// came from. The conversation record keeps them; a record written before it
// did falls back to the first turn's request when that run still exists, and
// to the profile otherwise.
func firstTurnSources(st *store.Store, conv store.Conversation) settingSources {
	d := conv.Defaults
	out := settingSources{d.ModelSource, d.EffortSource, d.SandboxSource}
	if out.model != "" && out.effort != "" && out.sandbox != "" {
		return out
	}
	var req struct {
		ModelSource   string `json:"model_source"`
		EffortSource  string `json:"effort_source"`
		SandboxSource string `json:"sandbox_source"`
	}
	if len(conv.Turns) > 0 {
		_ = st.ReadRequest(conv.Turns[0], &req) // a missing record leaves the profile
	}
	out.model = firstNonEmpty(out.model, req.ModelSource, settings.SourceProfile)
	out.effort = firstNonEmpty(out.effort, req.EffortSource, settings.SourceProfile)
	out.sandbox = firstNonEmpty(out.sandbox, req.SandboxSource, settings.SourceProfile)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// turnCwd is --cwd when given, else the conversation's recorded directory,
// which must still exist.
func turnCwd(ctx *Context, conv store.Conversation, p parsedTurn) (string, int) {
	if p.given["cwd"] {
		return resolveCwd(ctx, p.cwd, p.dryRun)
	}
	if info, err := os.Stat(conv.Cwd); err != nil || !info.IsDir() {
		return "", ctx.Fail(ExitUsage, "the conversation's working directory %q no longer exists; pass --cwd to run this turn elsewhere", conv.Cwd)
	}
	return conv.Cwd, 0
}
