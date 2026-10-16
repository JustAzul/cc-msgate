package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const versionPkg = "github.com/JustAzul/cc-msgate/internal/version"

var (
	variantMu   sync.Mutex
	variantBins = map[string]string{}
	variantDir  string
)

// buildMsgateVariant builds msgate once per distinct ldflags set and
// returns the binary path. The default harness binary carries no metadata.
func buildMsgateVariant(t *testing.T, version, commit string, buildSeq int) string {
	t.Helper()
	key := strings.Join([]string{version, commit, strconv.Itoa(buildSeq)}, "|")
	variantMu.Lock()
	defer variantMu.Unlock()
	if p, ok := variantBins[key]; ok {
		return p
	}
	if variantDir == "" {
		d, err := os.MkdirTemp("", "msgate-variants-*")
		if err != nil {
			t.Fatal(err)
		}
		variantDir = d
	}
	out := filepath.Join(variantDir, "msgate-"+strconv.Itoa(len(variantBins)))
	ld := "-X " + versionPkg + ".Version=" + version +
		" -X " + versionPkg + ".SourceCommit=" + commit +
		" -X " + versionPkg + ".BuildSeq=" + strconv.Itoa(buildSeq)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ld, "-o", out, "./cmd/msgate")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build variant: %v\n%s", err, o)
	}
	variantBins[key] = out
	return out
}

func cleanupVariants() {
	if variantDir != "" {
		os.RemoveAll(variantDir)
	}
}

// runBin runs an arbitrary binary with an explicit, non-inherited environment.
func runBin(t *testing.T, bin string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
