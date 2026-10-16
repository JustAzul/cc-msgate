package e2e

import (
	"reflect"
	"runtime"
	"testing"
)

func TestVersionJSONReportsBakedMetadata(t *testing.T) {
	bin := buildMsgateVariant(t, "9.8.7", "abc123def456", 4242)
	r := runBin(t, bin, []string{"PATH=/usr/bin:/bin"}, "version", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	m := r.json(t)
	want := map[string]any{
		"version":       "9.8.7",
		"source_commit": "abc123def456",
		"build_seq":     float64(4242),
		"platform":      runtime.GOOS + "/" + runtime.GOARCH,
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("version --json = %v, want %v", m, want)
	}
}

func TestVersionDefaultBuildHasDevMetadata(t *testing.T) {
	r := newSandbox(t).run("version", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	m := r.json(t)
	if m["version"] != "dev" || m["source_commit"] != "unknown" || m["build_seq"] != float64(0) {
		t.Errorf("default metadata = %v", m)
	}
}

func TestVersionPlainIsOneLine(t *testing.T) {
	bin := buildMsgateVariant(t, "9.8.7", "abc123def456", 4242)
	r := runBin(t, bin, []string{"PATH=/usr/bin:/bin"}, "version")
	if r.code != 0 || len(r.lines()) != 1 {
		t.Fatalf("exit %d stdout %q", r.code, r.stdout)
	}
	for _, want := range []string{"9.8.7", "abc123def456", "4242", runtime.GOOS + "/" + runtime.GOARCH} {
		if !contains(r.stdout, want) {
			t.Errorf("plain version %q lacks %q", r.stdout, want)
		}
	}
}

func TestVersionRejectsUnknownFlag(t *testing.T) {
	r := newSandbox(t).run("version", "--nope")
	if r.code != 2 {
		t.Errorf("exit %d, want 2", r.code)
	}
}
