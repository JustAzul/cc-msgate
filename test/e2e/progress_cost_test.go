package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// liveRun admits a background run on the exec-ok stream that the fake keeps
// running, waits until it has started and its conversation holds the provider
// session, and returns when it started. CODEX_HOME is a fresh directory.
func liveRun(t *testing.T, s *sandbox, runID string, args ...string) (start time.Time, codexHome string) {
	t.Helper()
	codexHome = t.TempDir()
	s.set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("FAKECODEX_SLEEP_MS", "6000").set("CODEX_HOME", codexHome)
	r := admitJob(t, s, runID, append(append([]string{"--json", "--cwd", gitRepo(t)}, args...), "q")...)
	conv := r.json(t)["conversation_id"].(string)
	return startedAt(t, s, runID), awaitSession(t, s, conv, codexHome)
}

// startedAt waits for the run to have started and returns when.
func startedAt(t *testing.T, s *sandbox, runID string) time.Time {
	t.Helper()
	st := waitForState(t, s, runID, "a start time", func(m map[string]any) bool {
		started, _ := m["started_at"].(string)
		return m["state"] == "running" && started != ""
	})
	at, err := time.Parse(time.RFC3339Nano, st["started_at"].(string))
	if err != nil {
		t.Fatalf("started_at: %v", err)
	}
	return at
}

// awaitSession waits for the conversation to record its provider session and
// returns codexHome unchanged, for chaining.
func awaitSession(t *testing.T, s *sandbox, conv, codexHome string) string {
	t.Helper()
	file := filepath.Join(s.home, "conversations", conv+".json")
	untilTrue(t, "the provider session id", func() bool { return readJSONFile(t, file)["provider_session_id"] != nil })
	return codexHome
}

func stamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z") }

func sessionTurnContext(at time.Time, model string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":%q,"effort":"high"}}`, stamp(at), model)
}

func sessionTokenCount(at time.Time, u tokens) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d,"total_tokens":%d},"last_token_usage":{},"model_context_window":258400}}}`,
		stamp(at), u.input, u.cached, u.write, u.output, u.reasoning, u.input+u.output)
}

// writeSession lays out the session file of exec-ok's provider session.
func writeSession(t *testing.T, codexHome string, lines ...string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "10", "07")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-10-07T00-00-00-"+execOKSession+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// progressLive reads the progress of a run, which must exit 0 and, as
// following a run is routine, say nothing on stderr.
func progressLive(t *testing.T, s *sandbox, runID string) map[string]any {
	t.Helper()
	got, stderr := progressWithStderr(t, s, runID)
	if stderr != "" {
		t.Errorf("progress wrote to stderr: %q", stderr)
	}
	return got
}

func progressWithStderr(t *testing.T, s *sandbox, runID string) (map[string]any, string) {
	t.Helper()
	r := s.run("progress", runID, "--json")
	if r.code != 0 {
		t.Fatalf("progress exit %d %s", r.code, r.stderr)
	}
	return r.json(t), r.stderr
}

// wantUsage checks the usage and cost a progress read reported. A nil want
// means null.
func wantUsage(t *testing.T, got map[string]any, usage map[string]any, cost any) {
	t.Helper()
	gotUsage, present := got["usage"]
	if !present {
		t.Fatalf("progress has no usage key: %v", got)
	}
	if usage == nil {
		if gotUsage != nil {
			t.Errorf("usage = %v, want null", gotUsage)
		}
	} else if !reflect.DeepEqual(gotUsage, any(usage)) {
		t.Errorf("usage = %v, want %v", gotUsage, usage)
	}
	if c, present := got["cost_usd"]; !present || c != cost {
		t.Errorf("cost_usd = %v (present %v), want %v", c, present, cost)
	}
}

var oneMillion = map[string]any{
	"input_tokens": float64(1000000), "cached_input_tokens": float64(800000), "cache_write_input_tokens": float64(0),
	"output_tokens": float64(10000), "reasoning_output_tokens": float64(2000),
}

func TestProgressReportsTheLiveCostOfARunningRun(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "lc1")
	writeSession(t, home,
		sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(2*time.Second), tokens{input: 400000, cached: 300000, output: 4000, reasoning: 500}),
		sessionTokenCount(start.Add(3*time.Second), tokens{1000000, 800000, 0, 10000, 2000}),
	)

	got := progressLive(t, s, "lc1")

	wantUsage(t, got, oneMillion, "0.680000")
	if got["state"] != "running" {
		t.Errorf("state = %v, want running", got["state"])
	}
}

func TestProgressSubtractsWhatTheConversationUsedBeforeTheRun(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "lc2")
	writeSession(t, home,
		sessionTurnContext(start.Add(-time.Hour), "m-old"),
		sessionTokenCount(start.Add(-time.Hour+time.Second), tokens{input: 400000, cached: 300000, output: 5000}),
		sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(2*time.Second), tokens{1400000, 1100000, 0, 15000, 2000}),
	)

	got := progressLive(t, s, "lc2")

	wantUsage(t, got, oneMillion, "0.680000")
}

func TestProgressHasNoUsageBeforeTheRunHasAny(t *testing.T) {
	t.Run("no session file", func(t *testing.T) {
		s := newSandbox(t)
		costCache(t, s)
		liveRun(t, s, "nu1")
		wantUsage(t, progressLive(t, s, "nu1"), nil, nil)
	})
	t.Run("no token_count after the start", func(t *testing.T) {
		s := newSandbox(t)
		costCache(t, s)
		start, home := liveRun(t, s, "nu2")
		writeSession(t, home,
			sessionTokenCount(start.Add(-time.Minute), tokens{input: 400000, cached: 300000, output: 5000}),
			sessionTurnContext(start.Add(time.Second), "m-sol"),
		)
		wantUsage(t, progressLive(t, s, "nu2"), nil, nil)
	})
	t.Run("no provider session id", func(t *testing.T) {
		s := newSandbox(t)
		costCache(t, s)
		home := t.TempDir()
		s.set("FAKECODEX_OUTPUT", "final").set("FAKECODEX_SLEEP_MS", "6000").set("CODEX_HOME", home)
		admitJob(t, s, "nu3", "--cwd", gitRepo(t), "q")
		start := startedAt(t, s, "nu3")
		writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-sol"),
			sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))
		wantUsage(t, progressLive(t, s, "nu3"), nil, nil)
	})
	t.Run("no start time recorded", func(t *testing.T) {
		s := newSandbox(t)
		costCache(t, s)
		start, home := liveRun(t, s, "nu4")
		stateFile := filepath.Join(s.home, "runs", "nu4", "state.json")
		state := readJSONFile(t, stateFile)
		state["started_at"] = nil
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stateFile, data, 0o600); err != nil {
			t.Fatal(err)
		}
		writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-sol"),
			sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))
		got := progressLive(t, s, "nu4")
		if got["state"] != "running" {
			t.Fatalf("state = %v, want running", got["state"])
		}
		wantUsage(t, got, nil, nil)
	})
}

func TestProgressReportsUsageWithoutAPriceAsNoCost(t *testing.T) {
	t.Run("model without a price", func(t *testing.T) {
		s := newSandbox(t)
		costCache(t, s)
		start, home := liveRun(t, s, "np1")
		writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-hidden"),
			sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))
		wantUsage(t, progressLive(t, s, "np1"), oneMillion, nil)
	})
	t.Run("no usable price cache", func(t *testing.T) {
		s := newSandbox(t)
		start, home := liveRun(t, s, "np2")
		writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-sol"),
			sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))
		wantUsage(t, progressLive(t, s, "np2"), oneMillion, nil)
	})
}

func TestProgressPricesAtTheRequestedModelWhenTheSessionNamesNone(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "fm1", "--model", "m-sol")
	writeSession(t, home, sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))

	got := progressLive(t, s, "fm1")

	wantUsage(t, got, oneMillion, "0.680000")
}

func TestProgressIgnoresASessionFileTornAtItsEnd(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "tn1")
	newest := sessionTokenCount(start.Add(3*time.Second), tokens{input: 9000000, cached: 1, output: 9})
	writeSession(t, home,
		sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}),
		newest[:len(newest)/2],
	)

	got := progressLive(t, s, "tn1")

	wantUsage(t, got, oneMillion, "0.680000")
}

func TestProgressReadsALongSessionFileFromItsEnd(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "lg1")
	dir := filepath.Join(home, "sessions", "2026", "10", "07")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "rollout-2026-10-07T00-00-00-"+execOKSession+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	filler := `{"timestamp":"2026-10-07T00:00:01.000Z","type":"response_item","payload":{"text":"` + strings.Repeat("x", 8000) + `"}}` + "\n"
	for written := 0; written < 50<<20; written += len(filler) {
		if _, err := f.WriteString(filler); err != nil {
			t.Fatal(err)
		}
	}
	tail := strings.Join([]string{
		sessionTokenCount(start.Add(-time.Hour), tokens{input: 400000, cached: 300000, output: 5000}),
		sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(2*time.Second), tokens{1400000, 1100000, 0, 15000, 2000}),
	}, "\n") + "\n"
	if _, err := f.WriteString(tail); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if len(tail) > 20<<10 {
		t.Fatalf("the run's events are %d bytes, want at most 20 KiB", len(tail))
	}

	began := time.Now()
	got := progressLive(t, s, "lg1")
	t.Logf("progress --json over a %d MiB session file took %v", 50, time.Since(began))

	wantUsage(t, got, oneMillion, "0.680000")
}

func TestProgressWarnsAndKeepsGoingWhenTheSessionFileCannotBeRead(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	_, home := liveRun(t, s, "ur1")
	// A directory where the session file should be: it matches, and cannot be read.
	dir := filepath.Join(home, "sessions", "2026", "10", "07", "rollout-2026-10-07T00-00-00-"+execOKSession+".jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	got, stderr := progressWithStderr(t, s, "ur1")

	wantUsage(t, got, nil, nil)
	if lines := strings.Split(strings.TrimSpace(stderr), "\n"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "msgate: warning: reading the live usage of run ur1: ") {
		t.Errorf("stderr = %q, want one warning naming the run", stderr)
	}
}

func TestProgressOfAFinishedRunReportsNoLiveUsage(t *testing.T) {
	for _, outcome := range []string{"done", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))
			costCache(t, s)
			home := t.TempDir()
			s.set("CODEX_HOME", home)
			if outcome == "failed" {
				s.set("FAKECODEX_EXIT", "1")
			}
			s.run("exec", "--run-id", "fin1", "--model", "m-sol", "q")
			state := stateOf(t, s, "fin1")
			if state["state"] != outcome {
				t.Fatalf("state = %v, want %s", state["state"], outcome)
			}
			start, _ := time.Parse(time.RFC3339Nano, state["started_at"].(string))
			writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-sol"),
				sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))

			wantUsage(t, progressLive(t, s, "fin1"), nil, nil)
		})
	}
}

func TestProgressCountsTheStartSecondAsBeforeTheRun(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "ss1")
	// started_at is kept to the second, so the previous turn's last count can
	// fall inside that same second.
	writeSession(t, home,
		sessionTurnContext(start.Add(-time.Minute), "m-sol"),
		sessionTokenCount(start.Add(500*time.Millisecond), tokens{input: 400000, cached: 300000, output: 5000}),
		sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(3*time.Second), tokens{1400000, 1100000, 0, 15000, 2000}),
	)

	wantUsage(t, progressLive(t, s, "ss1"), oneMillion, "0.680000")
}

func TestProgressWarnsWhenTheConversationCannotBeRead(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "cr1")
	writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))
	conv := stateOf(t, s, "cr1")["conversation_id"].(string)
	if err := os.WriteFile(filepath.Join(s.home, "conversations", conv+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, stderr := progressWithStderr(t, s, "cr1")

	wantUsage(t, got, nil, nil)
	if lines := strings.Split(strings.TrimSpace(stderr), "\n"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "msgate: warning: reading the conversation of run cr1: ") {
		t.Errorf("stderr = %q, want one warning naming the run", stderr)
	}
}

func TestProgressWarnsWhenTheRunStartCannotBeRead(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "bs1")
	stateFile := filepath.Join(s.home, "runs", "bs1", "state.json")
	state := readJSONFile(t, stateFile)
	state["started_at"] = "not a time"
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	writeSession(t, home, sessionTurnContext(start.Add(time.Second), "m-sol"),
		sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))

	got, stderr := progressWithStderr(t, s, "bs1")

	wantUsage(t, got, nil, nil)
	if lines := strings.Split(strings.TrimSpace(stderr), "\n"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "msgate: warning: reading the start of run bs1: ") {
		t.Errorf("stderr = %q, want one warning naming the run", stderr)
	}
}

func TestProgressWarnsWhenTheRequestCannotBeRead(t *testing.T) {
	s := newSandbox(t)
	costCache(t, s)
	start, home := liveRun(t, s, "br1", "--model", "m-sol")
	if err := os.WriteFile(filepath.Join(s.home, "runs", "br1", "request.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No turn_context: the model would come from the request.
	writeSession(t, home, sessionTokenCount(start.Add(2*time.Second), tokens{1000000, 800000, 0, 10000, 2000}))

	got, stderr := progressWithStderr(t, s, "br1")

	if got["usage"] == nil || got["cost_usd"] != nil {
		t.Errorf("usage = %v, cost_usd = %v; want usage and no cost", got["usage"], got["cost_usd"])
	}
	if lines := strings.Split(strings.TrimSpace(stderr), "\n"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "msgate: warning: reading the request of run br1: ") {
		t.Errorf("stderr = %q, want one warning naming the run", stderr)
	}
}
