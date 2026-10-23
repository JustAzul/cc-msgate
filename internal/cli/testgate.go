package cli

import (
	"os"
	"time"
)

// testGateLimit bounds how long a reader waits at a test gate.
const testGateLimit = 10 * time.Second

// readerGate lets a test act between a reader's first look at a run and the
// rest of its work. When MSGATE_TEST_READ_GATE names a path, the reader
// creates <path>.reached and waits for <path>.release to appear. It is
// inert otherwise.
func readerGate(ctx *Context) { waitAtGate(ctx.Getenv("MSGATE_TEST_READ_GATE")) }

// refreshGate holds a provider refresh, lock taken, until a test lets it go: it
// works like readerGate, on the path MSGATE_TEST_REFRESH_GATE names.
func refreshGate(ctx *Context) { waitAtGate(ctx.Getenv("MSGATE_TEST_REFRESH_GATE")) }

// waitAtGate creates <path>.reached and waits for <path>.release to appear,
// for at most testGateLimit. An empty path does nothing.
func waitAtGate(path string) {
	if path == "" {
		return
	}
	if err := os.WriteFile(path+".reached", nil, 0o600); err != nil {
		return
	}
	for deadline := time.Now().Add(testGateLimit); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(path + ".release"); err == nil {
			return
		}
	}
}

// refreshLog appends one line to the file MSGATE_TEST_REFRESH_LOG names, so a
// test can count the provider refreshes that were started and that ended
// without racing on wall time. It is inert otherwise.
func refreshLog(ctx *Context, line string) {
	path := ctx.Getenv("MSGATE_TEST_REFRESH_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line + "\n") // one short write: appends do not interleave
}
