package cli

import (
	"errors"
	"io"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/settings"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// turn is one fully validated provider turn, ready to plan, admit and run.
type turn struct {
	command  string // exec, review or send: the telemetry command
	flags    parsedTurn
	prov     provider.Provider
	eff      settings.Resolved
	cwd      string
	prompt   []byte
	req      provider.Request // everything but the output path
	st       *store.Store
	existing *store.Conversation // nil: the turn starts a new conversation
	// adopts is the provider session a new conversation takes over; empty when
	// the provider reports the session id during the first turn.
	adopts string
	// onHolder continues the send on the conversation that held adopts by the
	// time the new conversation was to be created.
	onHolder func(holder store.Conversation) int
}

// runTurn plans the turn and, unless it is a dry run, admits and runs it in
// the foreground, printing the result per the output contract.
func runTurn(ctx *Context, t turn) int {
	p := t.flags
	now := ctx.Now().UTC()
	runID, code := chooseRunID(ctx, t.st, p.runID, now)
	if code != 0 {
		return code
	}
	t.req.OutputPath = t.st.OutputPath(runID)
	plan, err := t.prov.BuildPlan(t.req)
	if err != nil {
		return ctx.Fail(ExitInternal, "building the provider plan: %v", err)
	}

	if p.dryRun {
		return printJSON(ctx, struct {
			Command  string `json:"command"`
			Provider string `json:"provider"`
			RunID    string `json:"run_id"`
			provider.Plan
		}{t.command, p.provider, runID, plan})
	}

	adm := t.admission(runID, plan, now)
	ctx.IDs["run_id"], ctx.IDs["conversation_id"] = runID, adm.conversationID
	state, err := admit(t.st, adm)
	if err != nil {
		return t.refused(ctx, err, adm)
	}
	if err := t.st.IndexRun(p.sessionID, runID); err != nil {
		ctx.Warnf("could not add run %s to the session index: %v", runID, err)
	}
	job := buildJob(ctx, t.st, t.prov, adm.request(), state, t.prompt)
	if p.background {
		return launchJob(ctx, job)
	}

	res, err := runner.Run(job)
	if errors.Is(err, runner.ErrCancelPending) {
		return ctx.Fail(ExitCancelled, "run %s was cancelled before it started", runID)
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}

	if p.json {
		return printRunResult(ctx, res)
	}
	io.WriteString(ctx.Stdout, res.State.OutputPath+"\n")
	return res.ExitCode
}

// admission is the turn's admission as run runID with plan.
func (t turn) admission(runID string, plan provider.Plan, now time.Time) admission {
	conversationID := store.NewConversationID(now)
	if t.existing != nil {
		conversationID = t.existing.ConversationID
	}
	return admission{
		command: t.command, runID: runID, conversationID: conversationID, existing: t.existing,
		flags: t.flags.turnFlags, effective: t.eff, cwd: t.cwd, prompt: t.prompt,
		passthrough: t.flags.passthrough, plan: plan, background: t.flags.background, now: now,
		providerSessionID: t.adopts,
	}
}

// refused ends a turn whose admission failed with err. A send that lost the
// adoption of a session to another continues on the conversation holding it.
func (t turn) refused(ctx *Context, err error, adm admission) int {
	var held *heldError
	if errors.As(err, &held) && t.onHolder != nil {
		return t.onHolder(held.holder)
	}
	return failAdmission(ctx, err, adm.runID, adm.conversationID, t.existing != nil)
}

// failAdmission maps an admission error to its exit. Ids that never came to
// exist are dropped from the JSON error object: the run's, and the
// conversation's when the turn was to create it. Only a busy refusal names a
// conversation that exists, since the turn found it there.
func failAdmission(ctx *Context, err error, runID, conversationID string, existing bool) int {
	delete(ctx.IDs, "run_id")
	var busy *store.BusyError
	if errors.As(err, &busy) {
		return ctx.Fail(ExitBusy, "conversation %s is busy: run %s is still queued or running", conversationID, busy.ActiveRunID)
	}
	if !existing {
		delete(ctx.IDs, "conversation_id")
	}
	if errors.Is(err, store.ErrRunExists) {
		return ctx.Fail(ExitUsage, "run id %q already exists", runID)
	}
	return ctx.Fail(ExitInternal, "admitting the run: %v", err)
}
