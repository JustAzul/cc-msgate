package provider_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// plainProvider implements Provider and nothing else.
type plainProvider struct{}

func (plainProvider) Name() string                        { return "plain" }
func (plainProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (plainProvider) ReservedFlag(string) bool            { return false }
func (plainProvider) BuildPlan(provider.Request) (provider.Plan, error) {
	return provider.Plan{}, nil
}
func (plainProvider) ParseEvent([]byte) (provider.Event, bool) { return provider.Event{}, false }
func (plainProvider) VersionArgs() []string                    { return nil }

// checkedProvider also implements HealthChecker, answering from a field.
type checkedProvider struct {
	plainProvider
	answer provider.Health
}

func (c checkedProvider) CheckHealth(context.Context, []string) provider.Health { return c.answer }

func TestAProviderWithoutTheHealthPortIsAlwaysAvailable(t *testing.T) {
	before := time.Now()

	got := provider.CheckHealth(context.Background(), plainProvider{}, nil)

	if !got.Available || got.Reason != "" || got.ResetsAt != nil {
		t.Errorf("health = %+v, want available with no reason and no reset", got)
	}
	if got.CheckedAt.Before(before) || got.CheckedAt.After(time.Now()) || got.CheckedAt.Location() != time.UTC {
		t.Errorf("CheckedAt = %v, want the current instant in UTC", got.CheckedAt)
	}
}

func TestAProviderWithTheHealthPortAnswersForItself(t *testing.T) {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	want := provider.Health{Available: false, Reason: provider.ReasonNotLoggedIn, CheckedAt: at}

	got := provider.CheckHealth(context.Background(), checkedProvider{answer: want}, nil)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("health = %+v, want %+v", got, want)
	}
}
