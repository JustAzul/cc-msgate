package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// rankedEntry is one summary_models entry as `providers --json` prints it.
func rankedEntry(provider, model string, cost float64, basis string, calls int) map[string]any {
	return map[string]any{"provider": provider, "model": model, "cost_usd": cost, "basis": basis, "calls": float64(calls)}
}

// seedSummaryHistory writes two recent and one old summary call of claude-haiku-5-5.
func seedSummaryHistory(t *testing.T, s *sandbox) {
	t.Helper()
	recent, old := tsAgo(time.Hour), tsAgo(40*24*time.Hour)
	seedLines(t, s, map[string]string{
		recent: modelCallLine("c-1", recent, "S", "claude-haiku-5-5", callTokens{input: 1000, output: 100}) + "\n" +
			modelCallLine("c-2", recent, "S", "claude-haiku-5-5", callTokens{input: 2000, cached: 1000, output: 200}),
		old: modelCallLine("c-3", old, "S", "claude-haiku-5-5", callPlain),
	})
}

func summaryModels(t *testing.T, r result) []any {
	t.Helper()
	models, ok := listedAnswer(t, r)["summary_models"].([]any)
	if !ok {
		t.Fatalf("summary_models = %v, want a list", r.json(t)["summary_models"])
	}
	return models
}

func listedAnswer(t *testing.T, r result) map[string]any {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	return r.json(t)
}

func TestRefreshStoresTheSummaryRankingAndProvidersPrintsIt(t *testing.T) {
	s, _ := healthSandbox(t)
	claudeCache(t, s)
	s.set("FAKECODEX_MODELS", catalogFixture())
	seedSummaryHistory(t, s)
	// Fresh entries keep the reader from starting a refresh of its own, which would hold the lock the explicit one needs.
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(true, "", "check", time.Second, nil),
		"anthropic": cacheEntry(true, "", "check", time.Second, nil),
	})
	if before := summaryModels(t, s.run("providers", "--json")); len(before) != 0 {
		t.Fatalf("summary_models before a refresh = %v, want []", before)
	}

	if r := s.run("providers", "refresh"); r.code != 0 {
		t.Fatalf("refresh: exit %d, stderr %q", r.code, r.stderr)
	}
	after := s.run("providers", "--json")

	// haiku: the mean of 150 and 210 millionths; the Codex models over the typical call, m-hidden having no price.
	want := []any{
		rankedEntry("anthropic", "claude-haiku-5-5", 0.00018, "measured", 2),
		rankedEntry("codex", "m-sol", 0.002636, "estimated", 0),
		rankedEntry("codex", "m-bare", 0.003389, "estimated", 0),
		rankedEntry("codex", "m-old", 0.006101, "estimated", 0),
	}
	if got := summaryModels(t, after); !reflect.DeepEqual(got, want) {
		t.Errorf("summary_models = %v, want %v", got, want)
	}
	file := readJSONFile(t, filepath.Join(s.home, "providers.json"))
	rankedAt, _ := file["ranked_at"].(string)
	at, err := time.Parse(time.RFC3339, rankedAt)
	if err != nil || time.Since(at) > time.Minute {
		t.Errorf("ranked_at = %v, want a time within the last minute", file["ranked_at"])
	}
	if !reflect.DeepEqual(file["summary_models"], any(want)) {
		t.Errorf("stored summary_models = %v, want %v", file["summary_models"], want)
	}
}

func TestRefreshWithNoPriceTableStoresAnEmptyRankingAndWarns(t *testing.T) {
	s, _ := healthSandbox(t)
	s.set("FAKECODEX_MODELS", catalogFixture())
	seedSummaryHistory(t, s)

	r := s.run("providers", "refresh")

	if r.code != 0 || r.stderr == "" {
		t.Errorf("exit %d, stderr %q; want exit 0 and a warning", r.code, r.stderr)
	}
	if got := summaryModels(t, s.run("providers", "--json")); len(got) != 0 {
		t.Errorf("summary_models = %v, want []", got)
	}
	if stored := readJSONFile(t, filepath.Join(s.home, "providers.json"))["summary_models"]; !reflect.DeepEqual(stored, any([]any{})) {
		t.Errorf("stored summary_models = %#v, want []", stored)
	}
	if got := cachedProviders(t, s); len(got) != 2 {
		t.Errorf("providers = %v, want codex and anthropic still checked", got)
	}
}

func TestRefreshWithNoPriceTableKeepsTheRankingAlreadyStored(t *testing.T) {
	s, _ := healthSandbox(t)
	s.set("FAKECODEX_MODELS", catalogFixture())
	seedProviders(t, s, map[string]map[string]any{})
	file := readJSONFile(t, filepath.Join(s.home, "providers.json"))
	stored := []any{rankedEntry("anthropic", "claude-haiku-5-5", 0.00018, "measured", 2)}
	rankedAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	file["summary_models"], file["ranked_at"] = stored, rankedAt
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.home, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	r := s.run("providers", "refresh")

	after := readJSONFile(t, filepath.Join(s.home, "providers.json"))
	if r.code != 0 || r.stderr == "" {
		t.Errorf("exit %d, stderr %q; want exit 0 and a warning", r.code, r.stderr)
	}
	if !reflect.DeepEqual(after["summary_models"], any(stored)) || after["ranked_at"] != rankedAt {
		t.Errorf("summary_models %v ranked_at %v, want the stored %v at %s", after["summary_models"], after["ranked_at"], stored, rankedAt)
	}
	if got := cachedProviders(t, s); len(got) != 2 {
		t.Errorf("providers = %v, want codex and anthropic still checked", got)
	}
}

func TestRefreshWithAnUnreadableCodexCatalogRanksWithoutCodexAndWarns(t *testing.T) {
	s, _ := healthSandbox(t)
	claudeCache(t, s)
	seedSummaryHistory(t, s)

	r := s.run("providers", "refresh")

	if r.code != 0 || r.stderr == "" {
		t.Errorf("exit %d, stderr %q; want exit 0 and a warning", r.code, r.stderr)
	}
	want := []any{rankedEntry("anthropic", "claude-haiku-5-5", 0.00018, "measured", 2)}
	if got := summaryModels(t, s.run("providers", "--json")); !reflect.DeepEqual(got, want) {
		t.Errorf("summary_models = %v, want %v", got, want)
	}
}

func TestProvidersTextPrintsOneLinePerRankedModelAfterTheProviders(t *testing.T) {
	s, _ := healthSandbox(t)
	seedProviders(t, s, map[string]map[string]any{
		"codex":     cacheEntry(true, "", "check", 10*time.Second, nil),
		"anthropic": cacheEntry(true, "", "check", 10*time.Second, nil),
	})
	file := readJSONFile(t, filepath.Join(s.home, "providers.json"))
	file["summary_models"] = []any{rankedEntry("anthropic", "claude-haiku-5-5", 0.00018, "measured", 2), rankedEntry("codex", "m-sol", 0.002636, "estimated", 0)}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.home, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	r := s.run("providers")

	lines := r.lines()
	if r.code != 0 || len(lines) != 4 {
		t.Fatalf("exit %d, lines %q; want exit 0 and 4 lines", r.code, lines)
	}
	for i, model := range []string{"claude-haiku-5-5", "m-sol"} {
		if !strings.Contains(lines[2+i], model) {
			t.Errorf("line %d = %q, want the model %s", 2+i, lines[2+i], model)
		}
	}
}
