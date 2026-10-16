package link_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/link"
)

func TestRenderCarriesTheMsgateMarkerAndMissingInstallMessage(t *testing.T) {
	script, err := link.Render("/opt/msgate/bin/msgate", 7)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	lines := strings.Split(text, "\n")
	if !strings.HasPrefix(lines[1], "# msgate-launcher") {
		t.Errorf("marker line = %q, want prefix %q", lines[1], "# msgate-launcher")
	}
	if !strings.Contains(text, "msgate plugin is not installed") {
		t.Errorf("launcher lacks the missing-install message:\n%s", text)
	}
	if strings.Contains(strings.ToLower(text), "agentcli") {
		t.Errorf("launcher still names agentcli:\n%s", text)
	}
}

func TestParseRecognisesOnlyTheMsgateMarker(t *testing.T) {
	script, err := link.Render("/opt/msgate/bin/msgate", 7)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := link.Parse(script)
	if !ok || got.BuildSeq != 7 || got.Target != "/opt/msgate/bin/msgate" {
		t.Errorf("Parse(msgate launcher) = %+v, %v", got, ok)
	}
	old := "#!/bin/sh\n# agentcli-launcher: written by \"agentcli link\"; do not edit.\nbuild_seq=7\ntarget='/x'\n"
	if _, ok := link.Parse([]byte(old)); ok {
		t.Error("an agentcli launcher must not be recognised as ours")
	}
}

func TestLauncherPathIsMsgate(t *testing.T) {
	if got := link.LauncherPath("/d"); got != "/d/msgate" {
		t.Errorf("LauncherPath = %q, want /d/msgate", got)
	}
}

func TestEnsureUsesMsgateLockAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := link.Options{Dir: filepath.Join(dir, "bin"), Target: target, BuildSeq: 7}
	if act, err := link.Ensure(opts); err != nil || act != link.Written {
		t.Fatalf("first Ensure = %v, %v", act, err)
	}
	if act, err := link.Ensure(opts); err != nil || act != link.Unchanged {
		t.Fatalf("second Ensure = %v, %v; want Unchanged", act, err)
	}
	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != ".msgate.lock,msgate" {
		t.Errorf("bin dir holds %v, want [.msgate.lock msgate]", names)
	}
}

func TestPluginKeyIsMsgateAtCcMsgate(t *testing.T) {
	if link.PluginKey != "msgate@cc-msgate" {
		t.Errorf("PluginKey = %q", link.PluginKey)
	}
}
