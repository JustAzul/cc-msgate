package e2e

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPricesWithoutACache(t *testing.T) {
	s := newSandbox(t)
	text := s.run("prices")
	if text.code != 0 || text.stdout != "no price cache: run msgate prices refresh\n" || text.stderr != "" {
		t.Errorf("text: exit %d stdout %q stderr %q", text.code, text.stdout, text.stderr)
	}
	r := s.run("prices", "--json")
	if r.code != 0 || !reflect.DeepEqual(r.json(t), map[string]any{"cached": false}) {
		t.Errorf("json: exit %d stdout %q", r.code, r.stdout)
	}
	if exists(s.home) {
		t.Error("reading the prices created the home")
	}
}

func TestPricesShowsTheCache(t *testing.T) {
	s, _ := refreshing(t)
	if r := s.run("prices", "refresh"); r.code != 0 {
		t.Fatalf("refresh exit %d: %s", r.code, r.stderr)
	}
	file := readJSONFile(t, pricesPath(s))

	r := s.run("prices", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	want := map[string]any{"cached": true}
	for k, v := range file {
		want[k] = v
	}
	if got := r.json(t); !reflect.DeepEqual(got, want) {
		t.Errorf("json = %v\nwant the cache's fields plus cached: true:\n%v", got, want)
	}

	text := s.run("prices")
	if text.code != 0 || text.stderr != "" {
		t.Fatalf("text: exit %d stderr %q", text.code, text.stderr)
	}
	for _, want := range []string{
		file["source"].(string), file["fetched_at"].(string), file["checked_at"].(string),
		"codex", "m-sol", "m-hidden", "unpriced",
	} {
		if !strings.Contains(text.stdout, want) {
			t.Errorf("text lacks %q:\n%s", want, text.stdout)
		}
	}
	var solLine string
	for _, l := range text.lines() {
		if strings.Contains(l, "m-sol") {
			solLine = l
		}
	}
	if fields := strings.Fields(solLine); !reflect.DeepEqual(fields, []string{"m-sol", "input", "2", "cached_input", "0.1", "cache_write", "2.5", "output", "10"}) {
		t.Errorf("m-sol line = %q, want the four prices", solLine)
	}
}

func TestPricesTreatsAnUnusableCacheAsAbsent(t *testing.T) {
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
			s, _ := refreshing(t)
			s.run("prices", "refresh")
			corrupt(t, s)
			if r := s.run("prices", "--json"); r.code != 0 || !reflect.DeepEqual(r.json(t), map[string]any{"cached": false}) {
				t.Errorf("json: exit %d stdout %q", r.code, r.stdout)
			}
			if r := s.run("prices"); r.stdout != "no price cache: run msgate prices refresh\n" {
				t.Errorf("text = %q", r.stdout)
			}
		})
	}
}

func TestPricesUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"refreshh"}} {
		s := newSandbox(t)
		r := s.run(append([]string{"prices", "--json"}, args...)...)
		j := r.json(t)
		if r.code != 2 || j["sdk_status"] != "usage_error" {
			t.Errorf("%v: exit %d json %v", args, r.code, j)
		}
		if msg, _ := j["error"].(string); !strings.Contains(msg, strings.TrimLeft(args[0], "-")) {
			t.Errorf("%v: error %q does not name the argument", args, msg)
		}
	}
}

func TestAnUnreadablePriceCacheIsWarnedAboutAndIgnored(t *testing.T) {
	s := newSandbox(t)
	// A directory where the file should be makes reading it fail with an
	// error other than "does not exist".
	if err := os.MkdirAll(pricesPath(s), 0o700); err != nil {
		t.Fatal(err)
	}
	r := s.run("prices")
	if r.code != 0 || r.stdout != "no price cache: run msgate prices refresh\n" {
		t.Errorf("prices: exit %d stdout %q", r.code, r.stdout)
	}
	if strings.Count(r.stderr, "msgate: warning: reading the price cache:") != 1 {
		t.Errorf("prices stderr = %q, want one warning", r.stderr)
	}
	st := s.run("stats", "--all", "--json")
	if st.code != 0 || st.json(t)["prices_checked_at"] != nil {
		t.Errorf("stats: exit %d stdout %q", st.code, st.stdout)
	}
	if strings.Count(st.stderr, "msgate: warning: reading the price cache:") != 1 {
		t.Errorf("stats stderr = %q, want one warning", st.stderr)
	}
}
