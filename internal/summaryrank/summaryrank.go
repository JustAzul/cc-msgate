// Package summaryrank ranks the models that can write a summary by what one
// summary call costs.
package summaryrank

import (
	"cmp"
	"math/big"
	"slices"
	"strconv"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

const (
	// Claude is the provider key of Claude models, which have no catalog: every
	// model the price table prices for it is a candidate.
	Claude = "anthropic"
	// Source is the source of the model calls that write summaries.
	Source = "mod-summary"
	// Window is how far back the model calls that count reach.
	Window = 30 * 24 * time.Hour

	// BasisMeasured is the basis of a cost taken from recorded summary calls
	// of the model itself.
	BasisMeasured = "measured"
	// BasisEstimated is the basis of a cost taken from a typical call, for a
	// model with no recorded summary call.
	BasisEstimated = "estimated"
)

// The typical summary calls a model with no recorded one is priced over: a
// bare Codex call, and the call Claude's recorded summaries averaged when
// this was written, for a Claude with none recorded at all.
var (
	typicalCodexCall  = telemetry.CallUsage{Input: 3349, Cached: 2432, Output: 10}
	typicalClaudeCall = telemetry.CallUsage{Input: 507, Output: 35}
)

// Entry is one ranked model.
type Entry struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	CostUSD  float64 `json:"cost_usd"`
	Basis    string  `json:"basis"`
	Calls    int64   `json:"calls"`
}

// Inputs is everything a ranking reads. Catalog maps a provider with a model
// catalog to the models it lists; ImplicitCacheWrites says whether a provider
// caches prompts without reporting what it wrote.
type Inputs struct {
	Prices              *prices.Cache
	Catalog             map[string][]string
	Calls               []map[string]any
	Now                 time.Time
	ImplicitCacheWrites func(provider string) bool
}

// costed is an entry with its exact cost, which the order is taken from.
type costed struct {
	entry Entry
	cost  *big.Rat
}

// Rank orders the models that have a price by the cost of one summary call,
// cheapest first; equal costs are ordered by provider, then model. Without a
// price table the ranking is empty.
func Rank(in Inputs) []Entry {
	ranking := []Entry{}
	if in.Prices == nil {
		return ranking
	}
	groups := telemetry.GroupModelCalls(in.Calls, in.Now.Add(-Window))
	var all []costed
	for provider, models := range candidates(in) {
		for _, model := range models {
			if c, ok := costOf(in, groups, provider, model); ok {
				all = append(all, c)
			}
		}
	}
	slices.SortFunc(all, func(a, b costed) int {
		return cmp.Or(a.cost.Cmp(b.cost), cmp.Compare(a.entry.Provider, b.entry.Provider), cmp.Compare(a.entry.Model, b.entry.Model))
	})
	for _, c := range all {
		c.entry.CostUSD = dollars(c.cost)
		ranking = append(ranking, c.entry)
	}
	return ranking
}

// candidates lists the models to rank per provider: the catalog's models, and
// every model the price table prices for Claude.
func candidates(in Inputs) map[string][]string {
	models := map[string][]string{}
	for provider, listed := range in.Catalog {
		models[provider] = listed
	}
	for model := range in.Prices.Models[Claude] {
		models[Claude] = append(models[Claude], model)
	}
	return models
}

// costOf is the cost of one summary call of the model; ok is false when the
// price table has no price for it.
func costOf(in Inputs, groups map[telemetry.CallGroup][]telemetry.CallUsage, provider, model string) (costed, bool) {
	price, ok := in.Prices.Lookup(provider, model)
	if !ok {
		return costed{}, false
	}
	calls := groups[telemetry.CallGroup{Source: Source, Provider: provider, Model: model}]
	entry := Entry{Provider: provider, Model: model, Basis: BasisMeasured, Calls: int64(len(calls))}
	if len(calls) == 0 {
		entry.Basis, calls = BasisEstimated, typicalCalls(groups, provider)
	}
	return costed{entry, meanCost(calls, price, in.ImplicitCacheWrites(provider))}, true
}

// typicalCalls are the calls a model with no recorded summary call is priced
// over: for Claude, every Claude summary call recorded, else the typical Claude
// call; for any other provider, the typical Codex call.
func typicalCalls(groups map[telemetry.CallGroup][]telemetry.CallUsage, provider string) []telemetry.CallUsage {
	if provider != Claude {
		return []telemetry.CallUsage{typicalCodexCall}
	}
	var recorded []telemetry.CallUsage
	for group, calls := range groups {
		if group.Source == Source && group.Provider == Claude {
			recorded = append(recorded, calls...)
		}
	}
	if len(recorded) == 0 {
		return []telemetry.CallUsage{typicalClaudeCall}
	}
	return recorded
}

// meanCost is the mean of the costs of the calls, each priced as stats prices
// a run. The calls are not empty.
func meanCost(calls []telemetry.CallUsage, price prices.Price, implicit bool) *big.Rat {
	total := new(big.Rat)
	for _, call := range calls {
		total.Add(total, prices.RunCost(usageOf(call, implicit), price))
	}
	return total.Quo(total, new(big.Rat).SetInt64(int64(len(calls))))
}

func usageOf(call telemetry.CallUsage, implicit bool) prices.Usage {
	return prices.Usage{Input: call.Input, Cached: call.Cached, CacheWrite: call.CacheWrite, Output: call.Output, ImplicitCacheWrites: implicit}
}

// dollars is cost rounded to six places, the precision the cost is kept at.
func dollars(cost *big.Rat) float64 {
	value, _ := strconv.ParseFloat(prices.FormatUSD(cost), 64)
	return value
}
