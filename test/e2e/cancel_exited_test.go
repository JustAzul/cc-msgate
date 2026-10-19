package e2e

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

// A worker that has not been scheduled since before the provider exited gets the
// cancel signal and the provider's exit at the same moment. Whichever it looks
// at first, the run keeps the provider's result. The worker is frozen for the
// stretch between the provider's exit and the cancel, and the race is repeated
// because the outcome of the old behavior depended on which event the worker
// picked up first.
func TestCancelOfAFrozenWorkerKeepsTheResultOfAProviderThatExited(t *testing.T) {
	const rounds = 20
	s := shutdownEnv(newSandbox(t), 500*time.Millisecond, 3*time.Second).
		set("FAKECODEX_OUTPUT", "answer").set("FAKECODEX_SLEEP_MS", "300")
	rec := s.recordTo()

	for i := 0; i < rounds; i++ {
		runID := fmt.Sprintf("fz%d", i)
		os.Remove(rec)
		admitJob(t, s, runID, "q")
		st := waitForState(t, s, runID, "a running state with the provider group", hasProviderGroup)
		// The provider records its call once it has read the prompt the worker
		// feeds it; a worker frozen before that would leave the provider waiting.
		untilTrue(t, "the provider to have read its prompt", func() bool { return exists(rec) })
		pgid, worker := int(st["provider_pgid"].(float64)), int(st["worker_pid"].(float64))

		if err := syscall.Kill(worker, syscall.SIGSTOP); err != nil {
			t.Fatalf("round %d: stopping the worker: %v", i, err)
		}
		t.Cleanup(func() { syscall.Kill(worker, syscall.SIGCONT) })
		untilTrue(t, "the provider to exit", func() bool { return len(liveInGroup(t, pgid)) == 0 })

		c := s.run("cancel", runID)
		if err := syscall.Kill(worker, syscall.SIGCONT); err != nil {
			t.Fatalf("round %d: resuming the worker: %v", i, err)
		}
		if c.code != 0 {
			t.Errorf("round %d: cancel exit %d; stderr %s", i, c.code, c.stderr)
			continue
		}
		w := s.run("wait", "--json", "--timeout", "15", runID)
		if w.code != 0 {
			t.Errorf("round %d: wait exit %d, want the provider's 0; stdout %s", i, w.code, w.stdout)
			continue
		}
		checkFields(t, fmt.Sprintf("round %d wait json", i), w.json(t),
			map[string]any{"state": "done", "sdk_status": "ok", "provider_exit": float64(0), "outcome": "ok"})
	}
}
