package telemetry

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(s)
	}
	return r
}

func testCache() *prices.Cache {
	p := prices.Price{Input: rat("2"), CachedInput: rat("2"), CacheWrite: rat("2"), Output: rat("2")}
	return &prices.Cache{
		CheckedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		Models:    map[string]map[string]prices.Price{"codex": {"m-a": p, "unknown": p}},
		Unpriced:  map[string][]string{"other": {"m-a"}},
	}
}

func usageOf(n int64) map[string]any {
	return map[string]any{"input_tokens": json.Number(big.NewInt(n).String())}
}

func TestPricesAreLookedUpPerProvider(t *testing.T) {
	runs := []map[string]any{
		{"provider": "codex", "model_used": "m-a", "usage": usageOf(1000000)},
		{"provider": "other", "model_used": "m-a", "usage": usageOf(1000000)},
		{"provider": "third", "model_used": "m-a", "usage": usageOf(1000000)},
	}

	stats := ComputeStats(runs, Skipped{}, nil, testCache())

	// The third provider's m-a is neither priced nor known; the other's is known unpriced.
	if got := stats.Get("unpriced_models"); !reflect.DeepEqual(got, []string{"m-a"}) {
		t.Errorf("unpriced_models = %v", got)
	}
	if got := stats.Get("missing_prices"); !reflect.DeepEqual(got, []string{"m-a"}) {
		t.Errorf("missing_prices = %v", got)
	}
	totals := stats.Get("usage_totals").(*Obj)
	if totals.Get("cost_complete") != false {
		t.Errorf("cost_complete = %v, want false", totals.Get("cost_complete"))
	}
	if c, _ := totals.Get("cost_usd").(Cost); c.Cents() != "2.00" {
		t.Errorf("cost_usd covers %v, want the codex run only", totals.Get("cost_usd"))
	}
}

func TestARunWithNoModelIsNeverPriced(t *testing.T) {
	runs := []map[string]any{{"provider": "codex", "usage": usageOf(1000000)}}

	stats := ComputeStats(runs, Skipped{}, nil, testCache())

	if got := stats.Get("unpriced_models"); !reflect.DeepEqual(got, []string{"unknown"}) {
		t.Errorf("unpriced_models = %v, want the unknown model even when the cache lists a model of that name", got)
	}
	if got := stats.Get("missing_prices"); !reflect.DeepEqual(got, []string{}) {
		t.Errorf("missing_prices = %#v, want none", got)
	}
}

func TestCostIsRoundedOnceFromTheExactSum(t *testing.T) {
	// 9,999 tokens at $0.50 per million cost 0.0049995 dollars: six places
	// round it half to even up to 0.005000, but cents taken from the exact value
	// are 0.00, where cents taken from the six-place string would be 0.01.
	cache := testCache()
	half := rat("0.5")
	cache.Models["codex"]["m-a"] = prices.Price{Input: half, CachedInput: half, CacheWrite: half, Output: half}
	runs := []map[string]any{{"provider": "codex", "model_used": "m-a", "usage": usageOf(9999)}}

	totals := ComputeStats(runs, Skipped{}, nil, cache).Get("usage_totals").(*Obj)

	cost := totals.Get("cost_usd").(Cost)
	data, _ := json.Marshal(cost)
	if string(data) != `"0.005000"` || cost.Cents() != "0.00" {
		t.Errorf("cost = %s (cents %s), want 0.005000 and 0.00", data, cost.Cents())
	}
}
