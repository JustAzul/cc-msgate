package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/version"
)

const (
	// appServerTimeout bounds the whole app-server exchange.
	appServerTimeout = 10 * time.Second
	// maxAppServerLine bounds one line of the app-server's answer.
	maxAppServerLine = 1 << 20
	// fullPercent is the usage at which a rate-limit window is spent.
	fullPercent = 100
)

// Why an app-server exchange gave no answer; the text goes into Health.Skipped,
// so it stays short and holds nothing the server said.
var (
	errCouldNotStart = errors.New("could not start")
	errNoReply       = errors.New("closed without a reply")
	errUnreadable    = errors.New("unreadable reply")
	errErrorReply    = errors.New("error reply")
)

// rateLimitLayer asks the app-server for the account's rate limits.
func rateLimitLayer(ctx context.Context, bin string, env []string) verdict {
	snap, err := readRateLimits(ctx, bin, env)
	if err != nil {
		return skip(err.Error())
	}
	return snap.verdict()
}

// rateLimitWindow is one usage window of the account.
type rateLimitWindow struct {
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    *int64  `json:"resetsAt"` // Unix seconds
}

// rateLimitSnapshot is the "rateLimits" of an account/rateLimits/read answer.
type rateLimitSnapshot struct {
	Primary              *rateLimitWindow `json:"primary"`
	Secondary            *rateLimitWindow `json:"secondary"`
	RateLimitReachedType *string          `json:"rateLimitReachedType"`
}

// verdict is failed, with the reset time, when the account has reached a limit:
// the server says so, or a window is used up. The reset is the latest among the
// used-up windows (all must reset before a call goes through), else among all.
func (s rateLimitSnapshot) verdict() verdict {
	var windows, spent []*rateLimitWindow
	for _, w := range []*rateLimitWindow{s.Primary, s.Secondary} {
		if w == nil {
			continue
		}
		windows = append(windows, w)
		if w.UsedPercent >= fullPercent {
			spent = append(spent, w)
		}
	}
	reached := s.RateLimitReachedType != nil && *s.RateLimitReachedType != ""
	if len(spent) == 0 && !reached {
		return verdict{outcome: passed}
	}
	if len(spent) == 0 {
		spent = windows
	}
	return verdict{outcome: failed, reason: provider.ReasonRateLimited, resetsAt: latestReset(spent)}
}

// latestReset is the latest reset time among the windows, nil when none has one.
func latestReset(windows []*rateLimitWindow) *time.Time {
	var latest *int64
	for _, w := range windows {
		if w.ResetsAt != nil && (latest == nil || *w.ResetsAt > *latest) {
			latest = w.ResetsAt
		}
	}
	if latest == nil {
		return nil
	}
	at := time.Unix(*latest, 0).UTC()
	return &at
}

// leadOwnGroup makes cmd lead a process group of its own and makes the end of
// its context kill the whole group. Codex can be a launcher script that starts
// the real binary as a child: killing the launcher alone leaves the child
// holding the pipes.
func leadOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// readRateLimits starts `codex app-server` on stdio, performs the handshake and
// reads the account's rate limits. The process group is killed, and the read
// end of its output closed, when the context ends or the answer is in.
func readRateLimits(ctx context.Context, bin string, env []string) (rateLimitSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, appServerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Env, cmd.WaitDelay = env, waitDelay
	leadOwnGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return rateLimitSnapshot{}, errCouldNotStart
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return rateLimitSnapshot{}, errCouldNotStart
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return rateLimitSnapshot{}, errTimedOut
		}
		return rateLimitSnapshot{}, errCouldNotStart
	}
	// A reader blocked on the pipe returns once the context ends, whoever else
	// still holds the write end.
	defer context.AfterFunc(ctx, func() { _ = stdout.Close() })()
	snap, err := rateLimitExchange(stdin, stdout)
	timedOut := ctx.Err() != nil
	cancel()
	_ = cmd.Wait()
	if err != nil && timedOut {
		return rateLimitSnapshot{}, errTimedOut
	}
	return snap, err
}

// rateLimitExchange speaks the app-server protocol, one JSON object per line:
// initialize, the initialized notification, then account/rateLimits/read.
func rateLimitExchange(w io.Writer, r io.Reader) (rateLimitSnapshot, error) {
	lines := bufio.NewScanner(r)
	lines.Buffer(nil, maxAppServerLine)
	clientInfo := map[string]any{"clientInfo": map[string]any{"name": "msgate", "version": version.Version}}
	if _, err := request(w, lines, 1, "initialize", clientInfo); err != nil {
		return rateLimitSnapshot{}, err
	}
	if err := send(w, map[string]any{"method": "initialized"}); err != nil {
		return rateLimitSnapshot{}, err
	}
	result, err := request(w, lines, 2, "account/rateLimits/read", nil)
	if err != nil {
		return rateLimitSnapshot{}, err
	}
	var answer struct {
		RateLimits *rateLimitSnapshot `json:"rateLimits"`
	}
	if err := json.Unmarshal(result, &answer); err != nil || answer.RateLimits == nil {
		return rateLimitSnapshot{}, errUnreadable
	}
	return *answer.RateLimits, nil
}

// request sends the request id with method and params (omitted when nil) and
// returns the result of the reply with that id. Notifications and the replies
// to other ids are skipped.
func request(w io.Writer, lines *bufio.Scanner, id int, method string, params any) (json.RawMessage, error) {
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := send(w, msg); err != nil {
		return nil, err
	}
	for lines.Scan() {
		var reply struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(lines.Bytes(), &reply); err != nil {
			return nil, errUnreadable
		}
		if reply.ID == nil || *reply.ID != id {
			continue
		}
		if reply.Error != nil {
			return nil, errErrorReply
		}
		return reply.Result, nil
	}
	if lines.Err() != nil {
		return nil, errUnreadable
	}
	return nil, errNoReply
}

// send writes msg as one line.
func send(w io.Writer, msg map[string]any) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err = w.Write(append(line, '\n')); err != nil {
		return errNoReply
	}
	return nil
}
