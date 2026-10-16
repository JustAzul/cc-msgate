package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// byPurposeRuns sums the runs of every purpose and origin of a by_purpose object.
func byPurposeRuns(t *testing.T, byPurpose any) int {
	t.Helper()
	total := 0
	for _, origins := range byPurpose.(map[string]any) {
		for _, bucket := range origins.(map[string]any) {
			total += int(bucket.(map[string]any)["runs"].(float64))
		}
	}
	return total
}

func TestStatsByPurposeAndOrigin(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts),
		recLine("bp1", ts, `"purpose":"code-review","source":"hook-post-commit","outcome":"ok","attrs":{"review.findings":{"total":3,"high":1}}`),
		recLine("bp2", ts, `"purpose":"code-review","source":"agent","outcome":"ok"`),
		recLine("bp3", ts, `"purpose":"adhoc","source":"cli","outcome":"error"`),
		recLine("bp4", ts, `"purpose":"adhoc","source":"cli"`),
		recLine("bp5", ts, `"purpose":"second-opinion","source":"plugin:followups","outcome":"clean"`),
		recLine("bp6", ts, `"purpose":"second-opinion","source":"mod","outcome":"clean"`),
	)

	j := s.run("stats", "--all", "--json").json(t)

	want := map[string]any{
		"adhoc": map[string]any{"cli": map[string]any{
			"runs": float64(2), "outcomes": map[string]any{"error": float64(1), "no-outcome": float64(1)},
			"findings": map[string]any{"annotated": float64(0)},
		}},
		"code-review": map[string]any{
			"cli": map[string]any{
				"runs": float64(1), "outcomes": map[string]any{"ok": float64(1)},
				"findings": map[string]any{
					"total": float64(3), "critical": float64(0), "high": float64(1), "medium": float64(0), "low": float64(0), "violations": float64(0),
					"annotated": float64(1), "with_findings": float64(1), "clean": float64(0),
				},
			},
			"plugin": map[string]any{
				"runs": float64(1), "outcomes": map[string]any{"ok": float64(1)}, "findings": map[string]any{"annotated": float64(0)},
			},
		},
		"second-opinion": map[string]any{"plugin": map[string]any{
			"runs": float64(2), "outcomes": map[string]any{"clean": float64(2)}, "findings": map[string]any{"annotated": float64(0)},
		}},
	}
	if !reflect.DeepEqual(j["by_purpose"], want) {
		t.Errorf("by_purpose\n got %#v\nwant %#v", j["by_purpose"], want)
	}
	for _, k := range removedStatsKeys {
		if _, present := j[k]; present {
			t.Errorf("stats still has %s", k)
		}
	}
}

func TestStatsByPurposeIsInNameOrder(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts),
		recLine("o1", ts, `"purpose":"zeta","source":"cli"`),
		recLine("o2", ts, `"purpose":"alpha","source":"cli"`),
		recLine("o3", ts, `"purpose":"alpha","source":"mod"`),
	)

	r := s.run("stats", "--all", "--json")

	raw := r.stdout
	alpha, zeta := strings.Index(raw, `"alpha"`), strings.Index(raw, `"zeta"`)
	if alpha < 0 || zeta < 0 || alpha > zeta {
		t.Fatalf("purposes are not in name order: alpha at %d, zeta at %d", alpha, zeta)
	}
	cli, plugin := strings.Index(raw[alpha:], `"cli"`), strings.Index(raw[alpha:], `"plugin"`)
	if cli < 0 || plugin < 0 || cli > plugin {
		t.Errorf("origins are not cli then plugin: cli at %d, plugin at %d", cli, plugin)
	}
}

func TestStatsOldRecordsByOrigin(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts),
		recLine("ol1", ts, `"scenario":"code-review","source":"hook-stop","outcome":"ok"`),
	)

	j := s.run("stats", "--all", "--json").json(t)

	byPurpose, _ := j["by_purpose"].(map[string]any)
	got, _ := byPurpose["code-review"].(map[string]any)
	if _, ok := got["cli"]; !ok || len(got) != 1 {
		t.Errorf("by_purpose[code-review] = %#v, want a cli bucket only", got)
	}
}

func TestStatsTextShowsThePurposeAndOriginCounts(t *testing.T) {
	s := newSandbox(t)
	ts := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(ts),
		recLine("tx1", ts, `"purpose":"code-review","source":"hook-post-commit","outcome":"ok","attrs":{"review.findings":{"total":3,"high":1}}`),
		recLine("tx2", ts, `"purpose":"code-review","source":"agent","outcome":"error"`),
	)

	r := s.run("stats", "--all")

	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	var cli, plugin string
	for _, line := range r.lines() {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "code-review cli"):
			cli = line
		case strings.HasPrefix(strings.TrimSpace(line), "code-review plugin"):
			plugin = line
		}
	}
	for _, want := range []string{"1 run", "ok=1", "annotated=1", "with_findings=1", "total=3", "high=1"} {
		if !strings.Contains(cli, want) {
			t.Errorf("cli line %q lacks %q\n%s", cli, want, r.stdout)
		}
	}
	for _, want := range []string{"1 run", "error=1"} {
		if !strings.Contains(plugin, want) {
			t.Errorf("plugin line %q lacks %q\n%s", plugin, want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "hook outcomes") {
		t.Errorf("the text still has the hook outcomes line:\n%s", r.stdout)
	}
}

// The engine reads three source labels it writes itself and names no purpose,
// agent type or hook.
func TestNoHookNamesInTheEngine(t *testing.T) {
	banned := regexp.MustCompile(`"hook[^"]*"|"(code-review|second-opinion|cross-check|expert-persona|delegation)"|HasPrefix\([^)]*"hook`)
	for _, root := range []string{"cmd", "internal", "tools"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if m := banned.FindString(string(src)); m != "" {
				t.Errorf("%s names %s", strings.TrimPrefix(path, repoRoot+"/"), m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
