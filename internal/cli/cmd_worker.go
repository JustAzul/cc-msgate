package cli

import (
	"errors"
	"os"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/runner"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// workerCommand is the internal entry point a job's detached worker runs.
const workerCommand = "_worker"

func init() {
	register(Command{Name: workerCommand, Hidden: true, Run: runWorker})
}

// runWorker executes an admitted background run: it rebuilds the job from the
// run directory and hands it to the same runner a foreground run uses. The
// run's outcome lives in its state file; the worker's own exit is 0 unless it
// could not run the job at all.
func runWorker(ctx *Context, args []string) int {
	if len(args) != 1 || store.ValidateRunID(args[0]) != nil {
		return ctx.Fail(ExitUsage, "usage: msgate %s <run_id>", workerCommand)
	}
	runID := args[0]
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	if log, err := st.OpenWorkerLog(runID); err == nil {
		defer log.Close()
		ctx.Stderr = store.NewTailWriter(log, store.WorkerLogLimit) // what the worker says stays within the log's bound
	}
	state, err := st.ReadState(runID)
	if errors.Is(err, os.ErrNotExist) {
		return ctx.Fail(ExitNotFound, "no run %q", runID)
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "reading the run's state: %v", err)
	}
	var req requestRecord
	if err := st.ReadRequest(runID, &req); err != nil {
		return ctx.Fail(ExitInternal, "reading the run's request: %v", err)
	}
	prompt, err := st.ReadPrompt(runID)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading the run's prompt: %v", err)
	}
	prov, ok := provider.Get(req.Provider)
	if !ok {
		return ctx.Fail(ExitInternal, "unknown provider %q", req.Provider)
	}

	if d, ok := envMillis(ctx.Getenv, "MSGATE_TEST_WORKER_STALL_MS"); ok {
		time.Sleep(d) // lets a test hold the run between queued and running
	}
	// The runner re-reads the state once it holds the run lock: a run settled
	// while this worker waited is left as it is.
	_, err = runner.Run(buildJob(ctx, st, prov, req, state, prompt))
	switch {
	case errors.Is(err, runner.ErrCancelPending):
		return ExitOK // whoever asked for the cancel settles the run
	case err != nil:
		return ctx.Fail(ExitInternal, "%v", err)
	}
	return ExitOK
}
