package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrProvidersRefreshLockHeld is returned when another process is refreshing
// the provider cache on this home.
var ErrProvidersRefreshLockHeld = errors.New("a provider refresh is already running")

// providersVersion is the layout of providers.json this code reads and writes.
const providersVersion = 1

// ProviderEntry is one provider's cached state. Source is "check" for what a
// health check found and "mark" for what a caller wrote.
type ProviderEntry struct {
	Available bool       `json:"available"`
	Reason    string     `json:"reason"`
	CheckedAt time.Time  `json:"checked_at"`
	ResetsAt  *time.Time `json:"resets_at"`
	Source    string     `json:"source"`
	// Skipped is the "<layer>: <why>" of each check that could not run.
	Skipped []string `json:"skipped,omitempty"`
}

// SummaryModel is one model of the summary ranking: what one summary call of
// it costs in US dollars, and whether that cost is "measured" over Calls
// recorded summary calls or "estimated" over a typical call.
type SummaryModel struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	CostUSD  float64 `json:"cost_usd"`
	Basis    string  `json:"basis"`
	Calls    int64   `json:"calls"`
}

// ProviderCache is the content of the provider cache file. SummaryModels is
// the ranking of the models by the cost of a summary call, cheapest first,
// as it stood at RankedAt; it is empty until a refresh has ranked.
type ProviderCache struct {
	Version       int                      `json:"version"`
	Providers     map[string]ProviderEntry `json:"providers"`
	SummaryModels []SummaryModel           `json:"summary_models"`
	RankedAt      *time.Time               `json:"ranked_at,omitempty"`
}

func (s *Store) providersPath() string { return filepath.Join(s.Home, "providers.json") }
func (s *Store) providersRefreshLockPath() string {
	return filepath.Join(s.Home, "providers.refresh.lock")
}
func (s *Store) providersWriteLockPath() string {
	return filepath.Join(s.Home, "providers.write.lock")
}

// ProvidersRefreshLogPath is the file a detached provider refresh writes its
// diagnostics to.
func (s *Store) ProvidersRefreshLogPath() string {
	return filepath.Join(s.Home, "providers.refresh.log")
}

// OpenProvidersRefreshLog opens the refresh log for appending, creating it
// and the home with user-only permissions.
func (s *Store) OpenProvidersRefreshLog() (*os.File, error) {
	if err := os.MkdirAll(s.Home, dirMode); err != nil {
		return nil, err
	}
	return os.OpenFile(s.ProvidersRefreshLogPath(), os.O_RDWR|os.O_CREATE|os.O_APPEND, fileMode)
}

// LockProvidersRefresh takes the home's provider-refresh lock without waiting
// and returns the function releasing it. The home is created when missing. A
// lock already held yields ErrProvidersRefreshLockHeld.
func (s *Store) LockProvidersRefresh() (func(), error) {
	f, err := s.flock(s.providersRefreshLockPath(), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, ErrProvidersRefreshLockHeld
	}
	if err != nil {
		return nil, err
	}
	return func() { f.Close() }, nil // closing the descriptor drops the lock
}

// ReadProviders returns the provider cache. A missing file, or one that is not
// a version 1 cache, is an empty cache; only a file that cannot be read is an
// error.
func (s *Store) ReadProviders() (ProviderCache, error) {
	empty := ProviderCache{Version: providersVersion, Providers: map[string]ProviderEntry{}, SummaryModels: []SummaryModel{}}
	data, err := os.ReadFile(s.providersPath())
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var cache ProviderCache
	if json.Unmarshal(data, &cache) != nil || cache.Version != providersVersion {
		return empty, nil
	}
	if cache.Providers == nil {
		cache.Providers = map[string]ProviderEntry{}
	}
	if cache.SummaryModels == nil {
		cache.SummaryModels = []SummaryModel{}
	}
	return cache, nil
}

// UpdateProviders reads the cache, lets update change it and writes it back
// whole, all under the home's write lock, so concurrent updates never lose
// each other's changes.
func (s *Store) UpdateProviders(update func(*ProviderCache)) error {
	f, err := s.flock(s.providersWriteLockPath(), syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer f.Close()
	cache, err := s.ReadProviders()
	if err != nil {
		return err
	}
	update(&cache)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.providersPath(), append(data, '\n'))
}

// flock opens the lock file at path, creating the home when missing, and locks
// it the way how asks. The descriptor is close-on-exec; the caller closes it.
func (s *Store) flock(path string, how int) (*os.File, error) {
	if err := os.MkdirAll(s.Home, dirMode); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), how)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
