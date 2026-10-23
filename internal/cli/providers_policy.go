package cli

import (
	"slices"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// How long a cached health entry is believed: an available provider is looked
// at again sooner than it takes to hurt, an unavailable one sooner still so it
// comes back quickly.
const (
	availableTTL   = 5 * time.Minute
	unavailableTTL = time.Minute
)

// Where a cached entry came from.
const (
	sourceCheck = "check"
	sourceMark  = "mark"
	// sourceNone is what an answer says for a provider with no cached entry.
	sourceNone = "none"
)

// anthropicProvider is Claude's key in the cache. Claude is not a registered
// provider: only its status page is checked, and the plugin marks it.
const anthropicProvider = "anthropic"

// healthProviders is every key the cache can hold, sorted: each registered
// provider plus Claude.
func healthProviders() []string {
	names := append(provider.Names(), anthropicProvider)
	slices.Sort(names)
	return names
}

// holdsMark reports whether e is a mark that has not reached its end, which a
// check never replaces and a reader has no reason to question.
func holdsMark(e store.ProviderEntry, now time.Time) bool {
	return e.Source == sourceMark && e.ResetsAt != nil && e.ResetsAt.After(now)
}

// isStale reports whether e is old enough to be looked at again.
func isStale(e store.ProviderEntry, now time.Time) bool {
	if holdsMark(e, now) {
		return false
	}
	ttl := availableTTL
	if !e.Available {
		ttl = unavailableTTL
	}
	return now.Sub(e.CheckedAt) > ttl
}

// entryOf is the cache entry for what a check found.
func entryOf(h provider.Health) store.ProviderEntry {
	e := store.ProviderEntry{Available: h.Available, Reason: h.Reason, CheckedAt: h.CheckedAt.UTC().Truncate(time.Second), Source: sourceCheck, Skipped: h.Skipped}
	if h.ResetsAt != nil {
		at := h.ResetsAt.UTC().Truncate(time.Second)
		e.ResetsAt = &at
	}
	return e
}

// mergedEntries is the cached entries with the checked ones in their place,
// except where the cache holds a mark that has not reached its end.
func mergedEntries(cached, checked map[string]store.ProviderEntry, now time.Time) map[string]store.ProviderEntry {
	merged := make(map[string]store.ProviderEntry, len(cached)+len(checked))
	for name, entry := range cached {
		merged[name] = entry
	}
	for name, entry := range checked {
		if held, ok := cached[name]; ok && holdsMark(held, now) {
			continue
		}
		merged[name] = entry
	}
	return merged
}
