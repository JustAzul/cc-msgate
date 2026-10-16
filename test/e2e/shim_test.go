package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var supportedPlatforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}

// pluginTree lays out a plugin root with the repository's shim and the given
// libexec contents. bins maps a libexec file name to its script or content.
func pluginTree(t *testing.T, bins map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "libexec"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim, err := os.ReadFile(filepath.Join(repoRoot, "plugin", "bin", "msgate"))
	if err != nil {
		t.Fatalf("shim missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "msgate"), shim, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range bins {
		if err := os.WriteFile(filepath.Join(root, "libexec", name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func stubBins() map[string]string {
	bins := map[string]string{}
	for _, p := range supportedPlatforms {
		bins["msgate-"+p] = "#!/bin/sh\necho \"picked " + p + "\"\nfor a in \"$@\"; do echo \"arg=$a\"; done\n"
	}
	return bins
}

// unameStub returns a directory whose uname answers -s and -m with fixed values.
func unameStub(t *testing.T, sysname, machine string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n  -s) echo %s ;;\n  -m) echo %s ;;\n  *) echo %s ;;\nesac\n", sysname, machine, sysname)
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runShim(t *testing.T, shim, stubDir string, args ...string) result {
	t.Helper()
	path := "/usr/bin:/bin"
	if stubDir != "" {
		path = stubDir + ":" + path
	}
	return runBin(t, shim, []string{"PATH=" + path, "HOME=" + t.TempDir()}, args...)
}

func TestShimRunsBinaryForThisPlatform(t *testing.T) {
	name := "msgate-" + runtime.GOOS + "-" + runtime.GOARCH
	real, err := os.ReadFile(msgateBin)
	if err != nil {
		t.Fatal(err)
	}
	root := pluginTree(t, map[string]string{name: string(real)})
	r := runShim(t, filepath.Join(root, "bin", "msgate"), "", "version", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %s", r.code, r.stderr)
	}
	if got := r.json(t)["platform"]; got != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("platform = %v, want %s/%s", got, runtime.GOOS, runtime.GOARCH)
	}
}

func TestShimPlatformAliases(t *testing.T) {
	cases := []struct{ sys, machine, want string }{
		{"Linux", "x86_64", "linux-amd64"},
		{"Linux", "amd64", "linux-amd64"},
		{"Linux", "aarch64", "linux-arm64"},
		{"Linux", "arm64", "linux-arm64"},
		{"Darwin", "x86_64", "darwin-amd64"},
		{"Darwin", "amd64", "darwin-amd64"},
		{"Darwin", "arm64", "darwin-arm64"},
		{"Darwin", "aarch64", "darwin-arm64"},
	}
	root := pluginTree(t, stubBins())
	shim := filepath.Join(root, "bin", "msgate")
	for _, c := range cases {
		t.Run(c.sys+"/"+c.machine, func(t *testing.T) {
			r := runShim(t, shim, unameStub(t, c.sys, c.machine), "two words", "--flag")
			if r.code != 0 {
				t.Fatalf("exit %d stderr %s", r.code, r.stderr)
			}
			want := "picked " + c.want + "\narg=two words\narg=--flag\n"
			if r.stdout != want {
				t.Errorf("stdout = %q, want %q", r.stdout, want)
			}
		})
	}
}

func TestShimUnsupportedPlatform(t *testing.T) {
	root := pluginTree(t, stubBins())
	shim := filepath.Join(root, "bin", "msgate")
	for _, c := range []struct{ sys, machine string }{
		{"FreeBSD", "amd64"},
		{"Linux", "riscv64"},
		{"Darwin", "ppc"},
	} {
		t.Run(c.sys+"/"+c.machine, func(t *testing.T) {
			r := runShim(t, shim, unameStub(t, c.sys, c.machine), "version")
			if r.code != 70 {
				t.Errorf("exit %d, want 70", r.code)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
			for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
				if !strings.Contains(r.stderr, p) {
					t.Errorf("stderr %q does not list %s", r.stderr, p)
				}
			}
		})
	}
}

func TestShimMissingBinaryExits70(t *testing.T) {
	bins := stubBins()
	delete(bins, "msgate-linux-amd64")
	root := pluginTree(t, bins)
	r := runShim(t, filepath.Join(root, "bin", "msgate"), unameStub(t, "Linux", "x86_64"), "version")
	if r.code != 70 || !strings.Contains(r.stderr, "msgate-linux-amd64") {
		t.Errorf("exit %d stderr %q", r.code, r.stderr)
	}
}

func TestShimPassesThroughExitCode(t *testing.T) {
	bins := stubBins()
	bins["msgate-linux-amd64"] = "#!/bin/sh\nexit 3\n"
	root := pluginTree(t, bins)
	r := runShim(t, filepath.Join(root, "bin", "msgate"), unameStub(t, "Linux", "x86_64"))
	if r.code != 3 {
		t.Errorf("exit %d, want 3", r.code)
	}
}

func TestShimResolvesItselfThroughSymlinks(t *testing.T) {
	root := pluginTree(t, stubBins())
	real := filepath.Join(root, "bin", "msgate")
	stub := unameStub(t, "Linux", "aarch64")

	linkDir := t.TempDir()
	first := filepath.Join(linkDir, "first")
	if err := os.Symlink(real, first); err != nil { // absolute link
		t.Fatal(err)
	}
	second := filepath.Join(linkDir, "msgate")
	if err := os.Symlink("first", second); err != nil { // relative link to a link
		t.Fatal(err)
	}
	// A symlinked directory in the path must resolve too.
	dirLink := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, dirLink); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{first, second, filepath.Join(dirLink, "bin", "msgate")} {
		r := runShim(t, p, stub)
		if r.code != 0 || !strings.HasPrefix(r.stdout, "picked linux-arm64\n") {
			t.Errorf("via %s: exit %d stdout %q stderr %q", p, r.code, r.stdout, r.stderr)
		}
	}
}

func TestShimViaPathLookup(t *testing.T) {
	root := pluginTree(t, stubBins())
	stub := unameStub(t, "Linux", "x86_64")
	linkDir := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "bin", "msgate"), filepath.Join(linkDir, "msgate")); err != nil {
		t.Fatal(err)
	}
	// Invoked as a bare name found through PATH, as the SessionStart hook's users do.
	r := runBin(t, "/bin/sh", []string{"PATH=" + linkDir + ":" + stub + ":/usr/bin:/bin"}, "-c", "msgate version")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "picked linux-amd64\n") {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
}
