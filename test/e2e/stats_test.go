package e2e

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

var analyzerKeys = []string{
	"total", "empty", "span", "by_source", "by_source_status", "instrumentation", "reliability",
	"outcomes", "duration_ms", "findings", "review_findings_proxy", "window_days",
}

func TestStatsParityWithTheRetiredAnalyzer(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if r := importInto(t, home, legacyFixture()); r.code != 0 {
		t.Fatalf("import exit %d: %s", r.code, r.stderr)
	}
	r := newSandbox(t)
	r.home = home
	got := r.run("stats", "--all", "--json")
	if got.code != 0 {
		t.Fatalf("stats exit %d: %s", got.code, got.stderr)
	}
	actual := got.json(t)
	expected := readJSONFile(t, filepath.Join(repoRoot, "testdata", "legacy", "expected-stats.json"))

	for _, k := range keysOf(expected) {
		if !reflect.DeepEqual(actual[k], expected[k]) {
			t.Errorf("stats.%s\n got %#v\nwant %#v", k, actual[k], expected[k])
		}
	}
	// Only keys the analyzer never had may be extra.
	wantKeys := append(append([]string(nil), keysOf(expected)...), "by_provider", "usage_totals", "usage_by_model", "usage_by_provider", "skipped",
		"unpriced_models", "missing_prices", "prices_checked_at", "model_calls_by_source")
	if g := keysOf(actual); !reflect.DeepEqual(g, sortedCopy(wantKeys)) {
		t.Errorf("stats keys = %v, want %v", g, sortedCopy(wantKeys))
	}
	for _, k := range analyzerKeys {
		if _, ok := actual[k]; !ok {
			t.Errorf("stats lacks %s", k)
		}
	}
	if !reflect.DeepEqual(actual["by_provider"], map[string]any{"codex": float64(22)}) {
		t.Errorf("by_provider = %v", actual["by_provider"])
	}
	for _, k := range []string{"unpriced_models", "missing_prices"} {
		if !reflect.DeepEqual(actual[k], []any{}) {
			t.Errorf("%s = %#v, want an empty list", k, actual[k])
		}
	}
	if v, ok := actual["prices_checked_at"]; !ok || v != nil {
		t.Errorf("prices_checked_at = %#v (present %v), want null", v, ok)
	}
	if !reflect.DeepEqual(actual["skipped"], map[string]any{"unknown_kind": float64(0), "unknown_version": float64(0), "unparseable": float64(0)}) {
		t.Errorf("skipped = %v", actual["skipped"])
	}
	wantUsage := map[string]any{"input_tokens": float64(0), "cached_input_tokens": float64(0), "cache_write_input_tokens": float64(0),
		"output_tokens": float64(0), "reasoning_output_tokens": float64(0), "runs_with_usage": float64(0),
		"cost_usd": nil, "cost_complete": true}
	if !reflect.DeepEqual(actual["usage_totals"], wantUsage) {
		t.Errorf("usage_totals = %v", actual["usage_totals"])
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestStatsFromLiveRuns(t *testing.T) {
	s := newSandbox(t)
	seedRun(t, s, "st-ok", "--source", "hook-post-commit", "--purpose", "code-review")
	if r := s.run("annotate", "st-ok", "--attr-json", `review.findings={"total":2,"high":1,"medium":1}`); r.code != 0 {
		t.Fatalf("annotate: %d %s", r.code, r.stderr)
	}
	s.set("FAKECODEX_FIXTURE", fixture("error-400")).set("FAKECODEX_EXIT", "1")
	s.run("exec", "--run-id", "st-err", "--source", "hook-post-commit", "--purpose", "code-review", "q")
	s.set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_EXIT", "0").set("FAKECODEX_OUTPUT", "")
	s.run("exec", "--run-id", "st-empty", "--source", "skill", "q")

	r := s.run("stats", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	j := r.json(t)
	checks := map[string]any{
		"total":       float64(3),
		"empty":       false,
		"window_days": float64(7),
		"by_source":   map[string]any{"hook-post-commit": float64(2), "skill": float64(1)},
		"by_source_status": map[string]any{
			"hook-post-commit": map[string]any{"ok": float64(1), "error": float64(1)},
			"skill":            map[string]any{"empty-output": float64(1)},
		},
		"instrumentation": map[string]any{"with_status": float64(3), "missing_status": float64(0)},
		"reliability":     map[string]any{"with_status": float64(2), "ok": float64(1), "error": float64(1), "empty_output": float64(0)},
		"outcomes":        map[string]any{"ok": float64(1), "error": float64(1)},
		"by_provider":     map[string]any{"codex": float64(3)},
		"findings": map[string]any{
			"instrumented_reviews": float64(1), "reviews_with_findings": float64(1), "reviews_clean": float64(0),
			"severity_totals": map[string]any{"total": float64(2), "critical": float64(0), "high": float64(1), "medium": float64(1), "low": float64(0), "violations": float64(0)},
		},
	}
	for k, want := range checks {
		if !reflect.DeepEqual(j[k], want) {
			t.Errorf("stats.%s\n got %#v\nwant %#v", k, j[k], want)
		}
	}
	usage := j["usage_totals"].(map[string]any)
	if usage["input_tokens"] != float64(2*18573) || usage["output_tokens"] != float64(2*5) || usage["runs_with_usage"] != float64(2) {
		t.Errorf("usage_totals = %v", usage)
	}
	proxy := j["review_findings_proxy"].(map[string]any)
	if proxy["total"] != float64(2) || proxy["succeeded"] != float64(1) || proxy["failed_or_empty"] != float64(1) {
		t.Errorf("review_findings_proxy = %v", proxy)
	}
}

func TestStatsDefaultWindowIsSevenDays(t *testing.T) {
	s := newSandbox(t)
	young, old := tsAgo(3*24*time.Hour), tsAgo(10*24*time.Hour)
	byMonth := map[string][]string{}
	byMonth[monthOf(old)] = append(byMonth[monthOf(old)], recLine("old", old, `"source":"skill"`))
	byMonth[monthOf(young)] = append(byMonth[monthOf(young)], recLine("young", young, `"source":"skill"`))
	for m, lines := range byMonth {
		writeMonthFile(t, s.home, m, lines...)
	}
	j := s.run("stats", "--json").json(t)
	if j["total"] != float64(1) || j["window_days"] != float64(7) {
		t.Errorf("default window: total %v window_days %v", j["total"], j["window_days"])
	}
	if j := s.run("stats", "--days", "30", "--json").json(t); j["total"] != float64(2) || j["window_days"] != float64(30) {
		t.Errorf("--days 30: total %v window_days %v", j["total"], j["window_days"])
	}
	if j := s.run("stats", "--all", "--json").json(t); j["total"] != float64(2) || j["window_days"] != nil {
		t.Errorf("--all: total %v window_days %v", j["total"], j["window_days"])
	}
	if r := s.run("stats", "--days", "3", "--all"); r.code != 2 {
		t.Errorf("--days with --all: exit %d, want 2", r.code)
	}
}

func TestStatsSkippedCounts(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts),
		`{"v":1,"kind":"note"}`, `{"v":2,"kind":"run","run_id":"x","ts":"`+ts+`"}`, `garbage`)
	j := s.run("stats", "--json").json(t)
	want := map[string]any{"unknown_kind": float64(1), "unknown_version": float64(1), "unparseable": float64(1)}
	if !reflect.DeepEqual(j["skipped"], want) {
		t.Errorf("skipped = %v, want %v", j["skipped"], want)
	}
	if j["total"] != float64(0) || j["empty"] != true || j["window_days"] != float64(7) {
		t.Errorf("empty window shape: %v", j)
	}
	for _, k := range []string{"by_provider", "usage_totals"} {
		if _, ok := j[k]; !ok {
			t.Errorf("empty stats lacks %s", k)
		}
	}
}

func TestStatsHumanSummary(t *testing.T) {
	s := newSandbox(t)
	seedRun(t, s, "hs1", "--source", "hook-post-commit", "--purpose", "code-review")
	r := s.run("stats")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(r.stdout), "{") {
		t.Errorf("human output looks like JSON: %q", r.stdout)
	}
	for _, want := range []string{"runs: 1", "hook-post-commit", "codex"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("summary lacks %q:\n%s", want, r.stdout)
		}
	}
	if n := len(r.lines()); n > 20 {
		t.Errorf("summary has %d lines, want a short one", n)
	}
}

func TestTelemetryVolume(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	if err := os.MkdirAll(telemetryDir(s.home), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(telemetryDir(s.home), monthOf(ts)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	const n = 20000
	for i := 0; i < n; i++ {
		w.WriteString(recLine("v"+itoa(i), ts, `"source":"skill","outcome":"ok","duration_ms":5,"usage":{"input_tokens":1,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0},"attrs":{"i":`+itoa(i)+`}`) + "\n")
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	start := time.Now()
	r := s.run("runs", "--all")
	runsTook := time.Since(start)
	if r.code != 0 || len(r.lines()) != n {
		t.Fatalf("runs: exit %d, %d lines", r.code, len(r.lines()))
	}
	start = time.Now()
	j := s.run("stats", "--all", "--json").json(t)
	statsTook := time.Since(start)
	if j["total"] != float64(n) || j["usage_totals"].(map[string]any)["runs_with_usage"] != float64(n) {
		t.Errorf("stats total = %v, usage_totals = %v", j["total"], j["usage_totals"])
	}
	t.Logf("20,000 records: runs --all %v, stats --all %v", runsTook, statsTook)
	if runsTook > 20*time.Second || statsTook > 20*time.Second {
		t.Errorf("too slow: runs %v stats %v", runsTook, statsTook)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
