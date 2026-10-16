package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func pricesPath(s *sandbox) string { return filepath.Join(s.home, "prices.json") }

func priceListFixture() string { return filepath.Join(repoRoot, "testdata", "prices", "list.json") }

func catalogFixture() string { return filepath.Join(repoRoot, "testdata", "prices", "catalog.json") }

// priceServer serves the fixture price list on 127.0.0.1 and records every
// request it receives.
type priceServer struct {
	*httptest.Server
	mu     sync.Mutex
	reqs   []*http.Request
	body   []byte
	etag   string        // "" serves no ETag and never answers 304
	status int           // non-zero answers this status instead of the list
	delay  time.Duration // waits this long before answering
}

func newPriceServer(t *testing.T) *priceServer {
	t.Helper()
	ps := &priceServer{body: []byte(readFile(t, priceListFixture())), etag: `"e1"`}
	ps.Server = httptest.NewServer(http.HandlerFunc(ps.serve))
	t.Cleanup(ps.Server.Close)
	return ps
}

func (ps *priceServer) serve(w http.ResponseWriter, r *http.Request) {
	ps.mu.Lock()
	ps.reqs = append(ps.reqs, r.Clone(context.Background()))
	body, etag, status, delay := ps.body, ps.etag, ps.status, ps.delay
	ps.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if etag != "" {
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
	}
	w.Write(body)
}

func (ps *priceServer) requests() []*http.Request {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]*http.Request(nil), ps.reqs...)
}

func (ps *priceServer) configure(f func(*priceServer)) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	f(ps)
}

// refreshing points the sandbox at a price server and a codex catalog, and
// seeds one telemetry run on m-zero. Tests that need another shape start from
// newSandbox themselves.
func refreshing(t *testing.T) (*sandbox, *priceServer) {
	t.Helper()
	ps := newPriceServer(t)
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL).set("FAKECODEX_MODELS", catalogFixture())
	seedModelRuns(t, s, `"model":"m-zero"`)
	return s, ps
}

// seedModelRuns writes one telemetry run per field list, in one month file.
func seedModelRuns(t *testing.T, s *sandbox, fields ...string) {
	t.Helper()
	ts := tsAgo(time.Hour)
	lines := make([]string, len(fields))
	for i, f := range fields {
		lines[i] = recLine("pr-"+strconv.Itoa(i), ts, f)
	}
	writeMonthFile(t, s.home, monthOf(ts), lines...)
}

func strs(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("%#v is not a JSON list", v)
	}
	out := make([]string, len(list))
	for i, e := range list {
		if out[i], ok = e.(string); !ok {
			t.Fatalf("%#v is not a string", e)
		}
	}
	return out
}

// ageCache rewrites the times of the price cache so it looks fetched and
// checked that long ago.
func ageCache(t *testing.T, s *sandbox, fetchedAgo, checkedAgo time.Duration) {
	t.Helper()
	c := readJSONFile(t, pricesPath(s))
	c["fetched_at"], c["checked_at"] = tsAgo(fetchedAgo), tsAgo(checkedAgo)
	writeCache(t, s, c)
}

func writeCache(t *testing.T, s *sandbox, c map[string]any) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pricesPath(s), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// costCache writes the price cache the cost cases read: the fixture prices of
// m-sol, m-old and m-bare under codex (half a cent per million for m-half) and
// m-hidden unpriced, last checked at costCheckedAt.
func costCache(t *testing.T, s *sandbox) {
	t.Helper()
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		t.Fatal(err)
	}
	price := func(in, cached, write, out string) map[string]any {
		return map[string]any{"input": in, "cached_input": cached, "cache_write": write, "output": out}
	}
	writeCache(t, s, map[string]any{
		"v": 1, "source": "https://prices.example/list.json", "etag": nil,
		"fetched_at": costCheckedAt, "checked_at": costCheckedAt, "unit": "usd_per_1m_tokens",
		"models": map[string]any{"codex": map[string]any{
			"m-sol":  price("2", "0.1", "2.5", "10"),
			"m-old":  price("5", "0.5", "5", "30"),
			"m-bare": price("1", "1", "1", "4"),
			"m-half": price("0.5", "0.5", "0.5", "0.5"),
		}},
		"unpriced": map[string]any{"codex": []string{"m-hidden"}},
	})
}

const costCheckedAt = "2026-10-07T12:00:00Z"

// tokens is a run's usage as telemetry records it.
type tokens struct{ input, cached, write, output, reasoning int64 }

// usageRun is the record fields of a run of a model with that usage.
func usageRun(model string, u tokens, extra ...string) string {
	fields := []string{fmt.Sprintf(`"usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d}`,
		u.input, u.cached, u.write, u.output, u.reasoning)}
	if model != "" {
		fields = append(fields, fmt.Sprintf(`"model_used":%q`, model))
	}
	return strings.Join(append(fields, extra...), ",")
}
