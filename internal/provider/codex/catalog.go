package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// catalogTimeout bounds the `codex debug models` call.
const catalogTimeout = 10 * time.Second

// PriceNamespace is the litellm_provider of the price-list entries of the
// models codex offers.
func (adapter) PriceNamespace() string { return "openai" }

// CatalogModels lists the distinct non-empty model slugs `codex debug models`
// prints, in the order given.
func (a adapter) CatalogModels(env []string) ([]string, error) {
	entries, err := a.CatalogEntries(env)
	if err != nil {
		return nil, err
	}
	slugs := []string{}
	for _, e := range entries {
		if e.ID != "" && !slices.Contains(slugs, e.ID) {
			slugs = append(slugs, e.ID)
		}
	}
	return slugs, nil
}

// CatalogEntries lists every model `codex debug models` prints, in the order
// given: the slug as ID, the display name as Name and the visibility ("list"
// or "hide"). The binary is resolved on the PATH of env and runs with env,
// without standard input, and is stopped after ten seconds.
func (adapter) CatalogEntries(env []string) ([]provider.CatalogEntry, error) {
	bin, err := lookPath("codex", env)
	if err != nil {
		return nil, fmt.Errorf("codex debug models: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "debug", "models")
	cmd.Env = env
	cmd.WaitDelay = time.Second // a grandchild holding the pipe must not outlast the timeout
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if tail := strings.TrimSpace(string(exit.Stderr)); tail != "" {
				return nil, fmt.Errorf("codex debug models: %w: %s", err, tail)
			}
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("codex debug models: timed out after %s", catalogTimeout)
		}
		return nil, fmt.Errorf("codex debug models: %w", err)
	}
	return parseCatalog(out)
}

// parseCatalog reads {"models":[{"slug","display_name","visibility"}]}: every
// entry, in the order given.
func parseCatalog(out []byte) ([]provider.CatalogEntry, error) {
	var doc struct {
		Models *[]struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("codex debug models: output is not JSON: %w", err)
	}
	if doc.Models == nil {
		return nil, errors.New("codex debug models: output has no models list")
	}
	entries := make([]provider.CatalogEntry, 0, len(*doc.Models))
	for _, m := range *doc.Models {
		entries = append(entries, provider.CatalogEntry{ID: m.Slug, Name: m.DisplayName, Visibility: m.Visibility})
	}
	return entries, nil
}

// lookPath resolves a binary against the PATH of env, not the process's.
func lookPath(file string, env []string) (string, error) {
	pathVar := lookupEnv(env, "PATH")
	for _, dir := range filepath.SplitList(pathVar) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, file)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("codex %w", provider.ErrBinaryMissing)
}
