package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPricesRefreshUsageErrorsComeBeforeEverything(t *testing.T) {
	cases := []struct {
		name string
		args []string
		in   string // text the error must name
	}{
		{"max-age not a duration", []string{"--max-age", "soon"}, "max-age"},
		{"max-age negative", []string{"--max-age", "-1h"}, "max-age"},
		{"max-age zero", []string{"--max-age", "0s"}, "max-age"},
		{"max-age without a unit", []string{"--max-age", "90"}, "max-age"},
		{"max-age empty", []string{"--max-age", ""}, "max-age"},
		{"unknown flag", []string{"--bogus"}, "bogus"},
		{"positional argument", []string{"extra"}, "extra"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t) // the refresh is switched off: a usage error must still win
			r := s.run(append([]string{"prices", "refresh", "--json"}, c.args...)...)
			j := r.json(t)
			if r.code != 2 || j["sdk_status"] != "usage_error" || j["exit_code"] != float64(2) {
				t.Fatalf("exit %d, json %v", r.code, j)
			}
			if msg, _ := j["error"].(string); !strings.Contains(msg, c.in) || strings.Contains(msg, "unknown command") {
				t.Errorf("error = %q, want one naming %q", msg, c.in)
			}
			if exists(s.home) {
				t.Error("a usage error created the home")
			}
			text := s.run(append([]string{"prices", "refresh"}, c.args...)...)
			if text.code != 2 || text.stdout != "" || !strings.HasPrefix(text.lastStderrLine(), "msgate: ") {
				t.Errorf("text mode: exit %d stdout %q stderr %q", text.code, text.stdout, text.stderr)
			}
		})
	}
}

func TestPricesRefreshSwitchedOff(t *testing.T) {
	s := newSandbox(t) // MSGATE_PRICES_URL=off
	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["sdk_status"] != "ok" || j["exit_code"] != float64(0) || j["ran"] != false || j["changed"] != false || j["reason"] != "off" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if got := strs(t, j["priced"]); len(got) != 0 {
		t.Errorf("priced = %v, want none", got)
	}
	if got := strs(t, j["unpriced"]); len(got) != 0 {
		t.Errorf("unpriced = %v, want none", got)
	}
	if v, present := j["checked_at"]; !present || v != nil {
		t.Errorf("checked_at = %v (present %v), want null", v, present)
	}
	if v, present := j["catalog_error"]; !present || v != nil {
		t.Errorf("catalog_error = %v (present %v), want null", v, present)
	}
	if exists(s.home) {
		t.Error("a switched-off refresh created the home")
	}
	text := s.run("prices", "refresh")
	if text.code != 0 || text.stdout != "prices: off (0 priced, 0 unpriced)\n" || text.stderr != "" {
		t.Errorf("text mode: exit %d stdout %q stderr %q", text.code, text.stdout, text.stderr)
	}
}

func TestPricesRefreshLockHeldIsBusy(t *testing.T) {
	s, ps := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	before := readFile(t, pricesPath(s))
	ps.configure(func(p *priceServer) { p.reqs = nil })
	release := holdLock(t, filepath.Join(s.home, "prices.lock"))
	defer release()

	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["ran"] != false || j["changed"] != false || j["reason"] != "busy" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if len(strs(t, j["priced"])) != 4 {
		t.Errorf("priced = %v, want the cache's four", j["priced"])
	}
	if n := len(ps.requests()); n != 0 {
		t.Errorf("%d requests while the lock was held", n)
	}
	if readFile(t, pricesPath(s)) != before {
		t.Error("the cache changed while the lock was held")
	}
	text := s.run("prices", "refresh")
	if text.stdout != "prices: busy (4 priced, 1 unpriced)\n" {
		t.Errorf("text = %q", text.stdout)
	}
}

func TestPricesRefreshFreshCacheSkipsEverything(t *testing.T) {
	s, ps := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	ageCache(t, s, time.Hour, time.Hour)
	cache := readJSONFile(t, pricesPath(s))
	before := readFile(t, pricesPath(s))
	ps.configure(func(p *priceServer) { p.reqs = nil })
	record := s.recordTo()

	r := s.run("prices", "refresh", "--max-age", "24h", "--json")
	j := r.json(t)
	if r.code != 0 || j["ran"] != false || j["changed"] != false || j["reason"] != "fresh" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if got := strs(t, j["priced"]); strings.Join(got, ",") != "codex/m-bare,codex/m-old,codex/m-sol,codex/m-zero" {
		t.Errorf("priced = %v", got)
	}
	if got := strs(t, j["unpriced"]); strings.Join(got, ",") != "codex/m-hidden" {
		t.Errorf("unpriced = %v", got)
	}
	if j["checked_at"] != cache["checked_at"] {
		t.Errorf("checked_at = %v, want the cache's %v", j["checked_at"], cache["checked_at"])
	}
	if n := len(ps.requests()); n != 0 {
		t.Errorf("%d requests for a fresh cache", n)
	}
	if exists(record) {
		t.Error("codex ran for a fresh cache")
	}
	if readFile(t, pricesPath(s)) != before {
		t.Error("a fresh cache was rewritten")
	}
	if text := s.run("prices", "refresh", "--max-age", "90m"); text.stdout != "prices: fresh (4 priced, 1 unpriced)\n" {
		t.Errorf("text = %q", text.stdout)
	}
}

func TestPricesRefreshNothingWantedMakesNoRequestAndNoCache(t *testing.T) {
	ps := newPriceServer(t)
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL) // no catalog, no telemetry
	record := s.recordTo()
	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["ran"] != false || j["changed"] != false || j["reason"] != "nothing_wanted" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if msg, _ := j["catalog_error"].(string); msg == "" {
		t.Errorf("catalog_error = %v, want the reason the catalog could not be read", j["catalog_error"])
	}
	if n := len(ps.requests()); n != 0 {
		t.Errorf("%d requests with nothing wanted", n)
	}
	if exists(pricesPath(s)) {
		t.Error("prices.json written with nothing wanted")
	}
	if !exists(record) {
		t.Error("the catalog was not asked for")
	}
	if text := s.run("prices", "refresh"); text.stdout != "prices: nothing_wanted (0 priced, 0 unpriced)\n" {
		t.Errorf("text = %q", text.stdout)
	}
}

func TestPricesRefreshNothingWantedWithAnEmptyCatalog(t *testing.T) {
	ps := newPriceServer(t)
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL).set("FAKECODEX_MODELS", empty)
	j := s.run("prices", "refresh", "--json").json(t)
	if j["reason"] != "nothing_wanted" || j["catalog_error"] != nil || len(ps.requests()) != 0 {
		t.Errorf("json %v, %d requests", j, len(ps.requests()))
	}
}

func priceEntry(input, cached, write, output string) map[string]any {
	return map[string]any{"input": input, "cached_input": cached, "cache_write": write, "output": output}
}

func TestPricesRefreshWritesTheCache(t *testing.T) {
	s, ps := refreshing(t)
	record := s.recordTo()
	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || r.stderr != "" || j["sdk_status"] != "ok" || j["exit_code"] != float64(0) {
		t.Fatalf("exit %d stderr %q json %v", r.code, r.stderr, j)
	}
	if j["ran"] != true || j["changed"] != true || j["reason"] != "updated" || j["source"] != ps.URL || j["catalog_error"] != nil {
		t.Errorf("json = %v", j)
	}
	if got := strings.Join(strs(t, j["priced"]), ","); got != "codex/m-bare,codex/m-old,codex/m-sol,codex/m-zero" {
		t.Errorf("priced = %s", got)
	}
	if got := strings.Join(strs(t, j["unpriced"]), ","); got != "codex/m-hidden" {
		t.Errorf("unpriced = %s", got)
	}

	cache := readJSONFile(t, pricesPath(s))
	if cache["v"] != float64(1) || cache["unit"] != "usd_per_1m_tokens" || cache["etag"] != `"e1"` || cache["source"] != ps.URL {
		t.Errorf("cache header = %v", cache)
	}
	if cache["checked_at"] != cache["fetched_at"] || cache["checked_at"] != j["checked_at"] {
		t.Errorf("fetched_at %v, checked_at %v, reported %v; want all the same", cache["fetched_at"], cache["checked_at"], j["checked_at"])
	}
	if at, err := time.Parse(time.RFC3339, cache["checked_at"].(string)); err != nil || time.Since(at) > time.Minute {
		t.Errorf("checked_at = %v, %v; want now", at, err)
	}
	wantModels := map[string]any{"codex": map[string]any{
		"m-sol":  priceEntry("2", "0.1", "2.5", "10"),
		"m-old":  priceEntry("5", "0.5", "5", "30"),
		"m-bare": priceEntry("1", "1", "1", "4"),
		"m-zero": priceEntry("3", "0.3", "3", "12"),
	}}
	if !reflect.DeepEqual(cache["models"], wantModels) {
		t.Errorf("models = %v\nwant %v", cache["models"], wantModels)
	}
	if !reflect.DeepEqual(cache["unpriced"], map[string]any{"codex": []any{"m-hidden"}}) {
		t.Errorf("unpriced = %v", cache["unpriced"])
	}
	raw := readFile(t, pricesPath(s))
	for _, absent := range []string{"m-azure", "chatgpt", "m-half"} {
		if strings.Contains(raw, absent) {
			t.Errorf("the cache mentions %s", absent)
		}
	}
	if info, err := os.Stat(pricesPath(s)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}

	reqs := ps.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	if q := reqs[0].URL.RawQuery; q != "" || reqs[0].Method != "GET" {
		t.Errorf("request = %s ?%s", reqs[0].Method, q)
	}
	for _, h := range []string{"Authorization", "Cookie", "If-None-Match"} {
		if v := reqs[0].Header.Get(h); v != "" {
			t.Errorf("request header %s = %q, want none", h, v)
		}
	}
	if rec := readRecord(t, record); !reflect.DeepEqual(rec.Argv[1:], []string{"debug", "models"}) {
		t.Errorf("codex ran with %v, want debug models", rec.Argv)
	}
}

func TestPricesRefreshTextLine(t *testing.T) {
	s, _ := refreshing(t)
	r := s.run("prices", "refresh")
	if r.code != 0 || r.stdout != "prices: updated (4 priced, 1 unpriced)\n" || r.stderr != "" {
		t.Errorf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
}
func TestPricesRefreshStaleCacheRefreshes(t *testing.T) {
	s, ps := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	ageCache(t, s, 25*time.Hour, 25*time.Hour)
	ps.configure(func(p *priceServer) { p.reqs = nil })

	r := s.run("prices", "refresh", "--max-age", "24h", "--json")
	j := r.json(t)
	if r.code != 0 || j["ran"] != true || j["reason"] != "not_modified" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if n := len(ps.requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
	if checked, err := time.Parse(time.RFC3339, readJSONFile(t, pricesPath(s))["checked_at"].(string)); err != nil || time.Since(checked) > time.Minute {
		t.Errorf("checked_at = %v, %v; want now", checked, err)
	}
}

func TestPricesRefreshConditionalRequest(t *testing.T) {
	s, ps := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	ageCache(t, s, 2*time.Hour, 2*time.Hour)
	before := readJSONFile(t, pricesPath(s))

	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["ran"] != true || j["changed"] != false || j["reason"] != "not_modified" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	reqs := ps.requests()
	if len(reqs) != 2 || reqs[1].Header.Get("If-None-Match") != `"e1"` {
		t.Fatalf("%d requests; the second must carry If-None-Match \"e1\"", len(reqs))
	}
	after := readJSONFile(t, pricesPath(s))
	if after["fetched_at"] != before["fetched_at"] || !reflect.DeepEqual(after["models"], before["models"]) || !reflect.DeepEqual(after["unpriced"], before["unpriced"]) {
		t.Errorf("a 304 changed more than checked_at:\nbefore %v\nafter  %v", before, after)
	}
	if after["checked_at"] == before["checked_at"] || after["checked_at"] != j["checked_at"] {
		t.Errorf("checked_at %v → %v, reported %v; want it advanced to now", before["checked_at"], after["checked_at"], j["checked_at"])
	}
	if text := s.run("prices", "refresh"); text.stdout != "prices: not_modified (4 priced, 1 unpriced)\n" {
		t.Errorf("text = %q", text.stdout)
	}
}

func TestPricesRefreshNewModelForcesAFullRequest(t *testing.T) {
	s, ps := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	seedModelRuns(t, s, `"model":"m-zero"`, `"model":"m-old2"`)
	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["reason"] != "updated" || j["changed"] != true {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if reqs := ps.requests(); len(reqs) != 2 || reqs[1].Header.Get("If-None-Match") != "" {
		t.Errorf("the request for a model the cache lacks must be unconditional; got %d requests", len(reqs))
	}
	if got := strings.Join(strs(t, j["unpriced"]), ","); got != "codex/m-hidden,codex/m-old2" {
		t.Errorf("unpriced = %s", got)
	}
}

func TestPricesRefreshSourceChangeForcesAFullRequest(t *testing.T) {
	s, ps := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	other := newPriceServer(t)
	s.set("MSGATE_PRICES_URL", other.URL)
	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["reason"] != "updated" || j["source"] != other.URL {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if reqs := other.requests(); len(reqs) != 1 || reqs[0].Header.Get("If-None-Match") != "" {
		t.Errorf("a new source must be asked unconditionally; got %d requests", len(reqs))
	}
	if n := len(ps.requests()); n != 1 {
		t.Errorf("the old source saw %d requests, want only the first", n)
	}
	if got := readJSONFile(t, pricesPath(s))["source"]; got != other.URL {
		t.Errorf("cache source = %v, want %s", got, other.URL)
	}
}

func TestPricesRefreshIdempotent(t *testing.T) {
	t.Run("not modified", func(t *testing.T) {
		s, _ := refreshing(t)
		s.run("prices", "refresh")
		first := readJSONFile(t, pricesPath(s))
		j := s.run("prices", "refresh", "--json").json(t)
		second := readJSONFile(t, pricesPath(s))
		if j["changed"] != false {
			t.Errorf("changed = %v", j["changed"])
		}
		delete(first, "checked_at")
		delete(second, "checked_at")
		if !reflect.DeepEqual(first, second) {
			t.Errorf("the cache differs by more than checked_at:\n%v\n%v", first, second)
		}
	})
	t.Run("same list sent again without an entity tag", func(t *testing.T) {
		s, ps := refreshing(t)
		ps.configure(func(p *priceServer) { p.etag = "" })
		s.run("prices", "refresh")
		first := readJSONFile(t, pricesPath(s))
		if first["etag"] != nil {
			t.Errorf("etag = %v, want null", first["etag"])
		}
		j := s.run("prices", "refresh", "--json").json(t)
		if j["ran"] != true || j["reason"] != "updated" || j["changed"] != false {
			t.Errorf("json = %v; want an update that changed nothing", j)
		}
		second := readJSONFile(t, pricesPath(s))
		delete(first, "checked_at")
		delete(second, "checked_at")
		delete(first, "fetched_at")
		delete(second, "fetched_at")
		if !reflect.DeepEqual(first, second) {
			t.Errorf("the cache differs:\n%v\n%v", first, second)
		}
		if n := len(ps.requests()); n != 2 {
			t.Errorf("%d requests, want 2", n)
		}
	})
	t.Run("the list changed upstream", func(t *testing.T) {
		s, ps := refreshing(t)
		s.run("prices", "refresh")
		changed := strings.Replace(string(ps.body), `"input_cost_per_token": 1e-06`, `"input_cost_per_token": 2e-06`, 1)
		ps.configure(func(p *priceServer) { p.body, p.etag = []byte(changed), `"e2"` })
		j := s.run("prices", "refresh", "--json").json(t)
		if j["reason"] != "updated" || j["changed"] != true {
			t.Errorf("json = %v", j)
		}
		if got := readJSONFile(t, pricesPath(s))["etag"]; got != `"e2"` {
			t.Errorf("etag = %v, want \"e2\"", got)
		}
	})
}

func TestPricesRefreshReplacesAnUnusableCacheWithAFullRequest(t *testing.T) {
	cases := map[string]func(t *testing.T, s *sandbox){
		"not JSON": func(t *testing.T, s *sandbox) {
			if err := os.WriteFile(pricesPath(s), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"another version": func(t *testing.T, s *sandbox) {
			c := readJSONFile(t, pricesPath(s))
			c["v"] = 2
			writeCache(t, s, c)
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			s, ps := refreshing(t)
			s.run("prices", "refresh")
			corrupt(t, s)
			j := s.run("prices", "refresh", "--json").json(t)
			if j["ran"] != true || j["reason"] != "updated" || j["changed"] != true {
				t.Errorf("json = %v", j)
			}
			if reqs := ps.requests(); len(reqs) != 2 || reqs[1].Header.Get("If-None-Match") != "" {
				t.Errorf("an unusable cache must not make the request conditional; %d requests", len(reqs))
			}
			if v := readJSONFile(t, pricesPath(s))["v"]; v != float64(1) {
				t.Errorf("v = %v after the replacement", v)
			}
		})
	}
}

func TestPricesRefreshListUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(s *sandbox, ps *priceServer)
	}{
		{"connection refused", func(s *sandbox, ps *priceServer) { ps.Close() }},
		{"status 500", func(s *sandbox, ps *priceServer) { ps.configure(func(p *priceServer) { p.status = 500 }) }},
		{"invalid JSON", func(s *sandbox, ps *priceServer) {
			ps.configure(func(p *priceServer) { p.body, p.etag = []byte(`{"m-sol": `), "" })
		}},
		{"not a JSON object", func(s *sandbox, ps *priceServer) {
			ps.configure(func(p *priceServer) { p.body, p.etag = []byte(`[1,2]`), "" })
		}},
		{"no wanted model", func(s *sandbox, ps *priceServer) {
			ps.configure(func(p *priceServer) {
				p.body, p.etag = []byte(`{"other":{"litellm_provider":"openai","input_cost_per_token":1e-06,"output_cost_per_token":1e-06}}`), ""
			})
		}},
		{"body over the limit", func(s *sandbox, ps *priceServer) {
			s.set("MSGATE_TEST_PRICES_MAX_BYTES", "64")
			ps.configure(func(p *priceServer) { p.etag = "" })
		}},
		{"server stalls", func(s *sandbox, ps *priceServer) {
			s.set("MSGATE_TEST_PRICES_TIMEOUT_MS", "300")
			ps.configure(func(p *priceServer) { p.delay, p.etag = 10*time.Second, "" })
		}},
	}
	for _, c := range cases {
		for _, cached := range []bool{false, true} {
			name := c.name + "/no cache"
			if cached {
				name = c.name + "/cache kept"
			}
			t.Run(name, func(t *testing.T) {
				s, ps := refreshing(t)
				var before []byte
				if cached {
					if r := s.run("prices", "refresh"); r.code != 0 {
						t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
					}
					ageCache(t, s, 2*time.Hour, 2*time.Hour)
					before = []byte(readFile(t, pricesPath(s)))
				}
				c.break_(s, ps)

				r := s.run("prices", "refresh", "--json")
				j := r.json(t)
				if r.code != 7 || j["sdk_status"] != "prices_unavailable" || j["exit_code"] != float64(7) {
					t.Fatalf("exit %d, json %v", r.code, j)
				}
				if j["ran"] != true || j["changed"] != false || j["reason"] != "unavailable" {
					t.Errorf("json = %v", j)
				}
				msg, _ := j["error"].(string)
				if msg == "" || r.stderr != "msgate: "+msg+"\n" {
					t.Errorf("error = %q, stderr = %q; want one matching msgate: line", msg, r.stderr)
				}
				wantPriced, wantChecked := 0, any(nil)
				if cached {
					wantPriced, wantChecked = 4, readJSONFile(t, pricesPath(s))["checked_at"]
				}
				if n := len(strs(t, j["priced"])); n != wantPriced || j["checked_at"] != wantChecked {
					t.Errorf("priced %d, checked_at %v; want %d, %v (the cache as it stands)", n, j["checked_at"], wantPriced, wantChecked)
				}

				if cached {
					if got := readFile(t, pricesPath(s)); got != string(before) {
						t.Error("a failed refresh changed the cache")
					}
				} else if exists(pricesPath(s)) {
					t.Error("a failed refresh created prices.json")
				}

				text := s.run("prices", "refresh")
				if text.code != 7 || text.stdout != "" || strings.Count(text.stderr, "\n") != 1 || !strings.HasPrefix(text.stderr, "msgate: ") {
					t.Errorf("text mode: exit %d stdout %q stderr %q", text.code, text.stdout, text.stderr)
				}
			})
		}
	}
}

func TestPricesRefreshWantsTheModelsOfTheWholeTelemetry(t *testing.T) {
	ps := newPriceServer(t)
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL) // no catalog: telemetry only
	old := tsAgo(400 * 24 * time.Hour)
	recent := tsAgo(time.Hour)
	writeMonthFile(t, s.home, monthOf(old), recLine("w-old", old, `"model":"m-half"`))
	writeMonthFile(t, s.home, monthOf(recent),
		recLine("w-used", recent, `"model":"m-bare","model_used":"m-sol"`), // what ran wins over what was asked
		recLine("w-asked", recent, `"model":"m-old","model_used":null`),
		recLine("w-empty", recent, `"model":"","model_used":""`),
		recLine("w-null", recent, `"model":null`),
		recLine("w-unknown", recent, `"model":"unknown"`),
	)
	j := s.run("prices", "refresh", "--json").json(t)
	if j["reason"] != "updated" {
		t.Fatalf("json = %v", j)
	}
	if got := strings.Join(strs(t, j["priced"]), ","); got != "codex/m-half,codex/m-old,codex/m-sol" {
		t.Errorf("priced = %s", got)
	}
	if got := strs(t, j["unpriced"]); len(got) != 0 {
		t.Errorf("unpriced = %v", got)
	}
	if msg, _ := j["catalog_error"].(string); msg == "" {
		t.Errorf("catalog_error = %v, want the failure of the missing catalog", j["catalog_error"])
	}
}

func TestPricesRefreshCatalogFailureKeepsTheTelemetryModels(t *testing.T) {
	s, _ := refreshing(t)
	delete(s.env, "FAKECODEX_MODELS") // `codex debug models` now fails
	r := s.run("prices", "refresh", "--json")
	j := r.json(t)
	if r.code != 0 || j["reason"] != "updated" {
		t.Fatalf("exit %d, json %v", r.code, j)
	}
	if got := strings.Join(strs(t, j["priced"]), ","); got != "codex/m-zero" {
		t.Errorf("priced = %s, want only the telemetry model", got)
	}
	if msg, _ := j["catalog_error"].(string); !strings.Contains(msg, "codex") {
		t.Errorf("catalog_error = %v, want it to name the failure", j["catalog_error"])
	}
}

func TestPricesRefreshCatalogIsReadWithTheCallerEnvironment(t *testing.T) {
	s, _ := refreshing(t)
	s.withoutProvider() // codex is not on PATH: the catalog cannot be read
	j := s.run("prices", "refresh", "--json").json(t)
	if msg, _ := j["catalog_error"].(string); msg == "" || j["reason"] != "updated" {
		t.Errorf("json = %v, want the telemetry models and a catalog_error", j)
	}
}

func TestPricesRefreshProvidersWithoutACatalogAreUnpriced(t *testing.T) {
	s, _ := refreshing(t)
	seedModelRuns(t, s, `"model":"m-zero"`, `"provider":"other","model":"m-sol"`)
	j := s.run("prices", "refresh", "--json").json(t)
	if j["reason"] != "updated" {
		t.Fatalf("json = %v", j)
	}
	if got := strings.Join(strs(t, j["unpriced"]), ","); got != "codex/m-hidden,other/m-sol" {
		t.Errorf("unpriced = %s", got)
	}
	if got := strings.Join(strs(t, j["priced"]), ","); strings.Contains(got, "other/") {
		t.Errorf("priced = %s, a provider without a namespace has no price", got)
	}
}

func TestPricesRefreshTakesPricesOnlyFromTheProvidersNamespaceAndNumbers(t *testing.T) {
	ps := newPriceServer(t)
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL)
	seedModelRuns(t, s, `"model":"m-half"`, `"model":"m-text"`, `"model":"m-azure"`, `"model":"chatgpt/m-sol"`, `"model":"m-sol"`)
	j := s.run("prices", "refresh", "--json").json(t)
	if got := strings.Join(strs(t, j["priced"]), ","); got != "codex/m-half,codex/m-sol" {
		t.Errorf("priced = %s", got)
	}
	if got := strings.Join(strs(t, j["unpriced"]), ","); got != "codex/chatgpt/m-sol,codex/m-azure,codex/m-text" {
		t.Errorf("unpriced = %s", got)
	}
	cache := readJSONFile(t, pricesPath(s))
	sol := cache["models"].(map[string]any)["codex"].(map[string]any)["m-sol"]
	if !reflect.DeepEqual(sol, priceEntry("2", "0.1", "2.5", "10")) {
		t.Errorf("m-sol = %v; the chatgpt/m-sol entry must not leak in, and 1e-07 must stay exact", sol)
	}
}

func TestPricesRefreshWithOnlyUnpriceableModelsIsUnavailable(t *testing.T) {
	ps := newPriceServer(t)
	s := newSandbox(t).set("MSGATE_PRICES_URL", ps.URL)
	seedModelRuns(t, s, `"model":"m-azure"`, `"model":"m-text"`)
	r := s.run("prices", "refresh", "--json")
	if r.code != 7 || exists(pricesPath(s)) {
		t.Errorf("exit %d, cache written %v; want exit 7 and no cache", r.code, exists(pricesPath(s)))
	}
}

func TestPricesRefreshFreshDecisionReadsOnlyTheCache(t *testing.T) {
	s, _ := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("first refresh exit %d: %s", r.code, r.stderr)
	}
	// Telemetry that cannot be read would fail any refresh that looked at it.
	if err := os.RemoveAll(telemetryDir(s.home)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(telemetryDir(s.home), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := s.run("prices", "refresh", "--max-age", "24h", "--json")
	if j := r.json(t); r.code != 0 || j["reason"] != "fresh" {
		t.Errorf("exit %d, json %v; want fresh", r.code, j)
	}
	if r := s.run("prices", "refresh", "--json"); r.code != 70 {
		t.Errorf("without --max-age the unreadable telemetry must fail the refresh, got exit %d", r.code)
	}
}

func TestPricesRefreshBusyComesBeforeFresh(t *testing.T) {
	s, _ := refreshing(t)
	s.run("prices", "refresh")
	holdLock(t, filepath.Join(s.home, "prices.lock"))
	j := s.run("prices", "refresh", "--max-age", "24h", "--json").json(t)
	if j["reason"] != "busy" {
		t.Errorf("reason = %v, want busy", j["reason"])
	}
}
