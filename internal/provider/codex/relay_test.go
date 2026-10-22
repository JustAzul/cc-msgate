package codex_test

import (
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

func relayer(t *testing.T) provider.Relayer {
	t.Helper()
	r, ok := codex.New().(provider.Relayer)
	if !ok {
		t.Fatal("the codex adapter is not a provider.Relayer")
	}
	return r
}

func TestRelayPromptNamesTheChildAndTheToolsThatReachIt(t *testing.T) {
	got := string(relayer(t).RelayPrompt("helper", []byte("q")))

	for _, want := range []string{`"helper"`, "`followup_task`", "`wait_agent`"} {
		if !strings.Contains(got, want) {
			t.Errorf("RelayPrompt = %q, want it to contain %s", got, want)
		}
	}
}

func TestRelayPromptEndsWithThePromptBytesUnchanged(t *testing.T) {
	prompts := map[string]string{
		"plain":                   "q",
		"a trailing newline":      "line one\n\nline two \n",
		"a backtick":              "run `ls` now",
		"a line like the heading": "before\nTask for x:\nafter",
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			got := string(relayer(t).RelayPrompt("a", []byte(prompt)))

			if !strings.HasSuffix(got, "\n"+prompt) {
				t.Errorf("RelayPrompt = %q, want it to end with a line break and %q", got, prompt)
			}
		})
	}
}
