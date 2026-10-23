package codex_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeBinDir is a directory holding the fake Codex (internal/testutil/fakecodex)
// as an executable named codex; TestMain builds it once.
var fakeBinDir string

func TestMain(m *testing.M) {
	os.Exit(runWithFakeCodex(m))
}

func runWithFakeCodex(m *testing.M) int {
	dir, err := os.MkdirTemp("", "fakecodex-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "codex"),
		"github.com/JustAzul/cc-msgate/internal/testutil/fakecodex")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakecodex: %v\n%s", err, out)
		return 1
	}
	fakeBinDir = dir
	return m.Run()
}
