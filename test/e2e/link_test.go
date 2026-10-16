package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const pluginKey = "msgate@cc-msgate"

// linkWorld is one isolated HOME with a Claude plugins root and a launcher dir.
type linkWorld struct {
	t       *testing.T
	home    string
	cfg     string // CLAUDE_CONFIG_DIR
	plugins string // <cfg>/plugins
}

func newLinkWorld(t *testing.T) *linkWorld {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "claude-config")
	w := &linkWorld{t: t, home: home, cfg: cfg, plugins: filepath.Join(cfg, "plugins")}
	if err := os.MkdirAll(w.plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *linkWorld) launcher() string { return filepath.Join(w.home, ".local", "bin", "msgate") }

func (w *linkWorld) env(extra ...string) []string {
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + w.home, "CLAUDE_CONFIG_DIR=" + w.cfg}
	return append(env, extra...)
}

// fakeInstall creates an install dir whose bin/msgate is a stub script
// and returns the install path.
func (w *linkWorld) fakeInstall(name string) string {
	w.t.Helper()
	dir := filepath.Join(w.home, "cache", name)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		w.t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"installed-shim $*\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "msgate"), []byte(script), 0o755); err != nil {
		w.t.Fatal(err)
	}
	return dir
}

type installEntry struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
}

func (w *linkWorld) record(key string, entries ...installEntry) {
	w.t.Helper()
	w.recordAt(w.plugins, key, entries...)
}

func (w *linkWorld) recordAt(pluginsRoot, key string, entries ...installEntry) {
	w.t.Helper()
	if err := os.MkdirAll(pluginsRoot, 0o755); err != nil {
		w.t.Fatal(err)
	}
	doc := map[string]any{"version": 2, "plugins": map[string]any{key: entries}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(pluginsRoot, "installed_plugins.json"), b, 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *linkWorld) link(bin string, args ...string) result {
	w.t.Helper()
	return runBin(w.t, bin, w.env(), append([]string{"link"}, args...)...)
}

func buildSeqLine(t *testing.T, launcher string) string {
	t.Helper()
	for _, l := range strings.Split(readFile(t, launcher), "\n") {
		if strings.HasPrefix(l, "build_seq=") {
			return l
		}
	}
	t.Fatalf("no build_seq= line in %s:\n%s", launcher, readFile(t, launcher))
	return ""
}

func TestLinkFreshWritesExecutableLauncher(t *testing.T) {
	w := newLinkWorld(t)
	bin := buildMsgateVariant(t, "1.0.0", "c0ffee", 7)
	install := w.fakeInstall("v1")
	// The install's shim execs the real binary so `version` goes end to end.
	script := fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", bin)
	if err := os.WriteFile(filepath.Join(install, "bin", "msgate"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: install, Version: "1.0.0"})

	r := w.link(bin)
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	st, err := os.Stat(w.launcher())
	if err != nil {
		t.Fatalf("launcher not written: %v", err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("launcher mode = %v, want 0755", st.Mode().Perm())
	}
	if got := buildSeqLine(t, w.launcher()); got != "build_seq=7" {
		t.Errorf("build_seq line = %q", got)
	}
	if !strings.HasPrefix(readFile(t, w.launcher()), "#!/bin/sh\n") {
		t.Errorf("launcher is not a POSIX sh script:\n%s", readFile(t, w.launcher()))
	}
	entries, _ := os.ReadDir(filepath.Dir(w.launcher()))
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// The launcher runs the install: version matches the binary's own.
	want := runBin(t, bin, w.env(), "version", "--json").stdout
	got := runBin(t, w.launcher(), w.env(), "version", "--json")
	if got.code != 0 || got.stdout != want {
		t.Errorf("launcher version = %q (exit %d), want %q", got.stdout, got.code, want)
	}
}

func TestLinkCreatesMissingLauncherDir(t *testing.T) {
	w := newLinkWorld(t)
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	if exists(filepath.Dir(w.launcher())) {
		t.Fatal("precondition: ~/.local/bin exists")
	}
	r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7))
	if r.code != 0 || !exists(w.launcher()) {
		t.Fatalf("exit %d, launcher exists %v, stderr %s", r.code, exists(w.launcher()), r.stderr)
	}
}

func TestLinkDirFlagOverridesLauncherDir(t *testing.T) {
	w := newLinkWorld(t)
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	dir := filepath.Join(w.home, "elsewhere")
	r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7), "--dir", dir)
	if r.code != 0 || !exists(filepath.Join(dir, "msgate")) || exists(w.launcher()) {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
}

func TestLinkLeavesForeignFileUntouched(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(fmt.Sprintf("quiet=%v", quiet), func(t *testing.T) {
			w := newLinkWorld(t)
			w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
			if err := os.MkdirAll(filepath.Dir(w.launcher()), 0o755); err != nil {
				t.Fatal(err)
			}
			foreign := "#!/usr/bin/python3\nprint('pipx entry point')\n"
			if err := os.WriteFile(w.launcher(), []byte(foreign), 0o755); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
			if err := os.Chtimes(w.launcher(), old, old); err != nil {
				t.Fatal(err)
			}
			args := []string{}
			if quiet {
				args = append(args, "--quiet")
			}
			r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7), args...)
			if r.code != 0 {
				t.Fatalf("exit %d stderr %s", r.code, r.stderr)
			}
			if got := readFile(t, w.launcher()); got != foreign {
				t.Errorf("foreign file changed: %q", got)
			}
			st, _ := os.Stat(w.launcher())
			if !st.ModTime().Equal(old) {
				t.Errorf("foreign file mtime moved to %v", st.ModTime())
			}
			out := r.stdout + r.stderr
			if quiet && out != "" {
				t.Errorf("--quiet printed %q", out)
			}
			if !quiet && !strings.Contains(out, w.launcher()) {
				t.Errorf("no explanation naming %s; got %q", w.launcher(), out)
			}
		})
	}
}

func TestLinkTreatsSymlinkAsForeign(t *testing.T) {
	w := newLinkWorld(t)
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	if err := os.MkdirAll(filepath.Dir(w.launcher()), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(w.home, "real-tool")
	if err := os.WriteFile(dest, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dest, w.launcher()); err != nil {
		t.Fatal(err)
	}
	r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7))
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if fi, err := os.Lstat(w.launcher()); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink was replaced: %v %v", fi, err)
	}
}

func TestLinkOptOut(t *testing.T) {
	w := newLinkWorld(t)
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	r := runBin(t, buildMsgateVariant(t, "1.0.0", "c0ffee", 7), w.env("MSGATE_NO_LINK=1"), "link")
	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if exists(filepath.Dir(w.launcher())) {
		t.Errorf("opt-out still created %s", filepath.Dir(w.launcher()))
	}
}

func TestLinkRerunIsNoOp(t *testing.T) {
	w := newLinkWorld(t)
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: w.fakeInstall("v1")})
	bin := buildMsgateVariant(t, "1.0.0", "c0ffee", 7)
	if r := w.link(bin); r.code != 0 {
		t.Fatalf("first link: exit %d %s", r.code, r.stderr)
	}
	first := readFile(t, w.launcher())
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(w.launcher(), old, old); err != nil {
		t.Fatal(err)
	}
	if r := w.link(bin); r.code != 0 {
		t.Fatalf("second link: exit %d %s", r.code, r.stderr)
	}
	if got := readFile(t, w.launcher()); got != first {
		t.Errorf("content changed on re-run")
	}
	st, _ := os.Stat(w.launcher())
	if !st.ModTime().Equal(old) {
		t.Errorf("mtime changed on re-run: %v", st.ModTime())
	}
}

func TestLinkNeverDowngrades(t *testing.T) {
	w := newLinkWorld(t)
	install := w.fakeInstall("v1")
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: install})
	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 50)); r.code != 0 {
		t.Fatalf("link@50: %d %s", r.code, r.stderr)
	}
	before := readFile(t, w.launcher())

	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 40)); r.code != 0 {
		t.Fatalf("link@40: %d %s", r.code, r.stderr)
	}
	if got := readFile(t, w.launcher()); got != before {
		t.Errorf("link from build_seq 40 changed a build_seq 50 launcher:\n%s", got)
	}

	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 60)); r.code != 0 {
		t.Fatalf("link@60: %d %s", r.code, r.stderr)
	}
	if got := buildSeqLine(t, w.launcher()); got != "build_seq=60" {
		t.Errorf("link from build_seq 60 left %q", got)
	}
}

func TestLinkRewritesStalePath(t *testing.T) {
	w := newLinkWorld(t)
	old := w.fakeInstall("v1")
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: old})
	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 50)); r.code != 0 {
		t.Fatalf("link@50: %d %s", r.code, r.stderr)
	}
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	fresh := w.fakeInstall("v2")
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: fresh})

	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 10)); r.code != 0 {
		t.Fatalf("link@10: %d %s", r.code, r.stderr)
	}
	body := readFile(t, w.launcher())
	if !strings.Contains(body, fresh) || strings.Contains(body, old) {
		t.Errorf("launcher still names the stale path:\n%s", body)
	}
	if got := buildSeqLine(t, w.launcher()); got != "build_seq=10" {
		t.Errorf("rewritten build_seq = %q, want the running build's", got)
	}
}

func TestLinkWithoutInstallRecord(t *testing.T) {
	bin := buildMsgateVariant(t, "1.0.0", "c0ffee", 7)

	t.Run("no file", func(t *testing.T) {
		w := newLinkWorld(t)
		r := w.link(bin)
		if r.code != 4 || r.stderr == "" {
			t.Errorf("exit %d stderr %q, want 4 with a message", r.code, r.stderr)
		}
		if exists(filepath.Dir(w.launcher())) {
			t.Error("launcher dir created without a record")
		}
	})
	t.Run("other plugin only", func(t *testing.T) {
		w := newLinkWorld(t)
		w.record("other@elsewhere", installEntry{Scope: "user", InstallPath: w.fakeInstall("x")})
		if r := w.link(bin); r.code != 4 {
			t.Errorf("exit %d, want 4", r.code)
		}
	})
	t.Run("target flag", func(t *testing.T) {
		w := newLinkWorld(t)
		shim := filepath.Join(w.home, "work", "msgate-shim")
		if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(shim, []byte("#!/bin/sh\necho dev-shim \"$@\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := w.link(bin, "--target", shim)
		if r.code != 0 {
			t.Fatalf("exit %d %s", r.code, r.stderr)
		}
		if body := readFile(t, w.launcher()); !strings.Contains(body, shim) {
			t.Errorf("launcher does not execute %s:\n%s", shim, body)
		}
		if got := buildSeqLine(t, w.launcher()); got != "build_seq=7" {
			t.Errorf("build_seq = %q, want the running binary's", got)
		}
		if out := runBin(t, w.launcher(), w.env(), "a", "b"); out.stdout != "dev-shim a b\n" {
			t.Errorf("launcher output = %q", out.stdout)
		}
	})
	t.Run("target must exist", func(t *testing.T) {
		w := newLinkWorld(t)
		if r := w.link(bin, "--target", filepath.Join(w.home, "missing")); r.code != 2 {
			t.Errorf("exit %d, want 2", r.code)
		}
		if exists(w.launcher()) {
			t.Error("launcher written for a missing target")
		}
	})
}

func TestLinkPrefersUserScope(t *testing.T) {
	w := newLinkWorld(t)
	project := w.fakeInstall("project")
	user := w.fakeInstall("user")
	w.record(pluginKey,
		installEntry{Scope: "project", InstallPath: project},
		installEntry{Scope: "user", InstallPath: user})
	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7)); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	body := readFile(t, w.launcher())
	if !strings.Contains(body, user) || strings.Contains(body, project) {
		t.Errorf("launcher did not pick the user-scope install:\n%s", body)
	}
}

func TestLinkPluginsRootPrecedence(t *testing.T) {
	w := newLinkWorld(t)
	cacheRoot := filepath.Join(w.home, "cache-root")
	viaCache := w.fakeInstall("via-cache")
	viaCfg := w.fakeInstall("via-cfg")
	w.recordAt(cacheRoot, pluginKey, installEntry{Scope: "user", InstallPath: viaCache})
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: viaCfg})
	bin := buildMsgateVariant(t, "1.0.0", "c0ffee", 7)

	r := runBin(t, bin, w.env("CLAUDE_CODE_PLUGIN_CACHE_DIR="+cacheRoot), "link")
	if r.code != 0 || !strings.Contains(readFile(t, w.launcher()), viaCache) {
		t.Fatalf("CLAUDE_CODE_PLUGIN_CACHE_DIR ignored (exit %d):\n%s", r.code, readFile(t, w.launcher()))
	}

	// Without the cache dir, CLAUDE_CONFIG_DIR/plugins wins over ~/.claude/plugins.
	w2 := newLinkWorld(t)
	home := w2.fakeInstall("via-home")
	cfg := w2.fakeInstall("via-cfg")
	w2.recordAt(filepath.Join(w2.home, ".claude", "plugins"), pluginKey, installEntry{Scope: "user", InstallPath: home})
	w2.record(pluginKey, installEntry{Scope: "user", InstallPath: cfg})
	if r := w2.link(bin); r.code != 0 || !strings.Contains(readFile(t, w2.launcher()), cfg) {
		t.Fatalf("CLAUDE_CONFIG_DIR/plugins not preferred (exit %d)", r.code)
	}

	// With neither set, ~/.claude/plugins is used.
	w3 := newLinkWorld(t)
	only := w3.fakeInstall("via-home")
	w3.recordAt(filepath.Join(w3.home, ".claude", "plugins"), pluginKey, installEntry{Scope: "user", InstallPath: only})
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + w3.home}
	if r := runBin(t, bin, env, "link"); r.code != 0 || !strings.Contains(readFile(t, w3.launcher()), only) {
		t.Fatalf("~/.claude/plugins fallback failed (exit %d)", r.code)
	}
}

func TestLauncherAfterUninstallExits127(t *testing.T) {
	w := newLinkWorld(t)
	install := w.fakeInstall("v1")
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: install})
	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7)); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if err := os.RemoveAll(install); err != nil {
		t.Fatal(err)
	}
	r := runBin(t, w.launcher(), w.env(), "version")
	if r.code != 127 {
		t.Errorf("exit %d, want 127", r.code)
	}
	if !strings.Contains(r.stderr, "msgate plugin is not installed") || !strings.Contains(r.stderr, install) {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestLauncherQuotesAwkwardInstallPaths(t *testing.T) {
	w := newLinkWorld(t)
	install := w.fakeInstall("it's a $path `x`")
	w.record(pluginKey, installEntry{Scope: "user", InstallPath: install})
	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7)); r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	r := runBin(t, w.launcher(), w.env(), "x y")
	if r.code != 0 || r.stdout != "installed-shim x y\n" {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	// Re-running must recognise its own quoted launcher and stay a no-op.
	before := readFile(t, w.launcher())
	if r := w.link(buildMsgateVariant(t, "1.0.0", "c0ffee", 7)); r.code != 0 || readFile(t, w.launcher()) != before {
		t.Errorf("re-run changed the launcher (exit %d)", r.code)
	}
}
