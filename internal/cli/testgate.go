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
func readerGate(ctx *Context) {
	path := ctx.Getenv("MSGATE_TEST_READ_GATE")
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
