package provider

import (
	"context"
	"time"
)

// Reasons a provider is unavailable, or "unknown" when none of its checks
// could run (the provider then counts as available).
const (
	ReasonNotInstalled   = "not-installed"
	ReasonNotLoggedIn    = "not-logged-in"
	ReasonRateLimited    = "rate-limited"
	ReasonQuotaExhausted = "quota-exhausted"
	ReasonProviderOutage = "provider-outage"
	ReasonUnknown        = "unknown"
)

// Health is what a provider's checks found, without a model call.
type Health struct {
	Available bool       `json:"available"`
	Reason    string     `json:"reason,omitempty"`
	CheckedAt time.Time  `json:"checked_at"`
	ResetsAt  *time.Time `json:"resets_at,omitempty"`
	// Skipped names, as "<layer>: <why>", each check that could not run. A check
	// switched off on purpose is not listed.
	Skipped []string `json:"skipped,omitempty"`
}

// HealthChecker is a provider that can tell whether it is usable now.
type HealthChecker interface {
	CheckHealth(ctx context.Context, env []string) Health
}

// CheckHealth is p's health: its own answer when it implements HealthChecker,
// else available, because a provider without checks is never held back.
func CheckHealth(ctx context.Context, p Provider, env []string) Health {
	if checker, ok := p.(HealthChecker); ok {
		return checker.CheckHealth(ctx, env)
	}
	return Health{Available: true, CheckedAt: time.Now().UTC()}
}
