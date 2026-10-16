package store

import (
	"os"
	"path/filepath"
	"syscall"
)

const (
	// legacyHomeName is the directory the home had before it was renamed. The
	// migration reads it as data; nothing else names it.
	legacyHomeName = "agentcli"

	migrateLockName = ".msgate-migrate.lock"
)

// HomeMigration is the one-time move of an old home to the home that replaced
// it, both children of one directory.
type HomeMigration struct{ Old, New string }

// PendingHomeMigration reports the migration a command must make before it
// uses the home, if any: MSGATE_HOME is unset, the resolved home does not
// exist, and the old home of the same tier does.
func PendingHomeMigration(getenv func(string) string) (HomeMigration, bool) {
	if getenv("MSGATE_HOME") != "" {
		return HomeMigration{}, false
	}
	home, err := ResolveHome(getenv)
	if err != nil {
		return HomeMigration{}, false
	}
	m := HomeMigration{Old: filepath.Join(filepath.Dir(home), legacyHomeName), New: home}
	return m, m.Pending()
}

// Pending reports whether the new home is absent and the old one is a
// directory. Any other state of the new path counts as present: a home that
// cannot be inspected is never replaced.
func (m HomeMigration) Pending() bool {
	if _, err := os.Lstat(m.New); !os.IsNotExist(err) {
		return false
	}
	info, err := os.Stat(m.Old)
	return err == nil && info.IsDir()
}

func (m HomeMigration) lockPath() string { return filepath.Join(filepath.Dir(m.New), migrateLockName) }

// Lock takes the migration lock in the parent directory, waiting until it is
// free, and returns the function releasing it. The lock file stays on disk:
// removing it would let two processes lock different files. The descriptor
// is close-on-exec.
func (m HomeMigration) Lock() (func(), error) {
	f, err := os.OpenFile(m.lockPath(), os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil // closing the descriptor drops the lock
}

// OldStore is the old home, opened.
func (m HomeMigration) OldStore() *Store { return Open(m.Old) }

// Move renames the old home to the new path: one rename, no link left behind.
func (m HomeMigration) Move() error { return os.Rename(m.Old, m.New) }
