package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/statuspage"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// anthropicStatusEnv names the environment variable that points Claude's
// status page check at another source, or switches it off with "off".
const anthropicStatusEnv = "MSGATE_STATUS_URL_ANTHROPIC"

// defaultAnthropicStatusURL is Anthropic's status page.
const defaultAnthropicStatusURL = "https://status.claude.com/api/v2/summary.json"

// anthropicStatusTimeout is how long the status page may take before the check
// counts as unable to run.
const anthropicStatusTimeout = 5 * time.Second

// anthropicComponents are the status page components whose major outage stops
// Claude.
var anthropicComponents = []string{"Claude API (api.anthropic.com)", "Claude Code"}

// runProvidersRefresh checks every provider and writes the cache. It takes the
// refresh lock without waiting: when another refresh holds it, this one has
// nothing to do and exits 0 at once.
func runProvidersRefresh(ctx *Context, args []string) int {
	fset := flag.NewFlagSet("providers refresh", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	detached := fset.Bool(strings.TrimPrefix(detachedFlag, "--"), false, "")
	positional, err := parseInterspersed(fset, args)
	if err != nil {
		return failFlagParse(ctx, providersUsage, err)
	}
	if len(positional) != 0 {
		return ctx.Fail(ExitUsage, "providers refresh takes no arguments, got %q", positional[0])
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	if *detached {
		if log, err := st.OpenProvidersRefreshLog(); err == nil {
			defer log.Close()
			ctx.Stderr = store.NewTailWriter(log, store.WorkerLogLimit) // what a refresh says stays within the log's bound
		}
	}
	release, err := st.LockProvidersRefresh()
	if errors.Is(err, store.ErrProvidersRefreshLockHeld) {
		refreshLog(ctx, "busy")
		fmt.Fprintln(ctx.Stdout, "providers: another refresh is running")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "locking the provider refresh: %v", err)
	}
	defer release()
	refreshGate(ctx)

	checked := checkAll(ctx)
	now := ctx.Now()
	ranking, ranked := rankSummaryModels(ctx, st, home, now)
	if err := st.UpdateProviders(func(c *store.ProviderCache) {
		c.Providers = mergedEntries(c.Providers, checked, now)
		if ranked {
			c.SummaryModels, c.RankedAt = ranking, &now
		}
	}); err != nil {
		code := ctx.Fail(ExitInternal, "writing the provider cache: %v", err)
		refreshLog(ctx, "failed") // after the report: a test that sees failed finds it written
		return code
	}
	refreshLog(ctx, "ran")
	fmt.Fprintf(ctx.Stdout, "providers: refreshed (%d checked)\n", len(checked))
	return ExitOK
}

// checkAll runs every provider's check at once, since each may wait on a
// process or a page, and returns what each found.
func checkAll(ctx *Context) map[string]store.ProviderEntry {
	names := healthProviders()
	entries := make([]store.ProviderEntry, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entries[i] = entryOf(checkProvider(ctx, name))
		}()
	}
	wg.Wait()
	checked := make(map[string]store.ProviderEntry, len(names))
	for i, name := range names {
		checked[name] = entries[i]
	}
	return checked
}

// checkProvider is one provider's health: Claude by its status page alone, a
// registered provider by its own checks.
func checkProvider(ctx *Context, name string) provider.Health {
	if name == anthropicProvider {
		return checkAnthropic(ctx)
	}
	p, _ := provider.Get(name)
	return provider.CheckHealth(context.Background(), p, ctx.Env)
}

// checkAnthropic is Claude's health from its status page: a major outage of a
// watched component marks it down. A page that cannot be read, or is switched
// off, says nothing, and with nothing to go on the reason is unknown.
func checkAnthropic(ctx *Context) provider.Health {
	health := provider.Health{CheckedAt: time.Now().UTC()}
	url := ctx.Getenv(anthropicStatusEnv)
	if url == "" {
		url = defaultAnthropicStatusURL
	}
	timed, cancel := context.WithTimeout(context.Background(), anthropicStatusTimeout)
	defer cancel()
	down, err := statuspage.MajorOutage(timed, url, anthropicComponents...)
	switch {
	case err == nil && down:
		health.Reason = provider.ReasonProviderOutage
	case err == nil:
		health.Available = true
	default:
		health.Available, health.Reason = true, provider.ReasonUnknown
		if why := statusSkipReason(timed, err); why != "" {
			health.Skipped = []string{"status page: " + why}
		}
	}
	return health
}

// statusSkipReason says briefly why the status page could not be read; it is
// empty for a page switched off on purpose.
func statusSkipReason(timed context.Context, err error) string {
	switch {
	case errors.Is(err, statuspage.ErrOff):
		return ""
	case timed.Err() != nil:
		return "timed out"
	case errors.Is(err, statuspage.ErrUnreadable):
		return "unreadable answer"
	}
	return "unreachable"
}
