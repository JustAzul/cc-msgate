package codex_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

var runStart = time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)

func turnContextAt(ts, model string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":%q,"effort":"high"}}`, ts, model)
}

// tokenCountAt is a token_count event whose cumulative totals are in, cached,
// write, out and reasoning.
func tokenCountAt(ts string, in, cached, write, out, reasoning int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d,"total_tokens":%d},"last_token_usage":{},"model_context_window":258400}}}`,
		ts, in, cached, write, out, reasoning, in+out)
}

func liveUsage(t *testing.T, env []string, since time.Time) (provider.Usage, string, bool) {
	t.Helper()
	u, model, ok, err := codex.New().(provider.LiveUsageReporter).LiveUsage(env, sessionID, since)
	if err != nil {
		t.Fatalf("LiveUsage: %v", err)
	}
	return u, model, ok
}

func TestLiveUsageIsTheNewestTotalsAndTheModelInUse(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home,
		turnContextAt("2026-10-07T21:00:01.000Z", "gpt-6.1-sol"),
		tokenCountAt("2026-10-07T21:00:05.000Z", 100, 40, 0, 10, 3),
		tokenCountAt("2026-10-07T21:00:09.500Z", 300, 120, 0, 30, 9),
	)

	u, model, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	want := provider.Usage{InputTokens: 300, CachedInputTokens: 120, OutputTokens: 30, ReasoningOutputTokens: 9}
	if !ok || u != want || model != "gpt-6.1-sol" {
		t.Errorf("got %+v %q ok=%v, want %+v gpt-6.1-sol", u, model, ok, want)
	}
}

func TestLiveUsageSubtractsWhatTheConversationHadBeforeTheRun(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home,
		turnContextAt("2026-10-07T20:00:00.000Z", "gpt-old"),
		tokenCountAt("2026-10-07T20:00:05.000Z", 100, 50, 7, 10, 2),
		tokenCountAt("2026-10-07T20:59:59.999Z", 400, 300, 20, 50, 5),
		turnContextAt("2026-10-07T21:00:01.000Z", "gpt-new"),
		tokenCountAt("2026-10-07T21:00:05.000Z", 700, 500, 25, 80, 9),
		tokenCountAt("2026-10-07T21:00:09.000Z", 1400, 1100, 40, 150, 25),
	)

	u, model, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	want := provider.Usage{InputTokens: 1000, CachedInputTokens: 800, CacheWriteInputTokens: 20, OutputTokens: 100, ReasoningOutputTokens: 20}
	if !ok || u != want || model != "gpt-new" {
		t.Errorf("got %+v %q ok=%v, want %+v gpt-new", u, model, ok, want)
	}
}

func TestLiveUsageCountsAnEventStampedExactlyAtTheStartAsTheRuns(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home,
		tokenCountAt("2026-10-07T20:59:59.000Z", 10, 0, 0, 1, 0),
		tokenCountAt("2026-10-07T21:00:00.000Z", 25, 0, 0, 4, 0),
	)

	u, _, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	if !ok || u.InputTokens != 15 || u.OutputTokens != 3 {
		t.Errorf("got %+v ok=%v, want input 15 output 3", u, ok)
	}
}

func TestLiveUsageHasNothingBeforeTheRunProducedATokenCount(t *testing.T) {
	cases := map[string][]string{
		"no token_count at all":       {turnContextAt("2026-10-07T21:00:01.000Z", "gpt-x")},
		"only token_counts before it": {tokenCountAt("2026-10-07T20:59:59.000Z", 10, 0, 0, 1, 0)},
	}
	for name, lines := range cases {
		home := t.TempDir()
		writeRollout(t, home, lines...)

		u, _, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

		if ok || u != (provider.Usage{}) {
			t.Errorf("%s: got %+v ok=%v, want nothing", name, u, ok)
		}
	}
}

func TestLiveUsageHasNothingWithoutASessionFile(t *testing.T) {
	home := t.TempDir()

	u, model, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)
	if ok || u != (provider.Usage{}) || model != "" {
		t.Errorf("no file: got %+v %q ok=%v", u, model, ok)
	}

	writeRollout(t, home, tokenCountAt("2026-10-07T21:00:05.000Z", 1, 0, 0, 1, 0))
	for _, id := range []string{"", "*", "../x"} {
		_, _, ok, err := codex.New().(provider.LiveUsageReporter).LiveUsage([]string{"CODEX_HOME=" + home}, id, runStart)
		if ok || err != nil {
			t.Errorf("session id %q: ok=%v err=%v, want nothing and no error", id, ok, err)
		}
	}
}

func TestLiveUsageFloorsEachCountAtZero(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home,
		tokenCountAt("2026-10-07T20:00:00.000Z", 500, 400, 30, 90, 20),
		tokenCountAt("2026-10-07T21:00:05.000Z", 600, 300, 0, 100, 10),
	)

	u, _, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	want := provider.Usage{InputTokens: 100, OutputTokens: 10}
	if !ok || u != want {
		t.Errorf("got %+v ok=%v, want %+v", u, ok, want)
	}
}

func TestLiveUsageSkipsTokenCountsWithoutInfo(t *testing.T) {
	home := t.TempDir()
	nullInfo := `{"timestamp":"2026-10-07T21:00:09.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`
	writeRollout(t, home,
		tokenCountAt("2026-10-07T21:00:05.000Z", 100, 10, 0, 5, 1),
		nullInfo,
	)

	u, _, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	if !ok || u.InputTokens != 100 || u.OutputTokens != 5 {
		t.Errorf("got %+v ok=%v, want the totals of the last token_count that had info", u, ok)
	}
}

func TestLiveUsageIgnoresATornLastLine(t *testing.T) {
	home := t.TempDir()
	torn := tokenCountAt("2026-10-07T21:00:09.000Z", 999, 0, 0, 99, 0)
	writeRollout(t, home,
		tokenCountAt("2026-10-07T21:00:05.000Z", 100, 10, 0, 5, 1),
		torn[:len(torn)/2],
	)

	u, _, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	if !ok || u.InputTokens != 100 || u.OutputTokens != 5 {
		t.Errorf("got %+v ok=%v, want the newest complete token_count", u, ok)
	}
}

func TestLiveUsageWithoutATurnContextHasNoModel(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, tokenCountAt("2026-10-07T21:00:05.000Z", 100, 10, 0, 5, 1))

	u, model, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	if !ok || u.InputTokens != 100 || model != "" {
		t.Errorf("got %+v %q ok=%v, want usage and no model", u, model, ok)
	}
}

func TestLiveUsageSkipsLongLinesBetweenTheEventsItWants(t *testing.T) {
	home := t.TempDir()
	big := `{"timestamp":"2026-10-07T21:00:06.000Z","type":"response_item","payload":{"text":"` + strings.Repeat("x", 3*1024*1024) + `"}}`
	writeRollout(t, home,
		turnContextAt("2026-10-07T20:59:00.000Z", "gpt-far"),
		tokenCountAt("2026-10-07T20:59:30.000Z", 40, 0, 0, 4, 0),
		tokenCountAt("2026-10-07T21:00:05.000Z", 100, 0, 0, 10, 0),
		big,
		tokenCountAt("2026-10-07T21:00:07.000Z", 200, 0, 0, 20, 0),
		big,
	)

	u, model, ok := liveUsage(t, []string{"CODEX_HOME=" + home}, runStart)

	if !ok || u.InputTokens != 160 || u.OutputTokens != 16 || model != "gpt-far" {
		t.Errorf("got %+v %q ok=%v, want input 160 output 16 gpt-far", u, model, ok)
	}
}

func TestLiveUsageFindsCodexUnderHome(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home+"/.codex", tokenCountAt("2026-10-07T21:00:05.000Z", 100, 10, 0, 5, 1))

	u, _, ok := liveUsage(t, []string{"HOME=" + home}, runStart)

	if !ok || u.InputTokens != 100 {
		t.Errorf("got %+v ok=%v", u, ok)
	}
}
