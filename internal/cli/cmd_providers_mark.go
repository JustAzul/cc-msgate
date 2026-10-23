package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// markReasons are the reasons a caller may give for marking a provider down;
// "unknown" is not one, because it describes a check that could not run.
var markReasons = []string{
	provider.ReasonNotInstalled, provider.ReasonNotLoggedIn, provider.ReasonRateLimited,
	provider.ReasonQuotaExhausted, provider.ReasonProviderOutage,
}

// runProvidersMark writes a provider as unavailable until its next check, or
// until the time given with --until.
func runProvidersMark(ctx *Context, args []string) int {
	var reason, until string
	fset := flag.NewFlagSet("providers mark", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.StringVar(&reason, "reason", "", "why the provider is unavailable")
	fset.StringVar(&until, "until", "", "when it is expected back (RFC3339)")
	positional, err := parseInterspersed(fset, args)
	if err != nil {
		return failFlagParse(ctx, providersUsage, err)
	}
	if len(positional) != 1 || !slices.Contains(healthProviders(), positional[0]) {
		return ctx.Fail(ExitUsage, "providers mark needs one provider out of %v", healthProviders())
	}
	if !slices.Contains(markReasons, reason) {
		return ctx.Fail(ExitUsage, "providers mark needs --reason out of %v, got %q", markReasons, reason)
	}
	entry, err := markEntry(reason, until, ctx.Now())
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	name := positional[0]
	if err := store.Open(home).UpdateProviders(func(c *store.ProviderCache) { c.Providers[name] = entry }); err != nil {
		return ctx.Fail(ExitInternal, "writing the provider cache: %v", err)
	}
	fmt.Fprintf(ctx.Stdout, "providers: %s marked unavailable (%s)\n", name, reason)
	return ExitOK
}

// markEntry is the unavailable entry a mark writes. until is empty or RFC3339.
func markEntry(reason, until string, now time.Time) (store.ProviderEntry, error) {
	entry := store.ProviderEntry{Reason: reason, CheckedAt: now.UTC().Truncate(time.Second), Source: sourceMark}
	if until == "" {
		return entry, nil
	}
	at, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return entry, errors.New("providers mark needs --until as an RFC3339 time such as 2026-10-23T18:00:00Z")
	}
	at = at.UTC()
	entry.ResetsAt = &at
	return entry, nil
}
