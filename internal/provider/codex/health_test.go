package codex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

// checkHealth runs the Codex adapter's health check under env and bounds the
// instant it stamps: it returns the health and fails the test when CheckedAt is
// not within the call, in UTC.
func checkHealth(t *testing.T, env []string) provider.Health {
	t.Helper()
	return checkHealthWithin(t, context.Background(), env)
}

// checkHealthWithin is checkHealth under ctx.
func checkHealthWithin(t *testing.T, ctx context.Context, env []string) provider.Health {
	t.Helper()
	checker, ok := codex.New().(provider.HealthChecker)
	if !ok {
		t.Fatal("the codex adapter does not declare the health port")
	}
	before := time.Now()
	got := checker.CheckHealth(ctx, env)
	after := time.Now()
	if got.CheckedAt.Before(before) || got.CheckedAt.After(after) || got.CheckedAt.Location() != time.UTC {
		t.Errorf("CheckedAt = %v, want an instant in UTC between %v and %v", got.CheckedAt, before, after)
	}
	return got
}

func TestHealthIsNotInstalledWhenCodexIsNotOnThePath(t *testing.T) {
	got := checkHealth(t, []string{"PATH=" + t.TempDir()})

	if got.Available || got.Reason != "not-installed" || got.ResetsAt != nil {
		t.Errorf("health = %+v, want unavailable, not-installed, no reset", got)
	}
}

// healthEnv is an environment with the fake Codex on the PATH and the status
// page layer off, plus extra KEY=value entries.
func healthEnv(extra ...string) []string {
	return append([]string{"PATH=" + fakeBinDir, "MSGATE_STATUS_URL_OPENAI=off"}, extra...)
}

func TestHealthIsNotLoggedInWhenLoginStatusFails(t *testing.T) {
	got := checkHealth(t, healthEnv("FAKECODEX_LOGIN_EXIT=1"))

	if got.Available || got.Reason != "not-logged-in" || got.ResetsAt != nil {
		t.Errorf("health = %+v, want unavailable, not-logged-in, no reset", got)
	}
}

// rateLimitsFile writes result, the "result" of an account/rateLimits/read
// answer, to a file the fake Codex's app-server serves it from.
func rateLimitsFile(t *testing.T, result string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rate-limits.json")
	if err := os.WriteFile(path, []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHealthIsRateLimited(t *testing.T) {
	cases := []struct {
		name, result string
		resetsAt     string
	}{
		{"reached type set", `{"rateLimits":{"primary":{"usedPercent":40,"resetsAt":1793000000},"secondary":null,` +
			`"rateLimitReachedType":"workspace_member_usage_limit_reached"}}`, "2026-10-26T07:33:20Z"},
		{"a window at 100", `{"rateLimits":{"primary":{"usedPercent":30,"resetsAt":1793000000},` +
			`"secondary":{"usedPercent":100,"resetsAt":1793100000},"rateLimitReachedType":null}}`, "2026-10-27T11:20:00Z"},
		{"both windows at 100 wait for the later reset", `{"rateLimits":{"primary":{"usedPercent":100,"resetsAt":1793100000},` +
			`"secondary":{"usedPercent":100,"resetsAt":1793000000}}}`, "2026-10-27T11:20:00Z"},
	}
	for _, c := range cases {
		got := checkHealth(t, healthEnv("FAKECODEX_RATE_LIMITS="+rateLimitsFile(t, c.result)))

		want, _ := time.Parse(time.RFC3339, c.resetsAt)
		if got.Available || got.Reason != "rate-limited" || got.ResetsAt == nil || !got.ResetsAt.Equal(want) || got.ResetsAt.Location() != time.UTC {
			t.Errorf("%s: health = %+v, want unavailable, rate-limited, resets at %s", c.name, got, c.resetsAt)
		}
	}
}

func TestHealthIsAvailableBelowTheLimit(t *testing.T) {
	cases := map[string]string{
		"a recorded answer of the real Codex": "../../../testdata/codex/rate-limits-ok.json",
		"a window at 99":                      rateLimitsFile(t, `{"rateLimits":{"primary":{"usedPercent":99,"resetsAt":1793000000},"secondary":null}}`),
	}
	for name, path := range cases {
		got := checkHealth(t, healthEnv("FAKECODEX_RATE_LIMITS="+path))

		if !got.Available || got.Reason != "" || got.ResetsAt != nil {
			t.Errorf("%s: health = %+v, want available with no reason and no reset", name, got)
		}
	}
}

// statusPage serves a summary.json listing the OpenAI components the adapter
// watches at the given statuses, and returns its URL.
func statusPage(t *testing.T, responses, desktop string) string {
	t.Helper()
	body := `{"components":[{"name":"Chat Completions","status":"major_outage"},` +
		`{"name":"Responses","status":"` + responses + `"},` +
		`{"name":"Codex in ChatGPT Desktop","status":"` + desktop + `"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// statusEnv is an environment whose login and rate limits pass and whose status
// page is at url.
func statusEnv(url string, extra ...string) []string {
	return append([]string{
		"PATH=" + fakeBinDir, "MSGATE_STATUS_URL_OPENAI=" + url,
		"FAKECODEX_RATE_LIMITS=../../../testdata/codex/rate-limits-ok.json",
	}, extra...)
}

func TestHealthIsAnOutageWhenAWatchedComponentIsAtMajorOutage(t *testing.T) {
	for name, url := range map[string]string{
		"Responses":                statusPage(t, "major_outage", "operational"),
		"Codex in ChatGPT Desktop": statusPage(t, "operational", "major_outage"),
	} {
		got := checkHealth(t, statusEnv(url))

		if got.Available || got.Reason != "provider-outage" || got.ResetsAt != nil {
			t.Errorf("%s: health = %+v, want unavailable, provider-outage, no reset", name, got)
		}
	}
}

func TestHealthStaysAvailableOnALesserStatus(t *testing.T) {
	got := checkHealth(t, statusEnv(statusPage(t, "partial_outage", "degraded_performance")))

	if !got.Available || got.Reason != "" {
		t.Errorf("health = %+v, want available with no reason (Chat Completions is not watched)", got)
	}
}

func TestTheFirstFailingLayerWins(t *testing.T) {
	down := statusPage(t, "major_outage", "major_outage")
	spent := rateLimitsFile(t, `{"rateLimits":{"primary":{"usedPercent":100,"resetsAt":1793000000}}}`)

	loginFirst := checkHealth(t, statusEnv(down, "FAKECODEX_LOGIN_EXIT=1", "FAKECODEX_RATE_LIMITS="+spent))
	limitFirst := checkHealth(t, statusEnv(down, "FAKECODEX_RATE_LIMITS="+spent))

	if loginFirst.Reason != "not-logged-in" || limitFirst.Reason != "rate-limited" {
		t.Errorf("reasons = %q, %q; want not-logged-in, rate-limited", loginFirst.Reason, limitFirst.Reason)
	}
}

// unrunnableCodexDir holds an executable named codex that the system cannot
// start: its interpreter does not exist.
func unrunnableCodexDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/nonexistent/interpreter\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHealthIsAvailableWithReasonUnknownWhenNoLayerCouldRun(t *testing.T) {
	cases := []struct {
		name    string
		got     provider.Health
		skipped []string
	}{
		{"a codex that cannot start", checkHealth(t, []string{"PATH=" + unrunnableCodexDir(t), "MSGATE_STATUS_URL_OPENAI=off"}),
			[]string{"login status: could not start", "app-server: could not start"}},
		{"codex hangs, the status page is off", checkHealthWithin(t, mustTimeout(t, 300*time.Millisecond), healthEnv("FAKECODEX_SLEEP_MS=5000")),
			[]string{"login status: timed out", "app-server: timed out"}},
		{"codex cannot start, the status page is unreachable", checkHealth(t, []string{"PATH=" + unrunnableCodexDir(t), "MSGATE_STATUS_URL_OPENAI=http://127.0.0.1:1/summary.json"}),
			[]string{"login status: could not start", "app-server: could not start", "status page: unreachable"}},
	}
	for _, c := range cases {
		if !c.got.Available || c.got.Reason != "unknown" || c.got.ResetsAt != nil || !slices.Equal(c.got.Skipped, c.skipped) {
			t.Errorf("%s: health = %+v, want available, unknown, skipped %q", c.name, c.got, c.skipped)
		}
	}
}

// mustTimeout is a context that ends after d.
func mustTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestALayerThatCannotRunLeavesTheDecisionToTheNext(t *testing.T) {
	down := statusPage(t, "major_outage", "operational")
	garbage := rateLimitsFile(t, "this is not json")
	cases := []struct {
		name    string
		env     []string
		want    provider.Health
		skipped []string
	}{
		{"nothing runs, the status page is down", []string{"PATH=" + unrunnableCodexDir(t), "MSGATE_STATUS_URL_OPENAI=" + down},
			provider.Health{Reason: "provider-outage"}, []string{"login status: could not start", "app-server: could not start"}},
		{"app-server answers garbage, the status page is down", statusEnv(down, "FAKECODEX_RATE_LIMITS="+garbage),
			provider.Health{Reason: "provider-outage"}, []string{"app-server: unreadable reply"}},
		{"app-server answers an error, the status page is down", append(statusEnv(down), "FAKECODEX_RATE_LIMITS="),
			provider.Health{Reason: "provider-outage"}, []string{"app-server: error reply"}},
		{"app-server answers garbage, login passed", healthEnv("FAKECODEX_RATE_LIMITS=" + garbage),
			provider.Health{Available: true}, []string{"app-server: unreadable reply"}},
		{"status page unreachable, rate limits fine", statusEnv("http://127.0.0.1:1/summary.json"),
			provider.Health{Available: true}, []string{"status page: unreachable"}},
		{"status page answers garbage, rate limits fine", statusEnv(garbagePage(t)),
			provider.Health{Available: true}, []string{"status page: unreadable answer"}},
	}
	for _, c := range cases {
		got := checkHealth(t, c.env)

		if got.Available != c.want.Available || got.Reason != c.want.Reason || got.ResetsAt != nil || !slices.Equal(got.Skipped, c.skipped) {
			t.Errorf("%s: health = %+v, want available=%v reason=%q skipped=%q", c.name, got, c.want.Available, c.want.Reason, c.skipped)
		}
	}
}

// garbagePage serves a status page that is not a summary.
func garbagePage(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAStatusPageSwitchedOffIsNotListedAsSkipped(t *testing.T) {
	got := checkHealth(t, statusEnv("off"))

	if !got.Available || got.Reason != "" || len(got.Skipped) != 0 {
		t.Errorf("health = %+v, want available with nothing skipped", got)
	}
}

func TestEverythingThatRanLeavesNothingSkipped(t *testing.T) {
	got := checkHealth(t, statusEnv(statusPage(t, "operational", "operational")))

	if !got.Available || got.Reason != "" || len(got.Skipped) != 0 {
		t.Errorf("health = %+v, want available with nothing skipped", got)
	}
}

// launcherDir holds an executable named codex that behaves as Codex does when
// it is a launcher script: `login status` passes, and any other subcommand runs
// the fake Codex as a background child and waits for it. The child inherits the
// launcher's standard output, so killing the launcher alone leaves it holding
// the pipe.
func launcherDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = login ] && exit 0\n\"$FAKECODEX_REAL\" \"$@\" &\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAnAppServerWhoseChildHoldsTheOutputIsGivenUpOnAtTheDeadline(t *testing.T) {
	env := []string{
		"PATH=" + launcherDir(t), "MSGATE_STATUS_URL_OPENAI=off",
		"FAKECODEX_REAL=" + filepath.Join(fakeBinDir, "codex"), "FAKECODEX_SLEEP_MS=5000",
	}
	start := time.Now()

	got := checkHealthWithin(t, mustTimeout(t, time.Second), env)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the check took %v, want it to give up within 3s of a 1s deadline", elapsed)
	}
	if want := []string{"app-server: timed out"}; !got.Available || got.Reason != "" || !slices.Equal(got.Skipped, want) {
		t.Errorf("health = %+v, want available, skipped %q", got, want)
	}
}

func TestALoginStatusWhoseChildOutlivesTheLauncherDoesNotHoldTheCheck(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n\"$FAKECODEX_REAL\" \"$@\" &\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + dir, "MSGATE_STATUS_URL_OPENAI=off",
		"FAKECODEX_REAL=" + filepath.Join(fakeBinDir, "codex"), "FAKECODEX_SLEEP_MS=5000",
	}
	start := time.Now()

	got := checkHealthWithin(t, mustTimeout(t, time.Second), env)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the check took %v, want it to give up within 3s of a 1s deadline", elapsed)
	}
	if want := []string{"login status: timed out", "app-server: timed out"}; got.Reason != "unknown" || !slices.Equal(got.Skipped, want) {
		t.Errorf("health = %+v, want unknown, skipped %q", got, want)
	}
}
