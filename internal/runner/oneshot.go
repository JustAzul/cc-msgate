package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// OneShot is one provider process run for its answer alone: it has no run
// directory, lock, state file or conversation, and leaves nothing behind but
// what the provider itself writes.
type OneShot struct {
	Provider provider.Provider
	Plan     provider.Plan
	Prompt   []byte
	Env      []string      // caller environment, inherited by the provider
	Timeout  time.Duration // ends the provider's group when it expires; zero means none
	Shutdown Shutdown      // the zero value means DefaultShutdown
}

// OneShotResult describes a finished one-shot call.
type OneShotResult struct {
	SDKStatus string // SDKStatusOK, SDKStatusProviderMissing, SDKStatusTimeout or SDKStatusCancelled
	// ExitCode is the provider's own exit, 127 when it never started, 124 after
	// a timeout and 128 plus the signal when msgate was asked to stop.
	ExitCode int
	Usage    *provider.Usage
	// Error is the error excerpt of a call that did not end well, as a run
	// records it; empty otherwise.
	Error string
}

// RunOneShot runs the call to its end. SIGINT, SIGTERM and the timeout end the
// provider's whole process group. It returns an error only for failures that
// keep the provider from starting.
func RunOneShot(call OneShot) (OneShotResult, error) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM) // before the provider exists, so no request is lost
	defer signal.Stop(sigs)

	bin := call.Plan.Argv[0]
	path, err := lookPath(bin, call.Env)
	if err != nil {
		return notStarted(fmt.Sprintf("provider binary %q not found on PATH", bin)), nil
	}
	out, err := newPipes()
	if err != nil {
		return OneShotResult{}, fmt.Errorf("opening the provider pipes: %w", err)
	}
	defer out.closeReaders()
	cmd := oneShotCommand(call, path, out)
	if err := cmd.Start(); err != nil {
		out.closeWriters()
		return notStarted(fmt.Sprintf("starting provider %q: %v", bin, err)), nil
	}
	out.closeWriters() // only the provider and its descendants hold the write ends now
	return awaitOneShot(call, cmd, out, sigs), nil
}

// notStarted is the result of a provider that could not be started.
func notStarted(excerpt string) OneShotResult {
	return OneShotResult{SDKStatus: SDKStatusProviderMissing, ExitCode: 127, Error: excerpt}
}

// oneShotCommand is the provider process for call, leading a process group of
// its own and writing to out.
func oneShotCommand(call OneShot, path string, out *pipes) *exec.Cmd {
	cmd := exec.Command(path, call.Plan.Argv[1:]...)
	cmd.Dir = call.Plan.Dir
	cmd.Env = buildEnv(call.Env, call.Plan.EnvAdd, call.Plan.EnvRemove)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = bytes.NewReader(nil)
	if call.Plan.Stdin == provider.StdinPrompt {
		cmd.Stdin = bytes.NewReader(call.Prompt)
	}
	cmd.WaitDelay = shutdownOf(call).Drain // a descendant holding the prompt pipe must not hold Wait
	cmd.Stdout, cmd.Stderr = out.stdoutW, out.stderrW
	return cmd
}

// shutdownOf is the call's shutdown bounds, DefaultShutdown where unset.
func shutdownOf(call OneShot) Shutdown {
	sd := call.Shutdown
	if sd.Grace <= 0 {
		sd.Grace = DefaultShutdown.Grace
	}
	if sd.Drain <= 0 {
		sd.Drain = DefaultShutdown.Drain
	}
	return sd
}

// awaitOneShot reads the started provider's output until it ends and turns how
// it ended into the result.
func awaitOneShot(call OneShot, cmd *exec.Cmd, out *pipes, sigs <-chan os.Signal) OneShotResult {
	pgid := cmd.Process.Pid
	j := &Job{Timeout: call.Timeout, Shutdown: shutdownOf(call)}
	j.watchLeader(pgid, nil)

	tail := newTailBuffer(stderrTailBytes)
	streamCh := make(chan streamResult, 1)
	streamDone := make(chan struct{})
	go func() {
		streamCh <- readStream(out.stdoutR, call.Provider, func(provider.Event, bool) {})
		close(streamDone)
	}()
	tailDone := make(chan struct{})
	go func() { io.Copy(tail, out.stderrR); close(tailDone) }()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	end := j.await(pgid, waitCh, sigs)

	deadline := time.Now().Add(j.Shutdown.Drain)
	var stream streamResult
	if drained(streamDone, out.stdoutR, deadline) {
		stream = <-streamCh
	}
	drained(tailDone, out.stderrR, deadline)
	return oneShotResult(call.Timeout, end, stream, tail.Bytes())
}

// oneShotResult turns how the provider ended into the call's result.
func oneShotResult(timeout time.Duration, end ended, stream streamResult, stderr []byte) OneShotResult {
	res := OneShotResult{SDKStatus: SDKStatusOK, Usage: stream.usage}
	switch end.reason {
	case endTimeout:
		res.SDKStatus, res.ExitCode = SDKStatusTimeout, 124
		res.Error = fmt.Sprintf("the provider timed out after %s", timeout)
	case endSignal:
		res.SDKStatus, res.ExitCode = SDKStatusCancelled, 128+int(end.signal)
		res.Error = "cancelled by " + signalName(end.signal)
	default:
		res.ExitCode, _ = exitCode(end.waitErr)
		res.Error = errorExcerpt(stream.lastError, res.ExitCode, lastNonEmptyLine(stderr))
	}
	return res
}
