package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

const anthropicUsage = `{"input_tokens":458,"output_tokens":18,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}`

// usageAddArgs is a valid invocation; extra flags follow.
func usageAddArgs(extra ...string) []string {
	return append([]string{"usage", "add", "--session-id", "sess-1", "--provider", "anthropic", "--model", "claude-haiku-5-5", "--source", "mod-summary"}, extra...)
}

func modelCalls(t *testing.T, home string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range telemetryRecords(t, home) {
		if rec["kind"] == "model_call" {
			out = append(out, rec)
		}
	}
	return out
}

func TestUsageAddNormalizesAnthropicUsage(t *testing.T) {
	s := newSandbox(t)
	s.stdin = anthropicUsage
	r := s.run(usageAddArgs("--run-id", "r-1")...)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	calls := modelCalls(t, s.home)
	if len(calls) != 1 {
		t.Fatalf("model calls = %v", calls)
	}
	c := calls[0]
	wantUsage := map[string]any{
		"input_tokens": float64(578), "cached_input_tokens": float64(100), "cache_write_input_tokens": float64(20),
		"output_tokens": float64(18), "reasoning_output_tokens": float64(0),
	}
	if !reflect.DeepEqual(c["usage"], wantUsage) {
		t.Errorf("usage = %v, want %v", c["usage"], wantUsage)
	}
	if c["v"] != float64(1) || c["session_id"] != "sess-1" || c["provider"] != "anthropic" ||
		c["model"] != "claude-haiku-5-5" || c["source"] != "mod-summary" || c["run_id"] != "r-1" {
		t.Errorf("record = %v", c)
	}
	if strings.TrimSpace(r.stdout) != c["call_id"] {
		t.Errorf("stdout %q, want the call id %v", r.stdout, c["call_id"])
	}
}

func TestUsageAddWritesTheRecordFieldsInOrder(t *testing.T) {
	s := newSandbox(t)
	s.stdin = `{"input_tokens":458,"output_tokens":18}`
	if r := s.run(usageAddArgs()...); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	line := strings.TrimSuffix(readFile(t, currentMonthFile(s.home)), "\n")
	pattern := regexp.MustCompile(`^\{"v":1,"kind":"model_call","call_id":"m-\d{8}T\d{6}Z-[0-9a-f]{8}","ts":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z",` +
		`"session_id":"sess-1","provider":"anthropic","model":"claude-haiku-5-5","source":"mod-summary","run_id":null,` +
		`"usage":\{"input_tokens":458,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":18,"reasoning_output_tokens":0\}\}$`)
	if !pattern.MatchString(line) {
		t.Errorf("line = %s", line)
	}
}

func TestUsageAddJSONKeys(t *testing.T) {
	s := newSandbox(t)
	s.stdin = anthropicUsage
	j := s.run(usageAddArgs("--json")...).json(t)
	keys := make([]string, 0, len(j))
	for k := range j {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"call_id", "exit_code", "recorded", "sdk_status"}) {
		t.Fatalf("keys = %v", keys)
	}
	calls := modelCalls(t, s.home)
	if len(calls) != 1 || j["sdk_status"] != "ok" || j["exit_code"] != float64(0) || j["recorded"] != true || j["call_id"] != calls[0]["call_id"] {
		t.Errorf("json = %v, calls = %v", j, calls)
	}
}

func TestUsageAddWritesNothingWhenEveryCountIsZero(t *testing.T) {
	for _, stdin := range []string{`{}`, `{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`} {
		s := newSandbox(t)
		s.stdin = stdin
		r := s.run(usageAddArgs()...)
		if r.code != 0 || r.stdout != "" {
			t.Errorf("%s: text mode exit %d stdout %q stderr %q", stdin, r.code, r.stdout, r.stderr)
		}
		j := s.run(usageAddArgs("--json")...).json(t)
		if j["recorded"] != false || j["call_id"] != nil || j["sdk_status"] != "ok" || j["exit_code"] != float64(0) {
			t.Errorf("%s: json = %v", stdin, j)
		}
		if exists(telemetryDir(s.home)) {
			if files, _ := filepath.Glob(filepath.Join(telemetryDir(s.home), "*.jsonl")); len(files) != 0 {
				t.Errorf("%s: wrote %v", stdin, files)
			}
		}
	}
}

func TestUsageAddRejectsBadFlagsBeforeWriting(t *testing.T) {
	cases := map[string][]string{
		"missing session":  {"usage", "add", "--provider", "anthropic", "--model", "m", "--source", "s"},
		"empty session":    {"usage", "add", "--session-id", "", "--provider", "anthropic", "--model", "m", "--source", "s"},
		"missing model":    {"usage", "add", "--session-id", "x", "--provider", "anthropic", "--source", "s"},
		"empty model":      {"usage", "add", "--session-id", "x", "--provider", "anthropic", "--model", "", "--source", "s"},
		"missing source":   {"usage", "add", "--session-id", "x", "--provider", "anthropic", "--model", "m"},
		"empty source":     {"usage", "add", "--session-id", "x", "--provider", "anthropic", "--model", "m", "--source", ""},
		"missing provider": {"usage", "add", "--session-id", "x", "--model", "m", "--source", "s"},
		"codex provider":   {"usage", "add", "--session-id", "x", "--provider", "codex", "--model", "m", "--source", "s"},
		"unknown flag":     append(usageAddArgs(), "--nope"),
		"positional":       append(usageAddArgs(), "extra"),
	}
	for name, args := range cases {
		s := newSandbox(t)
		s.stdin = anthropicUsage
		if r := s.run(args...); r.code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, r.code, r.stderr)
		}
		if len(telemetryRecords(t, s.home)) != 0 {
			t.Errorf("%s: wrote a record", name)
		}
	}
}

func TestUsageAddRejectsBadStdinBeforeWriting(t *testing.T) {
	bad := []string{
		``, `   `, `not json`, `[]`, `"text"`, `7`, `null`, `{"input_tokens":1} {"output_tokens":1}`, `{"input_tokens":1}x`,
		`{"input_tokens":-1}`, `{"output_tokens":1.5}`, `{"cache_read_input_tokens":"7"}`, `{"cache_creation_input_tokens":null}`,
		`{"input_tokens":1e2}`, `{"input_tokens":9223372036854775807,"output_tokens":1,"cache_read_input_tokens":1}`,
		`{"input_tokens":99999999999999999999}`,
	}
	for _, stdin := range bad {
		s := newSandbox(t)
		s.stdin = stdin
		r := s.run(usageAddArgs("--json")...)
		if r.code != 2 {
			t.Errorf("stdin %q: exit %d, want 2", stdin, r.code)
		}
		if j := r.json(t); j["sdk_status"] != "usage_error" || j["exit_code"] != float64(2) {
			t.Errorf("stdin %q: json = %v", stdin, j)
		}
		if len(telemetryRecords(t, s.home)) != 0 {
			t.Errorf("stdin %q: wrote a record", stdin)
		}
	}
}

func TestUsageNeedsASubcommand(t *testing.T) {
	for _, args := range [][]string{{"usage"}, {"usage", "bogus"}} {
		s := newSandbox(t)
		r := s.run(args...)
		if r.code != 2 || !strings.Contains(r.stderr, "usage: msgate usage add") {
			t.Errorf("%v: exit %d stderr %q", args, r.code, r.stderr)
		}
	}
}

func TestUsageAddFailsWithInternalErrorWhenTheTelemetryLockIsHeld(t *testing.T) {
	s := newSandbox(t)
	dir := telemetryDir(s.home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	s.stdin = anthropicUsage
	start := time.Now()
	r := s.run(usageAddArgs()...)
	if r.code != 70 {
		t.Fatalf("exit %d, want 70 (stderr %s)", r.code, r.stderr)
	}
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Errorf("took %v, want about the 5 s lock wait", elapsed)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Errorf("a record was written while the lock was held: %v", files)
	}
}

func TestRunsAndStatsIgnoreModelCalls(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts), recLine("r1", ts, `"source":"skill"`))
	runsBefore := s.run("runs").stdout
	statsBefore := s.run("stats", "--json").json(t)

	s.stdin = anthropicUsage
	if r := s.run(usageAddArgs("--run-id", "r1")...); r.code != 0 {
		t.Fatalf("usage add exit %d: %s", r.code, r.stderr)
	}
	if len(modelCalls(t, s.home)) != 1 {
		t.Fatal("the model call was not appended")
	}

	if got := s.run("runs"); got.stdout != runsBefore || got.stderr != "" {
		t.Errorf("runs changed: %q (stderr %q), was %q", got.stdout, got.stderr, runsBefore)
	}
	j := s.run("stats", "--json").json(t)
	if j["total"] != statsBefore["total"] || j["total"] != float64(1) {
		t.Errorf("total = %v, was %v", j["total"], statsBefore["total"])
	}
	if want := (map[string]any{"unknown_kind": float64(0), "unknown_version": float64(0), "unparseable": float64(0)}); !reflect.DeepEqual(j["skipped"], want) {
		t.Errorf("skipped = %v", j["skipped"])
	}
	if !reflect.DeepEqual(j["by_provider"], statsBefore["by_provider"]) || !reflect.DeepEqual(j["usage_totals"], statsBefore["usage_totals"]) {
		t.Errorf("stats changed: %v vs %v", j, statsBefore)
	}
}

// attrKey is the i-th distinct key of letters and underscores, as a key may be.
func attrKey(i int) string {
	return "k_" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

func TestUsageAddRecordsTheCallIDItIsGiven(t *testing.T) {
	for _, id := range []string{"followups:chk-1:e1:v0plus", "a", strings.Repeat("x", 200), "poc.s1_a-b"} {
		s := newSandbox(t)
		s.stdin = anthropicUsage
		r := s.run(usageAddArgs("--call-id", id, "--json")...)
		if r.code != 0 {
			t.Fatalf("%q: exit %d: %s", id, r.code, r.stderr)
		}
		calls := modelCalls(t, s.home)
		if len(calls) != 1 || calls[0]["call_id"] != id || r.json(t)["call_id"] != id {
			t.Errorf("%q: calls = %v, json = %v", id, calls, r.stdout)
		}
	}
}

func TestUsageAddRejectsABadCallIDBeforeWriting(t *testing.T) {
	for _, id := range []string{"", "has space", "-leading", ":leading", "a/b", "tab\tid", strings.Repeat("x", 201), "é"} {
		s := newSandbox(t)
		s.stdin = anthropicUsage
		if r := s.run(usageAddArgs("--call-id", id)...); r.code != 2 {
			t.Errorf("%q: exit %d, want 2 (stderr %q)", id, r.code, r.stderr)
		}
		if len(telemetryRecords(t, s.home)) != 0 {
			t.Errorf("%q: wrote a record", id)
		}
	}
}

func TestUsageAddRecordsEveryAttrInKeyOrderAfterTheUsage(t *testing.T) {
	s := newSandbox(t)
	s.stdin = `{"input_tokens":458,"output_tokens":18}`
	args := usageAddArgs("--attr", "stage=v0plus", "--attr", "check_id=chk-1", "--attr", "note=a=b c", "--attr", "empty=")
	if r := s.run(args...); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	line := strings.TrimSuffix(readFile(t, currentMonthFile(s.home)), "\n")
	want := `"reasoning_output_tokens":0},"attrs":{"check_id":"chk-1","empty":"","note":"a=b c","stage":"v0plus"}}`
	if !strings.HasSuffix(line, want) {
		t.Errorf("line = %s\nwant it to end with %s", line, want)
	}
}

func TestUsageAddRejectsBadAttrsBeforeWriting(t *testing.T) {
	many := []string{}
	for i := 0; i < 33; i++ {
		many = append(many, "--attr", attrKey(i)+"=v")
	}
	cases := map[string][]string{
		"no equals":     {"--attr", "stage"},
		"empty key":     {"--attr", "=v"},
		"digit in key":  {"--attr", "a9=v"},
		"camel case":    {"--attr", "checkId=v"},
		"dash in key":   {"--attr", "a-b=v"},
		"long key":      {"--attr", strings.Repeat("k", 33) + "=v"},
		"long value":    {"--attr", "k=" + strings.Repeat("v", 201)},
		"repeated key":  {"--attr", "k=1", "--attr", "k=2"},
		"too many":      many,
		"no value flag": {"--attr"},
	}
	for name, extra := range cases {
		s := newSandbox(t)
		s.stdin = anthropicUsage
		if r := s.run(usageAddArgs(extra...)...); r.code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, r.code, r.stderr)
		}
		if len(telemetryRecords(t, s.home)) != 0 {
			t.Errorf("%s: wrote a record", name)
		}
	}
}

func TestUsageAddAcceptsTheLimitsOfAnAttr(t *testing.T) {
	s := newSandbox(t)
	s.stdin = anthropicUsage
	extra := []string{"--attr", strings.Repeat("k", 32) + "=" + strings.Repeat("v", 200)}
	for i := 0; i < 31; i++ {
		extra = append(extra, "--attr", attrKey(i)+"=v")
	}
	if r := s.run(usageAddArgs(extra...)...); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	attrs, _ := modelCalls(t, s.home)[0]["attrs"].(map[string]any)
	if len(attrs) != 32 {
		t.Errorf("attrs = %d keys, want 32", len(attrs))
	}
}

func TestUsageAddRecordsTheTimeOfTheCallInTheMonthOfThatTime(t *testing.T) {
	s := newSandbox(t)
	s.stdin = anthropicUsage
	// A call made in September by the clock of its zone, but October in UTC, recorded later.
	r := s.run(usageAddArgs("--ts", "2026-09-30T21:00:00-03:00")...)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	records := telemetryRecords(t, s.home)
	if len(records) != 1 || records[0]["ts"] != "2026-10-01T00:00:00Z" {
		t.Fatalf("records = %v", records)
	}
	if !exists(filepath.Join(telemetryDir(s.home), "2026-10.jsonl")) {
		files, _ := filepath.Glob(filepath.Join(telemetryDir(s.home), "*"))
		t.Errorf("the record is not in the month of its ts: %v", files)
	}
}

func TestUsageAddDropsTheFractionOfASecondOfTheTime(t *testing.T) {
	s := newSandbox(t)
	s.stdin = anthropicUsage
	if r := s.run(usageAddArgs("--ts", "2026-10-12T17:25:54.987Z")...); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if got := modelCalls(t, s.home)[0]["ts"]; got != "2026-10-12T17:25:54Z" {
		t.Errorf("ts = %v", got)
	}
}

func TestUsageAddRejectsABadTimeBeforeWriting(t *testing.T) {
	for _, ts := range []string{"", "yesterday", "2026-10-12", "2026-10-12T17:25:54", "1760289954"} {
		s := newSandbox(t)
		s.stdin = anthropicUsage
		if r := s.run(usageAddArgs("--ts", ts)...); r.code != 2 {
			t.Errorf("%q: exit %d, want 2 (stderr %q)", ts, r.code, r.stderr)
		}
		if len(telemetryRecords(t, s.home)) != 0 {
			t.Errorf("%q: wrote a record", ts)
		}
	}
}
