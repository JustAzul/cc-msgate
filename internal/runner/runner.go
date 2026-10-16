// Package runner turns a provider plan into a finished run: spawn, stream
// events, classify the outcome and write the terminal state.
package runner

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

const stderrTailBytes = 64 * 1024

// Job is everything the runner needs; the caller has already admitted the run.
type Job struct {
	Store    *store.Store
	Provider provider.Provider
	Plan     provider.Plan
	Prompt   []byte
	Env      []string    // caller environment, inherited by the provider
	State    store.State // the admitted (queued) state
	// CleanSentinel and MaterialLabel drive outcome classification.
	CleanSentinel string
	MaterialLabel string
	Warn          func(string)
	Now           func() time.Time

	// Timeout ends the provider's group when it expires; zero means none.
	Timeout time.Duration
	// Shutdown bounds how a terminated group is given; the zero value means
	// DefaultShutdown.
	Shutdown Shutdown

	// Record carries the telemetry fields known before the run starts; the
	// runner fills in the rest and appends it when the run is terminal.
	Record telemetry.Record

	startedAt time.Time
	version   *string
}

// Result describes a finished run.
type Result struct {
	State        store.State
	ProviderExit *int // nil when the provider never ran to an exit
	ExitCode     int  // the process exit code msgate should use
	SDKStatus    string
	Usage        *provider.Usage
}

// SDK status values reported alongside a result.
const (
	SDKStatusOK              = "ok"
	SDKStatusProviderMissing = "provider_missing"
	SDKStatusTimeout         = "timeout"
	SDKStatusCancelled       = "cancelled"
	SDKStatusInternalError   = "internal_error"
)

// exitInternal is the exit code of a run that failed inside msgate.
const exitInternal = 70

// Run executes a foreground run to a terminal state. It returns an error only
// for internal failures that prevent the run from starting.
//
// The run holds the run lock from here until it returns, and drives the state
// file through running to a terminal state. The provider runs in a process
// group of its own; the timeout, SIGINT and SIGTERM all end that whole group.
func Run(job Job) (Result, error) {
	j := &job
	if j.Warn == nil {
		j.Warn = func(string) {}
	}
	var warnMu sync.Mutex
	warn := j.Warn
	j.Warn = func(msg string) { // the stream reader and the supervisor may both warn
		warnMu.Lock()
		defer warnMu.Unlock()
		warn(msg)
	}
	if j.Now == nil {
		j.Now = time.Now
	}
	if j.Shutdown.Grace <= 0 {
		j.Shutdown.Grace = DefaultShutdown.Grace
	}
	if j.Shutdown.Drain <= 0 {
		j.Shutdown.Drain = DefaultShutdown.Drain
	}

	st := j.State
	lock, err := j.Store.LockRun(st.RunID)
	if err != nil {
		return Result{}, fmt.Errorf("taking the run lock: %w", err)
	}
	defer lock.Close()
	if settled, done, err := j.alreadySettled(st.RunID); done || err != nil {
		return settled, err
	}

	// Termination requests that arrive before the provider exists wait here.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	j.startedAt = j.Now()
	started := j.startedAt.UTC().Format(time.RFC3339)
	st.State = "running"
	st.StartedAt = &started
	st.WorkerPID = os.Getpid()
	if err := j.Store.WriteState(st.RunID, st); err != nil {
		return Result{}, fmt.Errorf("writing the running state: %w", err)
	}

	bin := j.Plan.Argv[0]
	path, err := lookPath(bin, j.Env)
	if err != nil {
		excerpt := fmt.Sprintf("provider binary %q not found on PATH", bin)
		return j.finish(st, outcome{state: "failed", exitCode: 127, label: "error", excerpt: excerpt, sdkStatus: SDKStatusProviderMissing}), nil
	}

	cmd := exec.Command(path, j.Plan.Argv[1:]...)
	cmd.Dir = j.Plan.Dir
	cmd.Env = buildEnv(j.Env, j.Plan.EnvAdd, j.Plan.EnvRemove)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the provider leads a group of its own
	env := cmd.Env
	switch j.Plan.Stdin {
	case provider.StdinPrompt:
		cmd.Stdin = bytes.NewReader(j.Prompt)
	default:
		cmd.Stdin = bytes.NewReader(nil)
	}
	// A descendant that outlives the provider and keeps the prompt pipe open
	// must not hold Wait; the provider's output pipes are read below.
	cmd.WaitDelay = j.Shutdown.Drain

	out, err := newPipes()
	if err != nil {
		return Result{}, fmt.Errorf("opening the provider pipes: %w", err)
	}
	defer out.closeReaders()
	cmd.Stdout, cmd.Stderr = out.stdoutW, out.stderrW
	if err := cmd.Start(); err != nil {
		out.closeWriters()
		excerpt := fmt.Sprintf("starting provider %q: %v", bin, err)
		return j.finish(st, outcome{state: "failed", exitCode: 127, label: "error", excerpt: excerpt, sdkStatus: SDKStatusProviderMissing}), nil
	}
	out.closeWriters() // only the provider and its descendants hold the write ends now
	pgid := cmd.Process.Pid
	j.recordProviderGroup(&st, pgid)

	// The version probe runs beside the provider so it adds no wall time.
	versionCh := make(chan *string, 1)
	go func() { versionCh <- providerVersion(path, j.Provider.VersionArgs(), j.Plan.Dir, env) }()

	tail := newTailBuffer(stderrTailBytes)
	streamCh := make(chan streamResult, 1)
	streamDone := make(chan struct{})
	go func() { streamCh <- j.consume(out.stdoutR, st); close(streamDone) }()
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
	j.version = <-versionCh

	if terr := j.Store.WriteStderrTail(st.RunID, tail.Bytes()); terr != nil {
		j.Warn(fmt.Sprintf("could not write stderr.tail: %v", terr))
	}
	st.UnparsedEvents = stream.unparsed
	return j.finish(st, j.outcomeOf(end, stream, tail.Bytes())), nil
}

// ErrCancelPending is returned by Run when a cancel request is waiting for a
// run that has not started: the provider is not spawned and the state is left
// as it was, for whoever wrote the request to settle.
var ErrCancelPending = errors.New("a cancel request is waiting for the run")

// alreadySettled is the check a run makes once it holds the lock, against the
// state as it is now rather than as admitted: another process may have settled
// the run (cancelled it, or found it lost) while it waited. A run settled that
// way is returned as recorded and left untouched; a queued run with a cancel
// request is not started.
func (j *Job) alreadySettled(runID string) (Result, bool, error) {
	cur, err := j.Store.ReadState(runID)
	switch {
	case err != nil:
		return Result{}, false, fmt.Errorf("reading the run's state: %w", err)
	case store.IsTerminal(cur.State):
		return resultOf(cur), true, nil
	case cur.State != "queued":
		return Result{}, false, fmt.Errorf("the run is %s, not queued", cur.State)
	case j.Store.CancelRequested(runID):
		return Result{}, false, ErrCancelPending
	}
	return Result{}, false, nil
}

// resultOf describes a run that was already terminal as recorded.
func resultOf(st store.State) Result {
	res := Result{State: st, ProviderExit: st.ProviderExit, ExitCode: exitInternal}
	if st.ExitCode != nil {
		res.ExitCode = *st.ExitCode
	}
	if st.SDKStatus != nil {
		res.SDKStatus = *st.SDKStatus
	}
	return res
}

// Abort finalizes a run that was admitted but whose worker never started it:
// the state turns failed (exit 70, outcome error) with the excerpt, the
// conversation's marker is cleared and the telemetry record is appended, in
// the order a finished run uses. The record's timestamp is the admission.
func Abort(job Job, excerpt string) Result {
	return settleUnfinished(job, outcome{state: "failed", exitCode: exitInternal, label: "error", excerpt: excerpt, sdkStatus: SDKStatusInternalError})
}

// outcomeOf turns how the provider ended into the run's outcome.
func (j *Job) outcomeOf(end ended, stream streamResult, stderr []byte) outcome {
	o := outcome{sdkStatus: SDKStatusOK, usage: stream.usage, sessionID: stream.sessionID}
	exit, err := exitCode(end.waitErr)
	if end.reaped && err == nil {
		o.providerExit = &exit
	}
	switch end.reason {
	case endTimeout:
		o.state, o.label, o.exitCode, o.sdkStatus = "timeout", "timeout", 124, SDKStatusTimeout
		o.excerpt = fmt.Sprintf("the provider timed out after %s", j.Timeout)
	case endSignal:
		code := 128 + int(end.signal)
		if j.Store.CancelRequested(j.State.RunID) {
			code = 130
		}
		o.state, o.label, o.exitCode, o.sdkStatus = "cancelled", "cancelled", code, SDKStatusCancelled
		o.excerpt = "cancelled by " + signalName(end.signal)
	default:
		o.exitCode = exit
		o.eventError, o.stderrLine = stream.lastError, lastNonEmptyLine(stderr)
	}
	return o
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return s.String()
}

// pipes are the provider's stdout and stderr, owned by the runner so that a
// descendant holding a write end can never block the reader past the drain.
type pipes struct {
	stdoutR, stdoutW, stderrR, stderrW *os.File
}

func newPipes() (*pipes, error) {
	p := &pipes{}
	var err error
	if p.stdoutR, p.stdoutW, err = os.Pipe(); err != nil {
		return nil, err
	}
	if p.stderrR, p.stderrW, err = os.Pipe(); err != nil {
		p.closeReaders()
		p.closeWriters()
		return nil, err
	}
	return p, nil
}

func (p *pipes) closeWriters() {
	if p.stdoutW != nil {
		p.stdoutW.Close()
	}
	if p.stderrW != nil {
		p.stderrW.Close()
	}
}

func (p *pipes) closeReaders() {
	if p.stdoutR != nil {
		p.stdoutR.Close()
	}
	if p.stderrR != nil {
		p.stderrR.Close()
	}
}

// drained waits until done or the deadline. Past the deadline it closes the
// pipe's read end, which ends the reader, and reports false when the reader
// still has not stopped shortly after.
func drained(done <-chan struct{}, r io.Closer, deadline time.Time) bool {
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
	}
	r.Close()
	select {
	case <-done:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// endReason says what ended the provider.
type endReason int

const (
	endExited  endReason = iota // the provider finished on its own
	endTimeout                  // the timeout expired first
	endSignal                   // msgate was asked to stop first
)

// ended is how the provider's wait finished.
type ended struct {
	waitErr error
	reaped  bool // false: the group leader never reported after SIGKILL
	reason  endReason
	signal  syscall.Signal
}

// await blocks until the provider exits, the timeout expires or SIGINT or
// SIGTERM arrives, and in the last two cases ends the provider's group. A
// provider that has already exited wins any race with the timeout or a
// signal, so its own result is kept.
func (j *Job) await(pgid int, waitCh <-chan error, sigs <-chan os.Signal) ended {
	var timeoutC <-chan time.Time
	if j.Timeout > 0 {
		t := time.NewTimer(j.Timeout)
		defer t.Stop()
		timeoutC = t.C
	}
	var end ended
	select {
	case err := <-waitCh:
		return ended{waitErr: err, reaped: true}
	case <-timeoutC:
		end.reason = endTimeout
	case sig := <-sigs:
		end.reason = endSignal
		end.signal, _ = sig.(syscall.Signal)
	}
	select {
	case err := <-waitCh:
		return ended{waitErr: err, reaped: true}
	default:
	}
	reason, sig := end.reason, end.signal
	end = j.terminate(pgid, waitCh)
	end.reason, end.signal = reason, sig
	return end
}

// terminate sends SIGTERM to the provider's group and, if the group leader is
// still there when the grace period ends, SIGKILL. The grace period also ends
// once the leader has exited, at which point SIGKILL sweeps what it left
// behind.
func (j *Job) terminate(pgid int, waitCh <-chan error) ended {
	syscall.Kill(-pgid, syscall.SIGTERM)
	grace := time.NewTimer(j.Shutdown.Grace)
	defer grace.Stop()
	select {
	case err := <-waitCh:
		syscall.Kill(-pgid, syscall.SIGKILL)
		return ended{waitErr: err, reaped: true}
	case <-grace.C:
	}
	syscall.Kill(-pgid, syscall.SIGKILL)
	reap := time.NewTimer(j.Shutdown.Drain)
	defer reap.Stop()
	select {
	case err := <-waitCh:
		return ended{waitErr: err, reaped: true}
	case <-reap.C:
		return ended{}
	}
}

// recordProviderGroup persists the provider's process group and start time so
// other processes can find, and verify, the group. A failure only warns.
func (j *Job) recordProviderGroup(st *store.State, pid int) {
	st.ProviderPGID = pid // the provider leads its own group, so the group id is its pid
	if start, err := StartTime(pid); err != nil {
		j.Warn(fmt.Sprintf("could not read the provider's start time: %v", err))
	} else {
		st.ProviderStartTime = &start
	}
	if err := j.Store.WriteState(st.RunID, *st); err != nil {
		j.Warn(fmt.Sprintf("could not record the provider process group: %v", err))
	}
}

// appendTelemetry writes the run's record, the last step of finish. A
// failure only warns.
func (j *Job) appendTelemetry(st store.State, label, excerpt string, o outcome) {
	rec := j.Record
	rec.V, rec.Kind = telemetry.Version, "run"
	rec.RunID, rec.ConversationID, rec.Turn = st.RunID, st.ConversationID, st.Turn
	rec.TS = j.startedAt.UTC().Format(time.RFC3339)
	rec.ProviderVersion = j.version
	rec.ExitCode, rec.Outcome = o.exitCode, label
	rec.DurationMS = j.Now().Sub(j.startedAt).Milliseconds()
	rec.OutputFile = st.OutputPath
	if info, err := os.Stat(st.OutputPath); err == nil {
		rec.OutputBytes = info.Size()
	}
	rec.ErrorExcerpt = nullable(excerpt)
	rec.Usage = provider.NormalizeUsage(rec.Command, o.usage)
	rec.ProviderSessionID = nullable(o.sessionID)
	rec.ModelUsed, rec.EffortUsed = nullable(o.modelUsed), nullable(o.effortUsed)
	if err := telemetry.Append(j.Store.Home, rec, j.Now(), telemetry.Options{}); err != nil {
		j.Warn(fmt.Sprintf("could not append the telemetry record: %v", err))
	}
}

// usedModel asks the provider which model and effort the run actually used,
// when it can tell. It must run while the run still owns its conversation: a
// next turn of the same provider session records its own model after it.
func (j *Job) usedModel(sessionID string) (model, effort string) {
	reporter, ok := j.Provider.(provider.ModelReporter)
	if !ok || sessionID == "" {
		return "", ""
	}
	model, effort, err := reporter.UsedModel(j.Env, sessionID)
	if err != nil {
		j.Warn(fmt.Sprintf("could not read the model the provider used: %v", err))
	}
	return model, effort
}

func (j *Job) stamp() string { return j.Now().UTC().Format(time.RFC3339) }

type streamResult struct {
	unparsed  int
	lastError string
	usage     *provider.Usage
	sessionID string
}

// consume parses provider stdout line by line as it arrives, persists the
// provider session id as soon as it is seen and appends what the provider
// does to the run's progress file.
func (j *Job) consume(r io.Reader, st store.State) streamResult {
	var res streamResult
	progress := j.openProgress(st.RunID)
	if progress != nil {
		defer progress.Close()
	}
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			ev, ok := j.Provider.ParseEvent(trimmed)
			switch {
			case !ok:
				res.unparsed++
			default:
				if ev.SessionID != "" && ev.SessionID != res.sessionID {
					res.sessionID = ev.SessionID
					j.persistSession(st.ConversationID, res.sessionID)
				}
				if ev.Usage != nil {
					res.usage = ev.Usage
				}
				if ev.ErrorMsg != "" {
					res.lastError = ev.ErrorMsg
				}
				if ev.Progress != nil && progress != nil {
					j.appendProgress(progress, ev.Progress)
				}
			}
		}
		if err != nil {
			return res
		}
	}
}

// openProgress opens the run's progress file, or returns nil when it cannot:
// a run with no progress to show still runs.
func (j *Job) openProgress(runID string) *store.ProgressWriter {
	w, err := j.Store.OpenProgress(runID)
	if err != nil {
		j.Warn(fmt.Sprintf("could not open the progress file: %v", err))
		return nil
	}
	return w
}

func (j *Job) appendProgress(w *store.ProgressWriter, p *provider.Progress) {
	if err := w.Append(j.Now(), p.Kind, progressLine(p.Text)); err != nil {
		j.Warn(fmt.Sprintf("could not append to the progress file: %v", err))
	}
}

// progressTextBytes bounds one progress entry: it is a line to follow the run
// by, and the output file holds the whole text.
const progressTextBytes = 500

// progressLine folds text onto one line and cuts it at progressTextBytes,
// never inside a character.
func progressLine(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if len(line) <= progressTextBytes {
		return line
	}
	cut := progressTextBytes
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + "…"
}

func (j *Job) persistSession(conversationID, sessionID string) {
	err := j.Store.UpdateConversation(conversationID, func(c *store.Conversation) {
		c.ProviderSessionID = &sessionID
		c.UpdatedAt = j.stamp()
	})
	if err != nil {
		j.Warn(fmt.Sprintf("could not persist the provider session id: %v", err))
	}
}

type outcome struct {
	state        string // forced state; empty means derive from the exit code
	exitCode     int
	providerExit *int
	label        string // forced outcome; empty means classify the output
	excerpt      string // forced error excerpt
	sdkStatus    string
	usage        *provider.Usage
	eventError   string
	stderrLine   string
	sessionID    string
	modelUsed    string // what the provider reports it ran with; "" when unknown
	effortUsed   string
}

// finish classifies the run and finalizes it in this order: output final,
// state terminal, conversation marker cleared (only if it still names this
// run), telemetry appended.
func (j *Job) finish(st store.State, o outcome) Result {
	_ = os.Chmod(st.OutputPath, 0o600) // the provider wrote it; keep it user-only

	label := o.label
	if label == "" {
		label = classify(o.exitCode, st.OutputPath, j.CleanSentinel, j.MaterialLabel)
	}
	excerpt := o.excerpt
	if excerpt == "" {
		excerpt = errorExcerpt(o.eventError, o.exitCode, o.stderrLine)
	}
	state := o.state
	if state == "" {
		state = "done"
		if o.exitCode != 0 {
			state = "failed"
		}
	}

	o.modelUsed, o.effortUsed = j.usedModel(o.sessionID)
	ended := j.stamp()
	exit := o.exitCode
	st.State = state
	st.EndedAt = &ended
	st.ExitCode = &exit
	st.Outcome = &label
	st.ProviderExit = o.providerExit
	sdk := o.sdkStatus
	st.SDKStatus = &sdk
	st.ErrorExcerpt = nullable(excerpt)
	if err := j.Store.WriteState(st.RunID, st); err != nil {
		j.Warn(fmt.Sprintf("could not write the terminal state: %v", err))
	}
	err := j.Store.UpdateConversation(st.ConversationID, func(c *store.Conversation) {
		if c.ActiveRunID == nil || *c.ActiveRunID != st.RunID {
			return // another run owns the conversation now
		}
		c.ActiveRunID = nil
		c.UpdatedAt = ended
	})
	if err != nil {
		j.Warn(fmt.Sprintf("could not clear the conversation's active run: %v", err))
	}
	j.appendTelemetry(st, label, excerpt, o)
	return Result{State: st, ProviderExit: o.providerExit, ExitCode: exit, SDKStatus: o.sdkStatus, Usage: o.usage}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// classify assigns the outcome of a run that ended on its own.
func classify(exit int, outputPath, sentinel, materialLabel string) string {
	if exit != 0 {
		return "error"
	}
	data, err := os.ReadFile(outputPath)
	if err != nil || len(data) == 0 {
		return "empty"
	}
	if sentinel != "" && normalize(string(data)) == normalize(sentinel) {
		return "clean"
	}
	return materialLabel
}

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// exitCode maps a Wait error to a process exit code: the provider's own exit,
// or 128+n when a signal ended it.
func exitCode(waitErr error) (int, error) {
	if waitErr == nil || errors.Is(waitErr, exec.ErrWaitDelay) {
		return 0, nil // ErrWaitDelay: the provider succeeded but a descendant held a pipe
	}
	var ee *exec.ExitError
	if !errors.As(waitErr, &ee) {
		return 0, waitErr
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	return ee.ExitCode(), nil
}

func lastNonEmptyLine(b []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}
