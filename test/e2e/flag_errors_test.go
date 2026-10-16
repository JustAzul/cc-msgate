package e2e

import (
	"strings"
	"testing"
)

// lastStderrLine is the final line of the standard error text.
func (r result) lastStderrLine() string {
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	return lines[len(lines)-1]
}

// assertFlagError checks the stderr of a refused flag parse: the command's
// usage first, then one last line that carries the error behind the msgate
// prefix, and exit 2.
func assertFlagError(t *testing.T, r result, usage, errorPrefix string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	last := r.lastStderrLine()
	if r.code != 2 {
		t.Errorf("exit %d, want 2 (stderr %q)", r.code, r.stderr)
	}
	if !strings.HasPrefix(last, errorPrefix) {
		t.Errorf("last stderr line = %q, want it to start with %q (stderr %q)", last, errorPrefix, r.stderr)
	}
	if !strings.HasPrefix(r.stderr, usage) {
		t.Errorf("stderr %q does not open with the usage %q", r.stderr, usage)
	}
	prefixed := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "msgate: ") {
			prefixed++
		}
	}
	if prefixed != 1 {
		t.Errorf("stderr has %d msgate-prefixed lines, want exactly 1 (stderr %q)", prefixed, r.stderr)
	}
}

func TestUnknownFlagOnExec(t *testing.T) {
	s := newSandbox(t)

	r := s.run("exec", "--scenario", "x", "-")

	assertFlagError(t, r, "usage: msgate exec ", "msgate: flag provided but not defined: -scenario")
}

func TestBadFlagValueOnExec(t *testing.T) {
	s := newSandbox(t)

	r := s.run("exec", "--timeout", "abc", "-")

	assertFlagError(t, r, "usage: msgate exec ", `msgate: invalid value "abc" for flag -timeout: `)
}

func TestBadSettingsSourceIsAFlagError(t *testing.T) {
	s := newSandbox(t)

	r := s.run("exec", "--settings-source", "plugin", "-")

	assertFlagError(t, r, "usage: msgate exec ", `msgate: invalid value "plugin" for flag -settings-source: `)
}

func TestUnknownFlagOnEveryCommandThatParsesFlags(t *testing.T) {
	cases := []struct {
		args  []string
		usage string
	}{
		{[]string{"exec"}, "usage: msgate exec "},
		{[]string{"review"}, "usage: msgate review "},
		{[]string{"send"}, "usage: msgate send "},
		{[]string{"eta"}, "usage: msgate eta "},
		{[]string{"status"}, "usage: msgate status "},
		{[]string{"wait"}, "usage: msgate wait "},
		{[]string{"cancel"}, "usage: msgate cancel "},
		{[]string{"progress"}, "usage: msgate progress "},
		{[]string{"annotate"}, "usage: msgate annotate "},
		{[]string{"conversations"}, "usage: msgate conversations "},
		{[]string{"runs"}, "usage: msgate runs "},
		{[]string{"stats"}, "usage: msgate stats "},
		{[]string{"prune"}, "usage: msgate prune "},
		{[]string{"prices"}, "usage: msgate prices "},
		{[]string{"prices", "refresh"}, "usage: msgate prices refresh "},
		{[]string{"usage", "add"}, "usage: msgate usage add "},
		{[]string{"version"}, "usage: msgate version "},
		{[]string{"link"}, "usage: msgate link "},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			s := newSandbox(t)

			r := s.run(append(append([]string{}, c.args...), "--bogus")...)

			assertFlagError(t, r, c.usage, "msgate: flag provided but not defined: -bogus")
		})
	}
}

func TestFlagErrorKeepsTheJSONErrorObject(t *testing.T) {
	s := newSandbox(t)

	r := s.run("exec", "--json", "--scenario", "x", "-")

	j := r.json(t)
	if r.code != 2 || j["sdk_status"] != "usage_error" || j["exit_code"] != float64(2) {
		t.Errorf("exit %d error object %v", r.code, j)
	}
	if !strings.HasPrefix(r.stderr, "usage: msgate exec ") || !strings.HasSuffix(r.stderr, "flag provided but not defined: -scenario\n") {
		t.Errorf("stderr %q", r.stderr)
	}
}

func TestHelpStillPrintsTheUsageAlone(t *testing.T) {
	for _, args := range [][]string{{"exec", "--help"}, {"status", "--help"}, {"version", "--help"}, {"link", "--help"}} {
		s := newSandbox(t)

		r := s.run(args...)

		if r.code != 0 || !strings.HasPrefix(r.stderr, "usage: msgate "+args[0]) || strings.Contains(r.stderr, "msgate: ") {
			t.Errorf("%v: exit %d stderr %q, want exit 0 and the usage line alone", args, r.code, r.stderr)
		}
	}
}
