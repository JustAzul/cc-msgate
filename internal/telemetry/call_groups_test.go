package telemetry

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func groupedCall(ts, source, provider, model string, usage map[string]any) map[string]any {
	call := map[string]any{"ts": ts, "source": source, "provider": provider, "model": model}
	if usage != nil {
		call["usage"] = usage
	}
	return call
}

func TestModelCallsAreGroupedBySourceProviderAndModel(t *testing.T) {
	since := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	calls := []map[string]any{
		groupedCall("2026-10-01T00:00:00Z", "mod-summary", "anthropic", "m-a", map[string]any{"input_tokens": json.Number("507"), "cached_input_tokens": json.Number("7"), "cache_write_input_tokens": json.Number("3"), "output_tokens": json.Number("35")}),
		groupedCall("2026-10-02T00:00:00Z", "mod-summary", "anthropic", "m-a", map[string]any{"input_tokens": json.Number("100"), "output_tokens": json.Number("5")}),
		groupedCall("2026-10-02T00:00:00Z", "mod-summary", "anthropic", "m-b", map[string]any{"input_tokens": json.Number("1")}),
		groupedCall("2026-10-02T00:00:00Z", "mod-summary", "codex", "m-a", map[string]any{"input_tokens": json.Number("2")}),
		groupedCall("2026-10-02T00:00:00Z", "other", "anthropic", "m-a", map[string]any{"input_tokens": json.Number("3")}),
		groupedCall("2026-09-23T11:59:59Z", "mod-summary", "anthropic", "m-a", map[string]any{"input_tokens": json.Number("4")}),
		groupedCall("not a time", "mod-summary", "anthropic", "m-a", map[string]any{"input_tokens": json.Number("5")}),
		groupedCall("2026-10-02T00:00:00Z", "mod-summary", "anthropic", "m-a", nil),
	}

	got := GroupModelCalls(calls, since)

	want := map[CallGroup][]CallUsage{
		{"mod-summary", "anthropic", "m-a"}: {{Input: 507, Cached: 7, CacheWrite: 3, Output: 35}, {Input: 100, Output: 5}},
		{"mod-summary", "anthropic", "m-b"}: {{Input: 1}},
		{"mod-summary", "codex", "m-a"}:     {{Input: 2}},
		{"other", "anthropic", "m-a"}:       {{Input: 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}
