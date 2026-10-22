package codex_test

import (
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

func threadUsageBefore(t *testing.T, env []string, id string) (provider.Usage, bool) {
	t.Helper()
	u, ok, err := codex.New().(provider.ThreadUsageReporter).ThreadUsageBefore(env, id, runStart)
	if err != nil {
		t.Fatalf("ThreadUsageBefore: %v", err)
	}
	return u, ok
}

func TestThreadUsageBeforeIsTheNewestTotalStampedBeforeTheTime(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home,
		tokenCountAt("2026-10-07T20:00:05.000Z", 100, 50, 7, 10, 2),
		tokenCountAt("2026-10-07T20:59:59.999Z", 400, 300, 20, 50, 5),
		tokenCountAt("2026-10-07T21:00:05.000Z", 700, 500, 25, 80, 9),
		tokenCountAt("2026-10-07T21:00:09.000Z", 1400, 1100, 40, 150, 25),
	)

	u, ok := threadUsageBefore(t, []string{"CODEX_HOME=" + home}, sessionID)

	want := provider.Usage{InputTokens: 400, CachedInputTokens: 300, CacheWriteInputTokens: 20, OutputTokens: 50, ReasoningOutputTokens: 5}
	if !ok || u != want {
		t.Errorf("got %+v ok=%v, want %+v", u, ok, want)
	}
}

func TestThreadUsageBeforeFindsTheTotalWhenTheRunWroteNoTokenCount(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, tokenCountAt("2026-10-07T20:59:59.000Z", 400, 300, 20, 50, 5))

	u, ok := threadUsageBefore(t, []string{"CODEX_HOME=" + home}, sessionID)

	if !ok || u.InputTokens != 400 {
		t.Errorf("got %+v ok=%v, want input 400", u, ok)
	}
}

func TestThreadUsageBeforeHasNothingWithoutAnEarlierTokenCount(t *testing.T) {
	nullInfo := `{"timestamp":"2026-10-07T20:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`
	cases := map[string][]string{
		"no token_count at all":            {turnContextAt("2026-10-07T20:00:01.000Z", "gpt-x")},
		"only token_counts at or after":    {tokenCountAt("2026-10-07T21:00:00.000Z", 10, 0, 0, 1, 0)},
		"only an earlier one without info": {nullInfo},
	}
	for name, lines := range cases {
		home := t.TempDir()
		writeRollout(t, home, lines...)

		u, ok := threadUsageBefore(t, []string{"CODEX_HOME=" + home}, sessionID)

		if ok || u != (provider.Usage{}) {
			t.Errorf("%s: got %+v ok=%v, want nothing", name, u, ok)
		}
	}
}

func TestThreadUsageBeforeHasNothingWithoutASessionFile(t *testing.T) {
	home := t.TempDir()
	env := []string{"CODEX_HOME=" + home}

	if u, ok := threadUsageBefore(t, env, sessionID); ok || u != (provider.Usage{}) {
		t.Errorf("no file: got %+v ok=%v", u, ok)
	}

	writeRollout(t, home, tokenCountAt("2026-10-07T20:00:05.000Z", 1, 0, 0, 1, 0))
	for _, id := range []string{"", "*", "../x"} {
		if u, ok := threadUsageBefore(t, env, id); ok || u != (provider.Usage{}) {
			t.Errorf("session id %q: got %+v ok=%v, want nothing", id, u, ok)
		}
	}
}
