package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homeRun runs `exec` with an explicit child environment (no MSGATE_HOME unless
// the caller adds it) and returns the result.
func homeRun(t *testing.T, env ...string) result {
	t.Helper()
	base := []string{"PATH=" + fakeDir + string(os.PathListSeparator) + "/usr/bin:/bin", "MSGATE_PRICES_URL=off", "FAKECODEX_FIXTURE=" + fixture("exec-ok")}
	return runBin(t, msgateBin, append(base, env...), "exec", "--cwd", gitRepo(t), "--run-id", "h1", "q")
}

func TestHomeUnderXDGStateHome(t *testing.T) {
	xdg := t.TempDir()
	r := homeRun(t, "HOME="+t.TempDir(), "XDG_STATE_HOME="+xdg)
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if !exists(filepath.Join(xdg, "msgate", "runs", "h1")) {
		t.Errorf("run directory not under %s/msgate/runs/", xdg)
	}
}

func TestHomeDefaultsUnderLocalState(t *testing.T) {
	home := t.TempDir()
	r := homeRun(t, "HOME="+home)
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if !exists(filepath.Join(home, ".local", "state", "msgate", "runs", "h1")) {
		t.Errorf("run directory not under %s/.local/state/msgate/runs/", home)
	}
}

func TestMsgateHomeWinsOverXDGStateHome(t *testing.T) {
	home, xdg := filepath.Join(t.TempDir(), "h"), t.TempDir()
	r := homeRun(t, "HOME="+t.TempDir(), "XDG_STATE_HOME="+xdg, "MSGATE_HOME="+home)
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if !exists(filepath.Join(home, "runs", "h1")) || exists(filepath.Join(xdg, "msgate")) {
		t.Errorf("run directory must be under MSGATE_HOME only")
	}
}

func TestOldVariablesAreIgnored(t *testing.T) {
	old := filepath.Join(t.TempDir(), "old")
	xdg := t.TempDir()
	r := homeRun(t, "HOME="+t.TempDir(), "XDG_STATE_HOME="+xdg, "AGENTCLI_HOME="+old, "AGENTCLI_NO_LINK=1", "AGENTCLI_PRICES_URL=off")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if exists(old) {
		t.Errorf("AGENTCLI_HOME was honoured: %s exists", old)
	}
	if !exists(filepath.Join(xdg, "msgate", "runs", "h1")) {
		t.Errorf("home was not resolved without AGENTCLI_HOME")
	}
	st := runBin(t, msgateBin, []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "AGENTCLI_HOME=" + old}, "status")
	if st.code == 0 && exists(old) {
		t.Errorf("status honoured AGENTCLI_HOME")
	}
}

func TestOldLinkOptOutIsIgnored(t *testing.T) {
	w := newLinkWorld(t)
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	r := runBin(t, buildMsgateVariant(t, "1.0.0", "c0ffee", 7), w.env("AGENTCLI_NO_LINK=1"), "link")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if !strings.Contains(readFile(t, w.launcher()), "# msgate-launcher") {
		t.Errorf("AGENTCLI_NO_LINK must not disable link; launcher = %q", readFile(t, w.launcher()))
	}
}

func TestOldInstallKeyIsNotResolved(t *testing.T) {
	w := newLinkWorld(t)
	w.record("agentcli@agentcli", installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	r := runBin(t, buildMsgateVariant(t, "1.0.0", "c0ffee", 7), w.env(), "link")
	if r.code == 0 || exists(w.launcher()) {
		t.Errorf("an agentcli@agentcli record must not satisfy link: code %d, launcher exists %v", r.code, exists(w.launcher()))
	}
}
