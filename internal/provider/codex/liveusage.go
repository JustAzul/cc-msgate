package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// liveReadChunk is how much of the session file one backward read takes.
const liveReadChunk = 64 * 1024

// LiveUsage reads what a run has used so far from the session's file, from its
// end. Codex counts tokens cumulatively over the whole thread, so the usage is
// the newest token_count's totals minus those of the newest token_count stamped
// before since (nothing is subtracted when there is none), each count floored
// at 0. The model is the newest turn_context's. ok is false when no
// token_count is stamped at or after since, or the session has no file. A line
// that is not whole JSON (one being written) is skipped, and the scan stops as
// soon as the baseline and the model are both known.
func (adapter) LiveUsage(env []string, sessionID string, since time.Time) (u provider.Usage, model string, ok bool, err error) {
	scan := &liveScan{since: since}
	found, err := scanRollout(env, sessionID, scan)
	if err != nil || !found || !scan.haveCurrent {
		return provider.Usage{}, "", false, err
	}
	return scan.current.minus(scan.baseline), scan.model, true, nil
}

// ThreadUsageBefore is the session's running total as of its newest
// token_count stamped before the given time; ok is false when there is none or
// the session has no file. The scan stops at the first such event.
func (adapter) ThreadUsageBefore(env []string, sessionID string, before time.Time) (total provider.Usage, ok bool, err error) {
	scan := &totalBeforeScan{before: before}
	if _, err := scanRollout(env, sessionID, scan); err != nil || !scan.have {
		return provider.Usage{}, false, err
	}
	return scan.total.usage(), true, nil
}

// rolloutScan reads a session file's lines newest first and says when it has
// what it came for.
type rolloutScan interface {
	take(line []byte)
	done() bool
}

// scanRollout feeds the session's lines, newest first, to scan until it is
// done or the file is read. found is false when the session has no file.
func scanRollout(env []string, sessionID string, scan rolloutScan) (found bool, err error) {
	path, err := sessionFile(env, sessionID)
	if err != nil || path == "" {
		return false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	lines := newReverseLines(f, info.Size(), liveReadChunk)
	for !scan.done() {
		line, readErr := lines.next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return false, fmt.Errorf("reading %s: %w", path, readErr)
		}
		scan.take(line)
	}
	return true, nil
}

// totalBeforeScan looks for the newest token_count stamped before a time.
type totalBeforeScan struct {
	before time.Time
	total  tokenTotals
	have   bool
}

func (s *totalBeforeScan) done() bool { return s.have }

func (s *totalBeforeScan) take(line []byte) {
	if !bytes.Contains(line, tokenCountMarker) {
		return
	}
	if at, totals, ok := tokenCount(line); ok && at.Before(s.before) {
		s.total, s.have = totals, true
	}
}

// liveScan collects, newest line first, the three things LiveUsage needs.
type liveScan struct {
	since       time.Time
	current     tokenTotals // the newest token_count
	haveCurrent bool
	baseline    tokenTotals // the newest token_count stamped before since
	haveBase    bool
	model       string
	// noRun is set when the newest token_count is older than the run: nothing
	// of the run exists, so nothing more is worth reading.
	noRun bool
}

func (s *liveScan) done() bool {
	return s.noRun || (s.haveCurrent && s.haveBase && s.model != "")
}

var (
	tokenCountMarker  = []byte(`"token_count"`)
	turnContextMarker = []byte(`"turn_context"`)
)

func (s *liveScan) take(line []byte) {
	switch {
	case bytes.Contains(line, tokenCountMarker):
		s.takeTokenCount(line)
	case bytes.Contains(line, turnContextMarker):
		if s.model == "" {
			if m, _, ok := turnContext(line); ok {
				s.model = m
			}
		}
	}
}

func (s *liveScan) takeTokenCount(line []byte) {
	at, totals, ok := tokenCount(line)
	if !ok {
		return
	}
	switch {
	case at.Before(s.since):
		if !s.haveCurrent {
			s.noRun = true
		} else if !s.haveBase {
			s.baseline, s.haveBase = totals, true
		}
	case !s.haveCurrent:
		s.current, s.haveCurrent = totals, true
	}
}

// tokenTotals is a token_count event's total_token_usage.
type tokenTotals struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
}

// usage is the totals as a provider.Usage.
func (t tokenTotals) usage() provider.Usage {
	return provider.Usage{
		InputTokens:           t.Input,
		CachedInputTokens:     t.Cached,
		CacheWriteInputTokens: t.CacheWrite,
		OutputTokens:          t.Output,
		ReasoningOutputTokens: t.Reasoning,
	}
}

// minus is t - base, each count floored at 0.
func (t tokenTotals) minus(base tokenTotals) provider.Usage {
	return t.usage().Minus(base.usage())
}

// tokenCount decodes a token_count line; ok is false for any other line, one
// without usage (info is null on some) or one without a readable timestamp.
func tokenCount(line []byte) (at time.Time, totals tokenTotals, ok bool) {
	var item struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type string `json:"type"`
			Info *struct {
				Total *tokenTotals `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &item) != nil || item.Type != "event_msg" || item.Payload.Type != "token_count" ||
		item.Payload.Info == nil || item.Payload.Info.Total == nil {
		return time.Time{}, tokenTotals{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, item.Timestamp)
	if err != nil {
		return time.Time{}, tokenTotals{}, false
	}
	return at, *item.Payload.Info.Total, true
}
