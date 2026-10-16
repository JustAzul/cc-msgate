package link

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const (
	marker      = "# msgate-launcher"
	maxLauncher = 64 << 10
)

var (
	buildSeqLine = regexp.MustCompile(`^build_seq=([0-9]+)$`)
	targetLine   = regexp.MustCompile(`^target='(.*)'$`)
)

// Render returns the launcher script: it records buildSeq and executes target,
// or explains that the plugin is gone and exits 127.
func Render(target string, buildSeq int) ([]byte, error) {
	if strings.ContainsAny(target, "\n\r") {
		return nil, errors.New("launcher target contains a line break")
	}
	quoted := strings.ReplaceAll(target, "'", `'\''`)
	var b bytes.Buffer
	b.WriteString("#!/bin/sh\n")
	b.WriteString(marker + `: written by "msgate link"; do not edit.` + "\n")
	fmt.Fprintf(&b, "build_seq=%d\n", buildSeq)
	fmt.Fprintf(&b, "target='%s'\n", quoted)
	b.WriteString("if [ ! -e \"$target\" ]; then\n")
	b.WriteString("  echo \"msgate plugin is not installed (launcher target missing: $target)\" >&2\n")
	b.WriteString("  exit 127\n")
	b.WriteString("fi\n")
	b.WriteString("exec \"$target\" \"$@\"\n")
	return b.Bytes(), nil
}

// Parsed describes an existing msgate launcher.
type Parsed struct {
	BuildSeq int
	Target   string
}

// Parse reports whether content is a msgate launcher and what it records.
func Parse(content []byte) (Parsed, bool) {
	var p Parsed
	isLauncher := false
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, marker):
			isLauncher = true
		case buildSeqLine.MatchString(line):
			p.BuildSeq, _ = strconv.Atoi(buildSeqLine.FindStringSubmatch(line)[1])
		case targetLine.MatchString(line):
			p.Target = strings.ReplaceAll(targetLine.FindStringSubmatch(line)[1], `'\''`, "'")
		}
	}
	return p, isLauncher
}

// Action is what Ensure did.
type Action int

const (
	Written   Action = iota // launcher created or replaced
	Unchanged               // launcher already current
	Foreign                 // path holds something that is not a msgate launcher
)

// Options configure Ensure.
type Options struct {
	Dir      string // directory holding the launcher
	Target   string // absolute path the launcher executes
	BuildSeq int    // build_seq of the code asking for the link
}

// LauncherPath is where the launcher lives inside dir.
func LauncherPath(dir string) string { return filepath.Join(dir, "msgate") }

// Ensure makes the launcher in opts.Dir point at opts.Target, under an
// exclusive lock, writing only when the launcher is absent, older, or names
// a target that no longer exists. It never touches a file that is not an
// msgate launcher.
func Ensure(opts Options) (Action, error) {
	script, err := Render(opts.Target, opts.BuildSeq)
	if err != nil {
		return Foreign, err
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return Foreign, err
	}
	lock, err := os.OpenFile(filepath.Join(opts.Dir, ".msgate.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Foreign, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Foreign, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	path := LauncherPath(opts.Dir)
	switch fi, err := os.Lstat(path); {
	case errors.Is(err, fs.ErrNotExist):
		// absent: write
	case err != nil:
		return Foreign, err
	case !fi.Mode().IsRegular() || fi.Size() > maxLauncher:
		return Foreign, nil
	default:
		content, err := os.ReadFile(path)
		if err != nil {
			return Foreign, err
		}
		cur, ok := Parse(content)
		if !ok {
			return Foreign, nil
		}
		if _, statErr := os.Stat(cur.Target); statErr == nil && cur.BuildSeq >= opts.BuildSeq {
			return Unchanged, nil
		}
	}
	return Written, writeAtomic(path, script)
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".msgate.tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(name) }
	if err := tmp.Chmod(0o755); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
