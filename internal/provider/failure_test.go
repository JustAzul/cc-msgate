package provider_test

import (
	"testing"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// classifyingProvider also implements FailureClassifier, answering one class.
type classifyingProvider struct {
	plainProvider
	class provider.FailureClass
}

func (c classifyingProvider) ClassifyFailure(string) provider.FailureClass { return c.class }

func TestAProviderWithoutTheFailurePortFailsAsOther(t *testing.T) {
	got := provider.ClassifyFailure(plainProvider{}, "You hit your usage limit")

	if got != "other" {
		t.Errorf("class = %q, want other", got)
	}
}

func TestAProviderWithTheFailurePortClassifiesForItself(t *testing.T) {
	got := provider.ClassifyFailure(classifyingProvider{class: provider.FailureQuota}, "anything")

	if got != "quota" {
		t.Errorf("class = %q, want quota", got)
	}
}

func TestEachFailureClassHasTheReasonItMarksWith(t *testing.T) {
	cases := map[provider.FailureClass]string{
		"auth":       "not-logged-in",
		"quota":      "quota-exhausted",
		"rate-limit": "rate-limited",
		"other":      "",
	}
	for class, want := range cases {
		if got := class.Reason(); got != want {
			t.Errorf("%q.Reason() = %q, want %q", class, got, want)
		}
	}
}
