package summaryrank

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
)

var now = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func price(input, cached, write, output string) prices.Price {
	rat := func(s string) *big.Rat {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			panic(s)
		}
		return r
	}
	return prices.Price{Input: rat(input), CachedInput: rat(cached), CacheWrite: rat(write), Output: rat(output)}
}

func table(models map[string]map[string]prices.Price) *prices.Cache {
	return &prices.Cache{Models: models}
}

func daysAgo(n int) string { return now.Add(-time.Duration(n) * 24 * time.Hour).Format(time.RFC3339) }

type tokens struct{ input, cached, output int64 }

func modelCall(ts, source, provider, model string, u tokens) map[string]any {
	count := func(n int64) json.Number { return json.Number(big.NewInt(n).String()) }
	return map[string]any{
		"ts": ts, "source": source, "provider": provider, "model": model,
		"usage": map[string]any{"input_tokens": count(u.input), "cached_input_tokens": count(u.cached), "output_tokens": count(u.output)},
	}
}

func rank(in Inputs) []Entry {
	in.Now = now
	if in.ImplicitCacheWrites == nil {
		in.ImplicitCacheWrites = func(provider string) bool { return provider == "codex" }
	}
	return Rank(in)
}

func TestAModelWithSummaryCallsIsMeasuredOverTheLastThirtyDays(t *testing.T) {
	list := table(map[string]map[string]prices.Price{"anthropic": {"m-cheap": price("1", "0.1", "1.25", "5")}})
	calls := []map[string]any{
		modelCall(daysAgo(1), "mod-summary", "anthropic", "m-cheap", tokens{input: 1000, output: 100}),
		modelCall(daysAgo(29), "mod-summary", "anthropic", "m-cheap", tokens{input: 2000, cached: 1000, output: 200}),
		modelCall(daysAgo(31), "mod-summary", "anthropic", "m-cheap", tokens{input: 900000, output: 90000}),
		modelCall(daysAgo(1), "run", "anthropic", "m-cheap", tokens{input: 900000, output: 90000}),
	}

	got := rank(Inputs{Prices: list, Calls: calls})

	// 1000*1 + 100*5 = 1500 and 1000*1 + 1000*0.1 + 200*5 = 2100 millionths of a dollar: the mean is 1800.
	want := []Entry{{Provider: "anthropic", Model: "m-cheap", CostUSD: 0.0018, Basis: "measured", Calls: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %v, want %v", got, want)
	}
}

func TestACatalogModelWithoutSummaryCallsIsEstimatedOverTheTypicalCodexCall(t *testing.T) {
	list := table(map[string]map[string]prices.Price{"codex": {"m-a": price("2", "1", "4", "10")}})

	got := rank(Inputs{Prices: list, Catalog: map[string][]string{"codex": {"m-a"}}})

	// 2432 cached at 1, the 917 uncached tokens written to the cache at 4 and 10 output at 10: 2432 + 3668 + 100 = 6200 millionths.
	want := []Entry{{Provider: "codex", Model: "m-a", CostUSD: 0.0062, Basis: "estimated", Calls: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %v, want %v", got, want)
	}
}

func TestAClaudeModelWithoutSummaryCallsIsEstimatedOverTheRecordedMeanOfAllClaudeSummaryCalls(t *testing.T) {
	list := table(map[string]map[string]prices.Price{"anthropic": {
		"m-cheap": price("1", "0.1", "1.25", "5"),
		"m-dear":  price("3", "0.3", "3.75", "15"),
	}})
	calls := []map[string]any{
		modelCall(daysAgo(1), "mod-summary", "anthropic", "m-cheap", tokens{input: 1000, output: 100}),
		modelCall(daysAgo(2), "mod-summary", "anthropic", "m-cheap", tokens{input: 2000, cached: 1000, output: 200}),
		modelCall(daysAgo(40), "mod-summary", "anthropic", "m-other", tokens{input: 900000, output: 90000}),
		modelCall(daysAgo(1), "mod-summary", "codex", "m-cheap", tokens{input: 900000, output: 90000}),
	}

	got := rank(Inputs{Prices: list, Calls: calls})

	// The mean call is 1500 input (500 cached) and 150 output: 1000*3 + 500*0.3 + 150*15 = 5400 millionths.
	want := []Entry{
		{Provider: "anthropic", Model: "m-cheap", CostUSD: 0.0018, Basis: "measured", Calls: 2},
		{Provider: "anthropic", Model: "m-dear", CostUSD: 0.0054, Basis: "estimated", Calls: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %v, want %v", got, want)
	}
}

func TestAClaudeModelIsEstimatedOver507InputAnd35OutputWhenNoSummaryCallIsRecorded(t *testing.T) {
	list := table(map[string]map[string]prices.Price{"anthropic": {"m-dear": price("3", "0.3", "3.75", "15")}})

	got := rank(Inputs{Prices: list})

	// 507*3 + 35*15 = 2046 millionths.
	want := []Entry{{Provider: "anthropic", Model: "m-dear", CostUSD: 0.002046, Basis: "estimated", Calls: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %v, want %v", got, want)
	}
}

func TestModelsAreOrderedCheapestFirstAcrossProvidersAndBases(t *testing.T) {
	list := table(map[string]map[string]prices.Price{
		"codex":     {"m-fast": price("2", "1", "4", "10"), "m-big": price("20", "10", "40", "100")},
		"anthropic": {"m-small": price("1", "0.1", "1.25", "5")},
	})
	calls := []map[string]any{modelCall(daysAgo(1), "mod-summary", "codex", "m-big", tokens{input: 1000})}

	got := rank(Inputs{Prices: list, Catalog: map[string][]string{"codex": {"m-big", "m-fast"}}, Calls: calls})

	// m-small estimated: 507*1 + 35*5 = 682; m-fast estimated: 6200; m-big measured: its 1000 uncached input tokens count as written to the cache at 40 = 40000 millionths.
	want := []Entry{
		{Provider: "anthropic", Model: "m-small", CostUSD: 0.000682, Basis: "estimated", Calls: 0},
		{Provider: "codex", Model: "m-fast", CostUSD: 0.0062, Basis: "estimated", Calls: 0},
		{Provider: "codex", Model: "m-big", CostUSD: 0.04, Basis: "measured", Calls: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %v, want %v", got, want)
	}
}

func TestAModelWithNoPriceIsLeftOutAndNoPriceTableGivesNoRanking(t *testing.T) {
	list := table(map[string]map[string]prices.Price{"codex": {"m-fast": price("2", "1", "4", "10")}})
	catalog := map[string][]string{"codex": {"m-fast", "m-unpriced"}}
	calls := []map[string]any{modelCall(daysAgo(1), "mod-summary", "codex", "m-unpriced", tokens{input: 1000})}

	withTable := rank(Inputs{Prices: list, Catalog: catalog, Calls: calls})
	withoutTable := rank(Inputs{Catalog: catalog, Calls: calls})

	want := []Entry{{Provider: "codex", Model: "m-fast", CostUSD: 0.0062, Basis: "estimated", Calls: 0}}
	if !reflect.DeepEqual(withTable, want) {
		t.Errorf("ranking = %v, want %v", withTable, want)
	}
	if withoutTable == nil || len(withoutTable) != 0 {
		t.Errorf("ranking without a price table = %#v, want an empty list", withoutTable)
	}
}

func TestEqualCostsAreOrderedByProviderThenModelName(t *testing.T) {
	list := table(map[string]map[string]prices.Price{
		"codex":     {"m-a": price("1", "1", "1", "1")},
		"anthropic": {"m-b": price("1", "1", "1", "1"), "m-a": price("1", "1", "1", "1")},
	})
	var calls []map[string]any
	for _, c := range [][2]string{{"codex", "m-a"}, {"anthropic", "m-b"}, {"anthropic", "m-a"}} {
		calls = append(calls, modelCall(daysAgo(1), "mod-summary", c[0], c[1], tokens{input: 1000}))
	}

	got := rank(Inputs{Prices: list, Catalog: map[string][]string{"codex": {"m-a"}}, Calls: calls})

	var order []string
	for _, e := range got {
		order = append(order, e.Provider+"/"+e.Model)
	}
	if want := []string{"anthropic/m-a", "anthropic/m-b", "codex/m-a"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}
