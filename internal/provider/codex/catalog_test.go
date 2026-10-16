package codex_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/provider/codex"
)

// fakeCodexOn writes an executable named codex into a new directory and
// returns that directory.
func fakeCodexOn(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func catalog(t *testing.T) provider.ModelCatalog {
	t.Helper()
	c, ok := codex.New().(provider.ModelCatalog)
	if !ok {
		t.Fatal("the codex adapter does not declare a model catalog")
	}
	return c
}

func TestCodexPriceNamespaceIsOpenAI(t *testing.T) {
	if got := catalog(t).PriceNamespace(); got != "openai" {
		t.Errorf("PriceNamespace = %q, want openai", got)
	}
}

func TestCatalogModelsAreTheSlugsOfDebugModels(t *testing.T) {
	dir := fakeCodexOn(t, `
[ "$1 $2" = "debug models" ] || exit 9
echo '{"models":[{"slug":"m-a","visibility":"list"},{"slug":"m-b"},{"slug":""},{"slug":"m-a"}]}'
`)
	got, err := catalog(t).CatalogModels([]string{"PATH=" + dir})
	if err != nil {
		t.Fatalf("CatalogModels: %v", err)
	}
	if want := []string{"m-a", "m-b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("models = %v, want %v", got, want)
	}
}

func TestCatalogRunsCodexWithTheGivenEnvironment(t *testing.T) {
	dir := fakeCodexOn(t, `echo "{\"models\":[{\"slug\":\"$SEEN\"}]}"`)
	got, err := catalog(t).CatalogModels([]string{"PATH=" + dir, "SEEN=from-env"})
	if err != nil || !reflect.DeepEqual(got, []string{"from-env"}) {
		t.Errorf("models = %v, %v; want the value of the given environment", got, err)
	}
}

func TestCatalogFailures(t *testing.T) {
	cases := []struct {
		name string
		env  func(t *testing.T) []string
	}{
		{"codex exits non-zero", func(t *testing.T) []string {
			return []string{"PATH=" + fakeCodexOn(t, "echo boom >&2; exit 1")}
		}},
		{"output is not JSON", func(t *testing.T) []string {
			return []string{"PATH=" + fakeCodexOn(t, "echo not-json")}
		}},
		{"output has no models list", func(t *testing.T) []string {
			return []string{"PATH=" + fakeCodexOn(t, "echo '{}'")}
		}},
		{"codex is not on the PATH", func(t *testing.T) []string {
			return []string{"PATH=" + t.TempDir()}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := catalog(t).CatalogModels(c.env(t))
			if err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Errorf("models = %v, error = %v; want a non-empty error", got, err)
			}
		})
	}
}
