package telemetry_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

// etaRun is a folded run as telemetry.Fold yields it: numbers are json.Number.
func etaRun(scenario, cwd string, durationMS int, outcome string) map[string]any {
	return map[string]any{
		"kind": "run", "scenario": scenario, "cwd": cwd, "outcome": outcome,
		"duration_ms": json.Number(strconv.Itoa(durationMS)),
	}
}

// byDir keys a directory by itself, except that the listed dirs share a repo.
func byDir(shared map[string]string) func(string) string {
	return func(dir string) string {
		if key, ok := shared[dir]; ok {
			return key
		}
		return dir
	}
}

func wantETA(t *testing.T, got telemetry.ETA, basis string, ms int64, samples, repos int) {
	t.Helper()
	if got.Basis != basis || got.MS == nil || *got.MS != ms || got.Samples != samples || got.Repos != repos {
		gotMS := int64(-1)
		if got.MS != nil {
			gotMS = *got.MS
		}
		t.Errorf("eta = %s ms=%d samples=%d repos=%d, want %s ms=%d samples=%d repos=%d",
			got.Basis, gotMS, got.Samples, got.Repos, basis, ms, samples, repos)
	}
}

func TestEstimateETAUsesTheMeanOfTheTargetRepo(t *testing.T) {
	runs := []map[string]any{
		etaRun("code-review", "/work/a", 100000, "ok"),
		etaRun("code-review", "/work/a-sub", 200000, "ok"),
		etaRun("code-review", "/work/b", 900000, "ok"),
	}
	key := byDir(map[string]string{"/work/a": "/work/a/.git", "/work/a-sub": "/work/a/.git", "/work/b": "/work/b/.git"})

	got := telemetry.EstimateETA(runs, "code-review", "/work/a", key)

	wantETA(t, got, "repo", 150000, 2, 1)
	if got.Repo != "/work/a/.git" {
		t.Errorf("repo = %q, want /work/a/.git", got.Repo)
	}
}

func TestEstimateETAWithoutRepoHistoryAveragesTheRepoMeans(t *testing.T) {
	runs := []map[string]any{
		etaRun("code-review", "/work/a", 100000, "ok"),
		etaRun("code-review", "/work/a", 200000, "ok"),
		etaRun("code-review", "/work/a-sub", 300000, "ok"),
		etaRun("code-review", "/work/b", 1000000, "ok"),
	}
	key := byDir(map[string]string{"/work/a": "/work/a/.git", "/work/a-sub": "/work/a/.git", "/work/b": "/work/b/.git"})

	got := telemetry.EstimateETA(runs, "code-review", "/work/c", key)

	// a averages 200000 over three runs and b 1000000 over one: the mean of the
	// two repo means is 600000, where the pooled mean would be 400000.
	wantETA(t, got, "global", 600000, 4, 2)
	if got.Repo != "/work/c" {
		t.Errorf("repo = %q, want /work/c", got.Repo)
	}
}

func TestEstimateETACountsEveryOutcomeOfTheScenario(t *testing.T) {
	runs := []map[string]any{
		etaRun("code-review", "/work/a", 100000, "ok"),
		etaRun("code-review", "/work/a", 200000, "error"),
		etaRun("code-review", "/work/a", 300000, "timeout"),
		etaRun("code-review", "/work/a", 400000, "lost"),
		etaRun("code-review", "/work/a", 500000, "cancelled"),
		etaRun("code-review", "/work/a", 600000, ""),
		etaRun("other", "/work/a", 90000000, "ok"),
	}

	got := telemetry.EstimateETA(runs, "code-review", "/work/a", byDir(nil))

	wantETA(t, got, "repo", 350000, 6, 1)
}

func TestEstimateETAIgnoresRunsWithoutCwdOrDuration(t *testing.T) {
	noDuration := etaRun("code-review", "/work/d", 0, "ok")
	delete(noDuration, "duration_ms")
	nullDuration := etaRun("code-review", "/work/e", 0, "ok")
	nullDuration["duration_ms"] = nil
	runs := []map[string]any{
		etaRun("code-review", "/work/a", 100000, "ok"),
		etaRun("code-review", "/work/a", 300000, "error"),
		etaRun("code-review", "/work/b", 600000, "lost"),
		etaRun("code-review", "", 90000000, "ok"),
		etaRun("other", "/work/c", 90000000, "ok"),
		noDuration,
		nullDuration,
	}

	got := telemetry.EstimateETA(runs, "code-review", "/work/z", byDir(nil))

	// a averages 200000 and b 600000.
	wantETA(t, got, "global", 400000, 3, 2)
}

func TestEstimateETAWithoutHistoryHasNoEstimate(t *testing.T) {
	runs := []map[string]any{etaRun("other", "/work/a", 100000, "ok")}

	got := telemetry.EstimateETA(runs, "code-review", "/work/a", byDir(nil))

	if got.Basis != "none" || got.MS != nil || got.Samples != 0 || got.Repos != 0 || got.Repo != "/work/a" {
		t.Errorf("eta = %+v, want basis none, no estimate, 0 samples, 0 repos, repo /work/a", got)
	}
}

func TestEstimateETAResolvesEachDirectoryOnce(t *testing.T) {
	runs := []map[string]any{
		etaRun("code-review", "/work/a", 100000, "ok"),
		etaRun("code-review", "/work/a", 200000, "ok"),
		etaRun("code-review", "/work/b", 300000, "ok"),
	}
	calls := map[string]int{}
	key := func(dir string) string { calls[dir]++; return dir }

	telemetry.EstimateETA(runs, "code-review", "/work/a", key)

	for _, dir := range []string{"/work/a", "/work/b"} {
		if calls[dir] != 1 {
			t.Errorf("%s resolved %d times, want 1", dir, calls[dir])
		}
	}
}

func TestEstimateETARoundsHalfAwayFromZero(t *testing.T) {
	runs := []map[string]any{
		etaRun("code-review", "/work/a", 1000, "ok"),
		etaRun("code-review", "/work/a", 1001, "ok"),
	}

	got := telemetry.EstimateETA(runs, "code-review", "/work/a", byDir(nil))

	wantETA(t, got, "repo", 1001, 2, 1)
}
