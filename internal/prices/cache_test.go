package prices_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
)

func sampleCache(t *testing.T) *prices.Cache {
	t.Helper()
	return &prices.Cache{
		Source:    "https://example.test/prices.json",
		ETag:      `"e1"`,
		FetchedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
		CheckedAt: time.Date(2026, 10, 8, 13, 30, 0, 0, time.UTC),
		Models: map[string]map[string]prices.Price{
			"codex": {
				"m-sol": price(t, "2", "0.1", "2.5", "10"),
				"m-old": price(t, "5", "0.5", "5", "30"),
			},
			"alpha": {"m-a": price(t, "1", "1", "1", "4")},
		},
		Unpriced: map[string][]string{"codex": {"m-hidden", "m-azure"}},
	}
}

func TestEncodeShape(t *testing.T) {
	data, err := sampleCache(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("}\n")) || bytes.HasSuffix(data, []byte("\n\n")) {
		t.Errorf("want exactly one trailing newline, got %q", data[len(data)-3:])
	}
	if got, want := topLevelKeys(t, data), []string{"v", "source", "etag", "fetched_at", "checked_at", "unit", "models", "unpriced"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fields %v, want %v", got, want)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"v":          float64(1),
		"source":     "https://example.test/prices.json",
		"etag":       `"e1"`,
		"fetched_at": "2026-10-08T12:00:00Z",
		"checked_at": "2026-10-08T13:30:00Z",
		"unit":       "usd_per_1m_tokens",
		"models": map[string]any{
			"alpha": map[string]any{"m-a": map[string]any{"input": "1", "cached_input": "1", "cache_write": "1", "output": "4"}},
			"codex": map[string]any{
				"m-old": map[string]any{"input": "5", "cached_input": "0.5", "cache_write": "5", "output": "30"},
				"m-sol": map[string]any{"input": "2", "cached_input": "0.1", "cache_write": "2.5", "output": "10"},
			},
		},
		"unpriced": map[string]any{"codex": []any{"m-azure", "m-hidden"}},
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("document\n got %v\nwant %v", doc, want)
	}
}

// topLevelKeys lists the keys of the top-level JSON object in file order.
func topLevelKeys(t *testing.T, data []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestEncodeKeysAreSortedAndStable(t *testing.T) {
	a, err := sampleCache(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := sampleCache(t).Encode()
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("encoding is not deterministic: %v", err)
		}
	}
	s := string(a)
	if strings.Index(s, `"alpha"`) > strings.Index(s, `"codex"`) || strings.Index(s, `"m-old"`) > strings.Index(s, `"m-sol"`) {
		t.Errorf("provider and model keys must be sorted:\n%s", s)
	}
}

func TestEncodeEmptyETagIsNull(t *testing.T) {
	c := sampleCache(t)
	c.ETag = ""
	data, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["etag"]) != "null" {
		t.Errorf("etag = %s, want null", doc["etag"])
	}
}

func TestEncodeEmptyCacheKeepsObjects(t *testing.T) {
	c := &prices.Cache{Source: "s", FetchedAt: time.Unix(0, 0), CheckedAt: time.Unix(0, 0)}
	data, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["models"]) != "{}" || string(doc["unpriced"]) != "{}" {
		t.Errorf("models %s unpriced %s, want empty objects", doc["models"], doc["unpriced"])
	}
}

func TestEncodeTimesAreUTC(t *testing.T) {
	c := sampleCache(t)
	c.FetchedAt = time.Date(2026, 10, 8, 9, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	data, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"2026-10-08T12:00:00Z"`) {
		t.Errorf("fetched_at not in UTC:\n%s", data)
	}
}

func TestEncodeRefusesPriceWithoutDecimalForm(t *testing.T) {
	c := sampleCache(t)
	c.Models["codex"]["m-third"] = price(t, "1/3", "1", "1", "1")
	if _, err := c.Encode(); err == nil {
		t.Error("want an error for a price that does not terminate")
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	want := sampleCache(t)
	data, err := want.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := prices.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != want.Source || got.ETag != want.ETag || !got.FetchedAt.Equal(want.FetchedAt) || !got.CheckedAt.Equal(want.CheckedAt) {
		t.Errorf("header = %+v, want %+v", got, want)
	}
	if got.FetchedAt.Location() != time.UTC {
		t.Errorf("times must decode in UTC, got %v", got.FetchedAt.Location())
	}
	if len(got.Models) != len(want.Models) {
		t.Fatalf("models %v, want %v", got.Models, want.Models)
	}
	for provider, models := range want.Models {
		for name, p := range models {
			if g, ok := got.Models[provider][name]; !ok || !samePrice(g, p) {
				t.Errorf("%s/%s = %v, want %s", provider, name, g, showPrice(p))
			}
		}
	}
	if wantUnpriced := map[string][]string{"codex": {"m-azure", "m-hidden"}}; !reflect.DeepEqual(got.Unpriced, wantUnpriced) {
		t.Errorf("unpriced = %v, want %v", got.Unpriced, wantUnpriced)
	}
	again, err := got.Encode()
	if err != nil || !bytes.Equal(again, data) {
		t.Errorf("re-encoding differs (%v):\n%s\n%s", err, again, data)
	}
}

func TestDecodeNullETag(t *testing.T) {
	c := sampleCache(t)
	c.ETag = ""
	data, _ := c.Encode()
	got, err := prices.Decode(data)
	if err != nil || got.ETag != "" {
		t.Errorf("etag %q, %v", got.ETag, err)
	}
}

func TestDecodeRejectsUnusableFiles(t *testing.T) {
	good, err := sampleCache(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(from, to string) string {
		if !strings.Contains(string(good), from) {
			t.Fatalf("fixture lacks %q", from)
		}
		return strings.Replace(string(good), from, to, 1)
	}
	cases := map[string]string{
		"empty":               "",
		"truncated":           "{",
		"not an object":       "[]",
		"null":                "null",
		"other version":       mutate(`"v": 1`, `"v": 2`),
		"no version":          mutate(`"v": 1,`, ``),
		"other unit":          mutate(`usd_per_1m_tokens`, `eur_per_1m_tokens`),
		"exponent price":      mutate(`"input": "1"`, `"input": "1e-07"`),
		"word price":          mutate(`"input": "1"`, `"input": "abc"`),
		"number price":        mutate(`"input": "1"`, `"input": 1`),
		"negative price":      mutate(`"input": "1"`, `"input": "-1"`),
		"empty price":         mutate(`"input": "1"`, `"input": ""`),
		"missing price field": mutate(`"output": "4"`, `"extra": "4"`),
		"bad fetched_at":      mutate(`"2026-10-08T12:00:00Z"`, `"yesterday"`),
		"no checked_at":       mutate(`"checked_at": "2026-10-08T13:30:00Z",`, ``),
		"no models":           strings.Replace(string(good), `"models"`, `"modelz"`, 1),
		"no unpriced":         strings.Replace(string(good), `"unpriced"`, `"unpricedz"`, 1),
		"unpriced not a list": mutate(`"unpriced": {`, `"unpriced": {"x": 3,`),
		"data after the file": string(good) + `{}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := prices.Decode([]byte(in))
			if !errors.Is(err, prices.ErrUnusable) {
				t.Errorf("err = %v, want ErrUnusable (cache %+v)", err, got)
			}
		})
	}
}

func TestCacheLookupKnowsCovers(t *testing.T) {
	c := sampleCache(t)
	if p, ok := c.Lookup("codex", "m-sol"); !ok || !samePrice(p, price(t, "2", "0.1", "2.5", "10")) {
		t.Errorf("Lookup(m-sol) = %v, %v", p, ok)
	}
	for _, miss := range [][2]string{{"codex", "m-hidden"}, {"codex", "m-new"}, {"alpha", "m-sol"}, {"beta", "m-a"}} {
		if _, ok := c.Lookup(miss[0], miss[1]); ok {
			t.Errorf("Lookup(%v) found a price", miss)
		}
	}
	knows := map[[2]string]bool{
		{"codex", "m-sol"}:    true,
		{"codex", "m-hidden"}: true,
		{"codex", "m-new"}:    false,
		{"alpha", "m-hidden"}: false,
		{"beta", "m-a"}:       false,
	}
	for key, want := range knows {
		if got := c.Knows(key[0], key[1]); got != want {
			t.Errorf("Knows(%v) = %v, want %v", key, got, want)
		}
	}
	covers := []struct {
		name   string
		wanted map[string][]string
		want   bool
	}{
		{"empty map", map[string][]string{}, true},
		{"nil map", nil, true},
		{"provider with no models", map[string][]string{"beta": {}}, true},
		{"priced and unpriced", map[string][]string{"codex": {"m-sol", "m-hidden"}, "alpha": {"m-a"}}, true},
		{"one unknown", map[string][]string{"codex": {"m-sol", "m-new"}}, false},
		{"unknown provider", map[string][]string{"beta": {"m-a"}}, false},
	}
	for _, tc := range covers {
		if got := c.Covers(tc.wanted); got != tc.want {
			t.Errorf("Covers(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
