package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// symlinkedGitRepo returns a path that reaches a git work tree through a
// symlink, the way macOS reaches t.TempDir() through /var -> /private/var.
func symlinkedGitRepo(t *testing.T) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(gitRepo(t), link); err != nil {
		t.Fatal(err)
	}
	return link
}

// samePath reports whether two paths name the same location once symlinks are
// resolved. A provider process reports its working directory resolved, while
// msgate records the path as the caller passed it.
func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatalf("resolving %q: %v", a, err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatalf("resolving %q: %v", b, err)
	}
	return ra == rb
}
