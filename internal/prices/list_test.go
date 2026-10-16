package prices_test

import (
	"reflect"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/prices"
)

// fixtureList is a LiteLLM-shaped price list with fictitious models.
const fixtureList = `{
  "sample_spec": {"litellm_provider": "openai", "input_cost_per_token": 0.0},
  "m-sol":  {"litellm_provider": "openai", "input_cost_per_token": 2e-06, "cache_read_input_token_cost": 1e-07, "cache_creation_input_token_cost": 2.5e-06, "output_cost_per_token": 1e-05},
  "m-old":  {"litellm_provider": "openai", "input_cost_per_token": 5e-06, "cache_read_input_token_cost": 5e-07, "output_cost_per_token": 3e-05},
  "m-bare": {"litellm_provider": "openai", "input_cost_per_token": 1e-06, "output_cost_per_token": 4e-06},
  "m-zero": {"litellm_provider": "openai", "input_cost_per_token": 3e-06, "cache_read_input_token_cost": 3e-07, "cache_creation_input_token_cost": 0, "output_cost_per_token": 1.2e-05},
  "m-half": {"litellm_provider": "openai", "input_cost_per_token": 5e-07, "output_cost_per_token": 5e-07},
  "m-text": {"litellm_provider": "openai", "input_cost_per_token": "2e-06", "output_cost_per_token": 1e-05},
  "chatgpt/m-sol": {"litellm_provider": "chatgpt", "input_cost_per_token": null, "output_cost_per_token": null},
  "m-azure": {"litellm_provider": "azure", "input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05}
}`

var codexNS = map[string]string{"codex": "openai"}

func parseFixture(t *testing.T, wanted map[string][]string, ns map[string]string) (map[string]map[string]prices.Price, map[string][]string) {
	t.Helper()
	models, unpriced, err := prices.ParseList([]byte(fixtureList), wanted, ns)
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	return models, unpriced
}

func samePrice(a, b prices.Price) bool {
	return a.Input.Cmp(b.Input) == 0 && a.CachedInput.Cmp(b.CachedInput) == 0 &&
		a.CacheWrite.Cmp(b.CacheWrite) == 0 && a.Output.Cmp(b.Output) == 0
}

func showPrice(p prices.Price) string {
	return p.Input.RatString() + "/" + p.CachedInput.RatString() + "/" + p.CacheWrite.RatString() + "/" + p.Output.RatString()
}

func TestParseListResolvesPrices(t *testing.T) {
	wanted := map[string][]string{"codex": {"m-sol", "m-old", "m-bare", "m-zero", "m-half"}}
	models, unpriced := parseFixture(t, wanted, codexNS)
	want := map[string]prices.Price{
		"m-sol":  price(t, "2", "0.1", "2.5", "10"),
		"m-old":  price(t, "5", "0.5", "5", "30"),
		"m-bare": price(t, "1", "1", "1", "4"),
		"m-zero": price(t, "3", "0.3", "3", "12"),
		"m-half": price(t, "0.5", "0.5", "0.5", "0.5"),
	}
	if len(models["codex"]) != len(want) {
		t.Fatalf("priced %d models, want %d: %v", len(models["codex"]), len(want), models["codex"])
	}
	for name, w := range want {
		got, ok := models["codex"][name]
		if !ok {
			t.Errorf("%s not priced", name)
			continue
		}
		if !samePrice(got, w) {
			t.Errorf("%s = %s, want %s", name, showPrice(got), showPrice(w))
		}
	}
	if len(unpriced) != 0 {
		t.Errorf("unpriced = %v, want none", unpriced)
	}
}

func TestParseListUnpricedModels(t *testing.T) {
	cases := []struct {
		name  string
		model string
		why   string
	}{
		{"m-text", "m-text", "a price given as a JSON string"},
		{"m-azure", "m-azure", "another litellm_provider"},
		{"m-hidden", "m-hidden", "absent from the list"},
		{"null prices", "chatgpt/m-sol", "a key under another provider namespace, null prices"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			models, unpriced := parseFixture(t, map[string][]string{"codex": {c.model}}, codexNS)
			if _, ok := models["codex"][c.model]; ok {
				t.Errorf("%s priced, want unpriced (%s)", c.model, c.why)
			}
			if !reflect.DeepEqual(unpriced["codex"], []string{c.model}) {
				t.Errorf("unpriced = %v, want [%s] (%s)", unpriced, c.model, c.why)
			}
		})
	}
}

func TestParseListChatgptKeyNeverMatchesBareName(t *testing.T) {
	// Only the chatgpt/ key is wanted-by-name; "m-sol" under the openai
	// namespace is priced from its own entry, not from the chatgpt one.
	models, _ := parseFixture(t, map[string][]string{"codex": {"m-sol"}}, codexNS)
	if got := models["codex"]["m-sol"]; got.Input == nil || got.Input.Cmp(rat(t, "2")) != 0 {
		t.Errorf("m-sol = %+v", got)
	}
}

func TestParseListUnpricedIsSortedAndUnique(t *testing.T) {
	wanted := map[string][]string{"codex": {"m-sol", "m-z", "m-text", "m-hidden", "m-z"}}
	models, unpriced := parseFixture(t, wanted, codexNS)
	if want := []string{"m-hidden", "m-text", "m-z"}; !reflect.DeepEqual(unpriced["codex"], want) {
		t.Errorf("unpriced = %v, want %v", unpriced["codex"], want)
	}
	if len(models["codex"]) != 1 {
		t.Errorf("priced = %v, want only m-sol", models["codex"])
	}
}

func TestParseListProviderWithoutNamespace(t *testing.T) {
	wanted := map[string][]string{"codex": {"m-sol"}, "other": {"m-old", "m-bare"}}
	for name, ns := range map[string]map[string]string{
		"absent":      codexNS,
		"empty value": {"codex": "openai", "other": ""},
		"nil map":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			models, unpriced := parseFixture(t, wanted, ns)
			if len(models["other"]) != 0 {
				t.Errorf("other priced: %v", models["other"])
			}
			if want := []string{"m-bare", "m-old"}; !reflect.DeepEqual(unpriced["other"], want) {
				t.Errorf("unpriced[other] = %v, want %v", unpriced["other"], want)
			}
			if name != "nil map" && len(models["codex"]) != 1 {
				t.Errorf("codex priced = %v, want m-sol", models["codex"])
			}
		})
	}
}

func TestParseListReadsExactDecimals(t *testing.T) {
	body := `{"m": {"litellm_provider": "openai", "input_cost_per_token": 1e-07, "cache_read_input_token_cost": 0.000000125, "output_cost_per_token": 1E-7}}`
	models, _, err := prices.ParseList([]byte(body), map[string][]string{"codex": {"m"}}, codexNS)
	if err != nil {
		t.Fatal(err)
	}
	got := models["codex"]["m"]
	if got.Input.Cmp(rat(t, "1/10")) != 0 || got.CachedInput.Cmp(rat(t, "0.125")) != 0 || got.Output.Cmp(rat(t, "1/10")) != 0 {
		t.Errorf("price = %s, want 1/10/-/1/10 with cache read 1/8", showPrice(got))
	}
}

func TestParseListRejectsUnusableEntries(t *testing.T) {
	cases := map[string]string{
		"missing output":   `{"m": {"litellm_provider": "openai", "input_cost_per_token": 1e-06}}`,
		"null input":       `{"m": {"litellm_provider": "openai", "input_cost_per_token": null, "output_cost_per_token": 1e-06}}`,
		"negative input":   `{"m": {"litellm_provider": "openai", "input_cost_per_token": -1e-06, "output_cost_per_token": 1e-06}}`,
		"string output":    `{"m": {"litellm_provider": "openai", "input_cost_per_token": 1e-06, "output_cost_per_token": "1e-06"}}`,
		"entry not object": `{"m": 3}`,
		"huge exponent":    `{"m": {"litellm_provider": "openai", "input_cost_per_token": 1e999999999, "output_cost_per_token": 1e-06}}`,
		"no provider":      `{"m": {"input_cost_per_token": 1e-06, "output_cost_per_token": 1e-06}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			models, unpriced, err := prices.ParseList([]byte(body), map[string][]string{"codex": {"m"}}, codexNS)
			if err != nil {
				t.Fatal(err)
			}
			if len(models["codex"]) != 0 || !reflect.DeepEqual(unpriced["codex"], []string{"m"}) {
				t.Errorf("models %v unpriced %v, want m unpriced", models, unpriced)
			}
		})
	}
}

func TestParseListOmittedCacheFields(t *testing.T) {
	body := `{"m": {"litellm_provider": "openai", "input_cost_per_token": 1e-06, "cache_read_input_token_cost": null, "cache_creation_input_token_cost": null, "output_cost_per_token": 4e-06}}`
	models, _, err := prices.ParseList([]byte(body), map[string][]string{"codex": {"m"}}, codexNS)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := models["codex"]["m"], price(t, "1", "1", "1", "4"); !samePrice(got, want) {
		t.Errorf("price = %s, want %s", showPrice(got), showPrice(want))
	}
}

func TestParseListRejectsBadBodies(t *testing.T) {
	for name, body := range map[string]string{
		"empty":         ``,
		"not json":      `{`,
		"array":         `[]`,
		"string":        `"x"`,
		"null":          `null`,
		"trailing data": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := prices.ParseList([]byte(body), map[string][]string{"codex": {"m"}}, codexNS); err == nil {
				t.Error("want an error")
			}
		})
	}
}
