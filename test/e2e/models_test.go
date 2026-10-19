package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const twoModels = `{"models":[
  {"slug":"m-hidden","display_name":"M Hidden","visibility":"hide"},
  {"slug":"m-listed","display_name":"M Listed","visibility":"list"}
]}`

// catalogFile writes content to a file and returns its path, for FAKECODEX_MODELS.
func catalogFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestModelsJSONPrintsTheCatalogInTheProvidersOrder(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_MODELS", catalogFile(t, twoModels))
	r := s.run("models", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	want := map[string]any{
		"provider": "codex",
		"models": []any{
			map[string]any{"id": "m-hidden", "name": "M Hidden", "visibility": "hide"},
			map[string]any{"id": "m-listed", "name": "M Listed", "visibility": "list"},
		},
	}
	if got := r.json(t); !reflect.DeepEqual(got, want) {
		t.Errorf("models --json = %v, want %v", got, want)
	}
}

func TestModelsWithoutTheProviderExits127(t *testing.T) {
	s := newSandbox(t).withoutProvider()
	r := s.run("models", "--json")
	if r.code != 127 {
		t.Fatalf("exit %d, want 127; stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	m := r.json(t)
	if m["sdk_status"] != "provider_missing" || m["exit_code"] != float64(127) {
		t.Errorf("error object = %v, want provider_missing with exit_code 127", m)
	}
	if plain := s.run("models"); plain.code != 127 || plain.stdout != "" {
		t.Errorf("plain: exit %d, stdout %q, want 127 and nothing on stdout", plain.code, plain.stdout)
	}
}

func TestModelsUnreadableCatalogExits70WithTheAdapterError(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_MODELS", catalogFile(t, "not json"))
	r := s.run("models", "--json")
	if r.code != 70 {
		t.Fatalf("exit %d, want 70; stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	m := r.json(t)
	if m["sdk_status"] != "internal_error" || m["exit_code"] != float64(70) {
		t.Errorf("error object = %v, want internal_error with exit_code 70", m)
	}
	if msg, _ := m["error"].(string); !contains(msg, "codex debug models: output is not JSON") {
		t.Errorf("error = %q, want the adapter's text", msg)
	}
	if plain := s.run("models"); plain.code != 70 || plain.stdout != "" {
		t.Errorf("plain: exit %d, stdout %q, want 70 and nothing on stdout", plain.code, plain.stdout)
	}
}

func TestModelsUsageErrorsExit2(t *testing.T) {
	cases := map[string][]string{
		"unknown provider":   {"models", "--provider", "nope"},
		"extra argument":     {"models", "extra"},
		"unknown flag":       {"models", "--nope"},
		"provider w/o value": {"models", "--provider"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			r := newSandbox(t).run(append([]string{"models", "--json"}, args[1:]...)...)
			if r.code != 2 {
				t.Fatalf("exit %d, want 2; stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if m := r.json(t); m["sdk_status"] != "usage_error" {
				t.Errorf("error object = %v, want usage_error", m)
			}
		})
	}
}

func TestModelsPlainPrintsOneTabSeparatedLinePerModel(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_MODELS", catalogFile(t, twoModels))
	r := s.run("models")
	if r.code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	want := []string{"m-hidden\tM Hidden\thide", "m-listed\tM Listed\tlist"}
	if got := r.lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("models lines = %q, want %q", got, want)
	}
}
