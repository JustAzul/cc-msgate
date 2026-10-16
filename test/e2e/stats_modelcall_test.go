package e2e

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// callTokens is a model call's usage as telemetry records it.
type callTokens struct{ input, cached, write, output int64 }

// modelCallLine is a model_call record line.
func modelCallLine(id, ts, session, model string, u callTokens) string {
	return fmt.Sprintf(`{"v":1,"kind":"model_call","call_id":%q,"ts":%q,"session_id":%q,"provider":"anthropic","model":%q,"source":"mod-summary","run_id":null,`+
		`"usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0}}`,
		id, ts, session, model, u.input, u.cached, u.write, u.output)
}

func windowStats(t *testing.T, s *sandbox, args ...string) map[string]any {
	t.Helper()
	r := s.run(append([]string{"stats", "--json"}, args...)...)
	if r.code != 0 {
		t.Fatalf("stats exit %d: %s", r.code, r.stderr)
	}
	return r.json(t)
}

// seedLines writes the lines to the month files of their timestamps.
func seedLines(t *testing.T, s *sandbox, stamped map[string]string) {
	t.Helper()
	byMonth := map[string][]string{}
	for ts, line := range stamped {
		byMonth[monthOf(ts)] = append(byMonth[monthOf(ts)], line)
	}
	for month, lines := range byMonth {
		writeMonthFile(t, s.home, month, lines...)
	}
}

// claudeCache is the cost cache plus anthropic claude-haiku-5-5 at $0.10 input,
// $0.01 cached, $0.125 cache write and $0.50 output per million tokens.
func claudeCache(t *testing.T, s *sandbox) {
	t.Helper()
	costCache(t, s)
	c := readJSONFile(t, pricesPath(s))
	c["models"].(map[string]any)["anthropic"] = map[string]any{"claude-haiku-5-5": priceEntry("0.1", "0.01", "0.125", "0.5")}
	writeCache(t, s, c)
}

var (
	// 0.1 + 0.05 dollars.
	callPlain = callTokens{input: 1000000, output: 100000}
	// 200000 * 0.1 + 200000 * 0.01 + 100000 * 0.125 + 50000 * 0.5 = 0.0595 dollars.
	callCached = callTokens{input: 500000, cached: 200000, write: 100000, output: 50000}
)

func sessionHistory(t *testing.T, s *sandbox) {
	t.Helper()
	recent, old := tsAgo(time.Hour), tsAgo(10*24*time.Hour)
	lines := map[string]string{
		recent: recLine("r1", recent, usageRun("m-sol", tokens{input: 1000000, cached: 800000, output: 10000}, `"session_id":"S"`)) + "\n" +
			modelCallLine("m-1", recent, "S", "claude-haiku-5-5", callPlain) + "\n" +
			modelCallLine("m-2", recent, "S", "claude-haiku-5-5", callCached) + "\n" +
			modelCallLine("m-3", recent, "other", "claude-haiku-5-5", callPlain),
	}
	// Another timestamp in the same month would replace the file's lines, so the
	// old call joins them whenever the months match.
	if monthOf(old) == monthOf(recent) {
		lines[recent] += "\n" + modelCallLine("m-4", old, "S", "claude-haiku-5-5", callPlain)
	} else {
		lines[old] = modelCallLine("m-4", old, "S", "claude-haiku-5-5", callPlain)
	}
	seedLines(t, s, lines)
}

func TestStatsSplitsUsageByProviderWithModelCalls(t *testing.T) {
	s := newSandbox(t)
	claudeCache(t, s)
	sessionHistory(t, s)

	stats := windowStats(t, s, "--session-id", "S", "--days", "7")

	totals := obj(t, stats["usage_totals"])
	wantTotals := map[string]any{
		"input_tokens": float64(1000000), "cached_input_tokens": float64(800000), "cache_write_input_tokens": float64(0),
		"output_tokens": float64(10000), "reasoning_output_tokens": float64(0), "runs_with_usage": float64(1),
		"cost_usd": "0.680000", "cost_complete": true,
	}
	if !reflect.DeepEqual(totals, wantTotals) {
		t.Errorf("usage_totals = %v, want the runs only: %v", totals, wantTotals)
	}
	byProvider := obj(t, stats["usage_by_provider"])
	if !reflect.DeepEqual(obj(t, byProvider["codex"]), totals) {
		t.Errorf("usage_by_provider.codex = %v, want usage_totals %v", byProvider["codex"], totals)
	}
	wantClaude := map[string]any{
		"input_tokens": float64(1500000), "cached_input_tokens": float64(200000), "cache_write_input_tokens": float64(100000),
		"output_tokens": float64(150000), "reasoning_output_tokens": float64(0), "runs_with_usage": float64(2),
		"cost_usd": "0.209500", "cost_complete": true,
	}
	if !reflect.DeepEqual(obj(t, byProvider["anthropic"]), wantClaude) {
		t.Errorf("usage_by_provider.anthropic = %v, want %v", byProvider["anthropic"], wantClaude)
	}
	if len(byProvider) != 2 {
		t.Errorf("usage_by_provider keys = %v, want codex and anthropic", byProvider)
	}
	byModel := obj(t, stats["usage_by_model"])
	if !reflect.DeepEqual(obj(t, byModel["claude-haiku-5-5"]), map[string]any{
		"input_tokens": float64(1500000), "cached_input_tokens": float64(200000), "cache_write_input_tokens": float64(100000),
		"output_tokens": float64(150000), "reasoning_output_tokens": float64(0), "runs_with_usage": float64(2), "cost_usd": "0.209500",
	}) {
		t.Errorf("usage_by_model.claude-haiku-5-5 = %v", byModel["claude-haiku-5-5"])
	}
	if stats["total"] != float64(1) || !reflect.DeepEqual(stats["by_provider"], map[string]any{"codex": float64(1)}) {
		t.Errorf("total = %v, by_provider = %v: model calls must not count as runs", stats["total"], stats["by_provider"])
	}
}

func TestStatsModelCallsRespectTheSessionAndTheWindow(t *testing.T) {
	s := newSandbox(t)
	claudeCache(t, s)
	sessionHistory(t, s)

	// Session S inside seven days holds two calls; the other session's call and
	// the ten-day-old one are out.
	wantCalls := map[string]float64{"--session-id S --days 7": 2, "--days 7": 3, "--session-id S --all": 3, "--all": 4, "--session-id nobody --days 7": 0}
	for key, want := range wantCalls {
		args := strings.Fields(key)
		stats := windowStats(t, s, args...)
		got := float64(0)
		if c, ok := obj(t, stats["usage_by_provider"])["anthropic"]; ok {
			got = obj(t, c)["runs_with_usage"].(float64)
		}
		if got != want {
			t.Errorf("stats %s: anthropic model calls = %v, want %v", key, got, want)
		}
	}
}

func TestStatsLeavesAProviderWithoutUsageOut(t *testing.T) {
	s := newSandbox(t)
	claudeCache(t, s)
	sessionHistory(t, s)

	stats := windowStats(t, s, "--session-id", "nobody", "--days", "7")

	if byProvider := obj(t, stats["usage_by_provider"]); len(byProvider) != 0 {
		t.Errorf("usage_by_provider = %v, want none", byProvider)
	}
}

func TestStatsModelCallOfAModelWithoutAPrice(t *testing.T) {
	s := newSandbox(t)
	claudeCache(t, s)
	ts := tsAgo(time.Hour)
	seedLines(t, s, map[string]string{ts: modelCallLine("m-1", ts, "S", "claude-new-9", callPlain)})

	stats := windowStats(t, s)

	claude := obj(t, obj(t, stats["usage_by_provider"])["anthropic"])
	if cost, ok := claude["cost_usd"]; !ok || cost != nil || claude["cost_complete"] != false {
		t.Errorf("anthropic cost_usd = %v (present %v) cost_complete = %v, want null and false", cost, ok, claude["cost_complete"])
	}
	if got := strs(t, stats["missing_prices"]); !reflect.DeepEqual(got, []string{"claude-new-9"}) {
		t.Errorf("missing_prices = %v", got)
	}
	if got := strs(t, stats["unpriced_models"]); !reflect.DeepEqual(got, []string{"claude-new-9"}) {
		t.Errorf("unpriced_models = %v", got)
	}
	totals := obj(t, stats["usage_totals"])
	if totals["runs_with_usage"] != float64(0) || totals["cost_complete"] != true {
		t.Errorf("usage_totals = %v, want the runs only: none, and complete", totals)
	}
}

func TestStatsTextAddsTheModelCallCostLines(t *testing.T) {
	s := newSandbox(t)
	claudeCache(t, s)
	sessionHistory(t, s)

	r := s.run("stats", "--session-id", "S", "--days", "7")

	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	var costs []string
	for _, l := range r.lines() {
		if strings.HasPrefix(l, "cost") {
			costs = append(costs, l)
		}
	}
	want := []string{"cost: $0.68", "cost claude-haiku-5-5: $0.21", "cost m-sol: $0.68"}
	if !reflect.DeepEqual(costs, want) {
		t.Errorf("cost lines = %q, want %q\n%s", costs, want, r.stdout)
	}
	if !strings.Contains(r.stdout, "\ntokens: input_tokens=1000000 ") {
		t.Errorf("the tokens line is not the runs-only total:\n%s", r.stdout)
	}
}

func TestPricesRefreshWantsTheModelsOfModelCalls(t *testing.T) {
	ps := newPriceServer(t)
	ps.configure(func(p *priceServer) {
		p.body = []byte(`{
  "claude-haiku-5-5": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 1e-07, "cache_read_input_token_cost": 1e-08, "cache_creation_input_token_cost": 1.25e-07, "output_cost_per_token": 5e-07},
  "claude-elsewhere": {"litellm_provider": "bedrock", "mode": "chat", "input_cost_per_token": 1e-07, "output_cost_per_token": 5e-07},
  "unknown": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 1e-07, "output_cost_per_token": 5e-07}
}`)
	})
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL).set("FAKECODEX_MODELS", catalogFixture())
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts),
		modelCallLine("m-1", ts, "S", "claude-haiku-5-5", callPlain),
		modelCallLine("m-2", ts, "S", "claude-haiku-5-5", callCached),
		modelCallLine("m-3", ts, "S", "claude-elsewhere", callPlain),
		modelCallLine("m-4", ts, "S", "unknown", callPlain),
	)

	r := s.run("prices", "refresh", "--json")
	j := r.json(t)

	if r.code != 0 || j["reason"] != "updated" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	var claude []string
	for _, key := range []string{"priced", "unpriced"} {
		for _, name := range strs(t, j[key]) {
			if strings.HasPrefix(name, "anthropic/") {
				claude = append(claude, key+" "+name)
			}
		}
	}
	want := []string{"priced anthropic/claude-haiku-5-5", "unpriced anthropic/claude-elsewhere"}
	if !reflect.DeepEqual(claude, want) {
		t.Errorf("anthropic entries = %q, want %q (json %v)", claude, want, j)
	}
	cache := readJSONFile(t, pricesPath(s))
	wantModels := map[string]any{"claude-haiku-5-5": priceEntry("0.1", "0.01", "0.125", "0.5")}
	if got := obj(t, cache["models"])["anthropic"]; !reflect.DeepEqual(got, wantModels) {
		t.Errorf("cache anthropic models = %v, want %v", got, wantModels)
	}
}

// recordCall runs `usage add` for a call of the standard usage.
func recordCall(t *testing.T, s *sandbox, source string, extra ...string) {
	t.Helper()
	s.stdin = anthropicUsage
	args := append([]string{"usage", "add", "--session-id", "S", "--provider", "anthropic", "--model", "claude-haiku-5-5", "--source", source}, extra...)
	if r := s.run(args...); r.code != 0 {
		t.Fatalf("usage add exit %d: %s", r.code, r.stderr)
	}
}

func TestStatsGroupsModelCallsBySourceAndCountsACallIDOnce(t *testing.T) {
	s := newSandbox(t)
	claudeCache(t, s)
	recordCall(t, s, "mod:followups", "--call-id", "followups:chk-1:e1:v0plus")
	// The same call recorded again, as a retry of a recording that failed does.
	recordCall(t, s, "mod:followups", "--call-id", "followups:chk-1:e1:v0plus")
	recordCall(t, s, "mod:followups", "--call-id", "followups:chk-1:e1:consolidate")
	recordCall(t, s, "mod-summary")

	stats := windowStats(t, s, "--session-id", "S")

	// One call is 578 input tokens (100 read, 20 written), 18 out: 58.3 millionths of a dollar.
	want := map[string]any{
		"mod:followups": map[string]any{
			"calls": float64(2), "input_tokens": float64(1156), "cached_input_tokens": float64(200),
			"cache_write_input_tokens": float64(40), "output_tokens": float64(36), "cost_usd": "0.000117",
		},
		"mod-summary": map[string]any{
			"calls": float64(1), "input_tokens": float64(578), "cached_input_tokens": float64(100),
			"cache_write_input_tokens": float64(20), "output_tokens": float64(18), "cost_usd": "0.000058",
		},
	}
	if got := obj(t, stats["model_calls_by_source"]); !reflect.DeepEqual(got, want) {
		t.Errorf("model_calls_by_source = %v, want %v", got, want)
	}
	if stats["total"] != float64(0) {
		t.Errorf("total = %v: model calls are not runs", stats["total"])
	}
}

func TestStatsModelCallsBySourceIsEmptyWithoutModelCalls(t *testing.T) {
	s := newSandbox(t)
	stats := windowStats(t, s)
	if got, ok := stats["model_calls_by_source"].(map[string]any); !ok || len(got) != 0 {
		t.Errorf("model_calls_by_source = %#v, want {}", stats["model_calls_by_source"])
	}
}

func TestStatsModelCallsBySourceHasNoCostForAnUnpricedModel(t *testing.T) {
	s := newSandbox(t)
	recordCall(t, s, "mod:followups", "--call-id", "c1")

	got := obj(t, obj(t, windowStats(t, s, "--session-id", "S")["model_calls_by_source"])["mod:followups"])
	if got["calls"] != float64(1) || got["cost_usd"] != nil {
		t.Errorf("mod:followups = %v, want one call and no cost", got)
	}
}

func TestStatsCountsACallRecordedTwiceWithItsTimeOnce(t *testing.T) {
	s := newSandbox(t)
	recordCall(t, s, "mod:followups", "--call-id", "c1", "--ts", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339))
	recordCall(t, s, "mod:followups", "--call-id", "c1", "--ts", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339))

	got := obj(t, obj(t, windowStats(t, s, "--session-id", "S", "--days", "2")["model_calls_by_source"])["mod:followups"])
	if got["calls"] != float64(1) {
		t.Errorf("calls = %v, want 1", got["calls"])
	}
}

func TestStatsTextNamesTheCallsOfEachSource(t *testing.T) {
	s := newSandbox(t)
	recordCall(t, s, "mod:followups", "--call-id", "c1")
	recordCall(t, s, "mod:followups", "--call-id", "c2")
	recordCall(t, s, "mod-summary")

	r := s.run("stats", "--session-id", "S")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if want := "model calls by source: mod-summary=1 mod:followups=2\n"; !strings.Contains(r.stdout, want) {
		t.Errorf("stats = %q, want it to hold %q", r.stdout, want)
	}
}
