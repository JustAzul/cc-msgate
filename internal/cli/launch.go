package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
)

const (
	// defaultAdmissionWait is how long an admission waits for its worker to
	// reach running before it gives up on it.
	defaultAdmissionWait = 10 * time.Second
	admissionPoll        = 10 * time.Millisecond
)

// admissionWait is defaultAdmissionWait unless the test-only override
// MSGATE_TEST_ADMISSION_WAIT_MS is set.
func admissionWait(getenv func(string) string) time.Duration {
	if d, ok := envMillis(getenv, "MSGATE_TEST_ADMISSION_WAIT_MS"); ok {
		return d
	}
	return defaultAdmissionWait
}

// worker is the detached process executing a job.
type worker struct {
	cmd  *exec.Cmd
	done chan struct{} // closed once the process has been reaped
}

func startWorker(ctx *Context, st *store.Store, runID string) (*worker, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, workerCommand, runID)
	cmd.Env = ctx.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // its own session: the caller's terminal and group never reach it
	// Stdin and stdout stay nil, which connects them to the null device. The
	// worker's stderr is its log in the run directory.
	if log, err := st.OpenWorkerLog(runID); err != nil {
		ctx.Warnf("could not open the worker log of run %s: %v", runID, err)
	} else {
		defer log.Close()
		cmd.Stderr = log
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	w := &worker{cmd: cmd, done: make(chan struct{})}
	go func() { cmd.Wait(); close(w.done) }()
	return w, nil
}

// kill ends the worker and waits until it has been reaped.
func (w *worker) kill() {
	w.cmd.Process.Kill()
	<-w.done
}

// launchJob hands an admitted run to a detached worker and waits for the worker
// to take the run. It reports the admission, or fails the run when the worker
// does not get going.
func launchJob(ctx *Context, job runner.Job) int {
	runID := job.State.RunID
	w, err := startWorker(ctx, job.Store, runID)
	if err != nil {
		return abortJob(ctx, job, nil, fmt.Sprintf("starting the worker: %v", err))
	}
	wait := admissionWait(ctx.Getenv)
	state, reason := awaitRunning(job.Store, runID, wait, w.done)
	if reason != "" {
		return abortJob(ctx, job, w, reason)
	}
	return reportAdmission(ctx, state)
}

// awaitRunning polls the run's state until the worker has moved it past
// queued. A worker that finishes between two polls has still reached running,
// so any later state counts. It returns a reason when the worker exits or the
// wait passes first.
func awaitRunning(s *store.Store, runID string, wait time.Duration, workerDone <-chan struct{}) (store.State, string) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(admissionPoll)
	defer tick.Stop()
	exited := false
	for {
		state, err := s.ReadState(runID)
		if err == nil && state.State != "queued" {
			return state, ""
		}
		if exited {
			return state, "the worker exited before reaching running"
		}
		select {
		case <-workerDone:
			exited = true // read the state once more before judging
			workerDone = nil
		case <-deadline.C:
			return state, fmt.Sprintf("the worker did not reach running within %s", wait)
		case <-tick.C:
		}
	}
}

// abortJob ends an admission whose worker never reached running. Holding the
// run lock first guarantees the worker cannot start the provider while it is
// being ended; the run then turns failed, or cancelled when a cancel request
// is what stopped the worker.
func abortJob(ctx *Context, job runner.Job, w *worker, reason string) int {
	st := job.Store
	lock, err := st.LockRun(job.State.RunID)
	switch {
	case err == nil:
		defer lock.Close()
	case errors.Is(err, store.ErrRunLockHeld):
		// The worker took the run just after the wait ended.
		if cur, rerr := st.ReadState(job.State.RunID); rerr == nil && cur.State != "queued" {
			return reportAdmission(ctx, cur)
		}
	}
	if w != nil {
		job.State.WorkerPID = w.cmd.Process.Pid
		w.kill()
	}
	if st.CancelRequested(job.State.RunID) {
		return reportAdmission(ctx, runner.CancelQueued(job).State)
	}
	runner.Abort(job, reason)
	return ctx.Fail(ExitInternal, "job %s failed to start: %s", job.State.RunID, reason)
}

// reportAdmission prints the admitted job: its run id, or with --json the
// admission object.
func reportAdmission(ctx *Context, s store.State) int {
	if !ctx.JSON {
		fmt.Fprintln(ctx.Stdout, s.RunID)
		return ExitOK
	}
	return printJSON(ctx, struct {
		ConversationID string `json:"conversation_id"`
		RunID          string `json:"run_id"`
		State          string `json:"state"`
		SDKStatus      string `json:"sdk_status"`
		ExitCode       int    `json:"exit_code"`
		RunDir         string `json:"run_dir"`
		OutputPath     string `json:"output_path"`
	}{s.ConversationID, s.RunID, s.State, "ok", ExitOK, s.RunDir, s.OutputPath})
}
