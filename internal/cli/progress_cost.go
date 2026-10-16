package cli

import (
	"errors"
	"os"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// liveReading is what a run has used so far and what that costs. Both are nil
// when the provider cannot say, and CostUSD is nil without a price.
type liveReading struct {
	Usage   *provider.Usage
	CostUSD *string
}

// liveReadingOf asks the run's provider what a running run has used since it
// started. A run that has ended gets none: the session's records have no end
// boundary, so they would include whatever the conversation did afterwards.
// Anything else that keeps the answer from being known (a run not yet started,
// a conversation with no provider session, a provider without the capability,
// nothing in the session yet) yields an empty reading silently; a start time,
// conversation record, request or session file that cannot be read yields one
// too, with a warning. Following a run never fails because its usage is unknown.
func liveReadingOf(ctx *Context, st *store.Store, state store.State) liveReading {
	if store.IsTerminal(state.State) || state.StartedAt == nil {
		return liveReading{}
	}
	started, err := time.Parse(time.RFC3339Nano, *state.StartedAt)
	if err != nil {
		ctx.Warnf("reading the start of run %s: %v", state.RunID, err)
		return liveReading{}
	}
	// started_at is kept to the second, so the previous turn of a resumed
	// conversation can end inside that same second. No model call answers
	// within a second of starting, so the whole second counts as before the run.
	since := started.Truncate(time.Second).Add(time.Second)
	conv, err := st.ReadConversation(state.ConversationID)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			ctx.Warnf("reading the conversation of run %s: %v", state.RunID, err)
		}
		return liveReading{}
	}
	if conv.ProviderSessionID == nil || *conv.ProviderSessionID == "" {
		return liveReading{}
	}
	prov, ok := provider.Get(conv.Provider)
	if !ok {
		return liveReading{}
	}
	reporter, ok := prov.(provider.LiveUsageReporter)
	if !ok {
		return liveReading{}
	}
	usage, model, ok, err := reporter.LiveUsage(ctx.Env, *conv.ProviderSessionID, since)
	if err != nil {
		ctx.Warnf("reading the live usage of run %s: %v", state.RunID, err)
		return liveReading{}
	}
	if !ok {
		return liveReading{}
	}
	if model == "" {
		model = requestedModel(ctx, st, state.RunID)
	}
	return liveReading{Usage: &usage, CostUSD: liveCost(ctx, st, pricedUsage{provider: conv.Provider, model: model, usage: usage})}
}

// requestedModel is the model the run was asked for, or "". A request that
// exists but cannot be read is warned about.
func requestedModel(ctx *Context, st *store.Store, runID string) string {
	var req requestRecord
	if err := st.ReadRequest(runID, &req); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			ctx.Warnf("reading the request of run %s: %v", runID, err)
		}
		return ""
	}
	if req.Model == nil {
		return ""
	}
	return *req.Model
}

// pricedUsage is usage to be priced at a provider's model.
type pricedUsage struct {
	provider, model string
	usage           provider.Usage
}

// liveCost is the cost of the usage at the cached price as six decimals, or
// nil when there is no model, no usable cache or no price for the model.
func liveCost(ctx *Context, st *store.Store, p pricedUsage) *string {
	if p.model == "" {
		return nil
	}
	cache := readPriceCache(ctx, st)
	if cache == nil {
		return nil
	}
	price, found := cache.Lookup(p.provider, p.model)
	if !found {
		return nil
	}
	cost := prices.FormatUSD(prices.RunCost(prices.Usage{
		Input: p.usage.InputTokens, Cached: p.usage.CachedInputTokens,
		CacheWrite: p.usage.CacheWriteInputTokens, Output: p.usage.OutputTokens,
		ImplicitCacheWrites: provider.HasImplicitCacheWrites(p.provider),
	}, price))
	return &cost
}
