package provider

// FailureClass is what kind of failure a run's error text describes.
type FailureClass string

// The classes of failure.
const (
	FailureAuth      FailureClass = "auth"
	FailureQuota     FailureClass = "quota"
	FailureRateLimit FailureClass = "rate-limit"
	FailureOther     FailureClass = "other"
)

// FailureClassifier is a provider that can tell, from the text of a failed
// run's error, why the provider cannot be used.
type FailureClassifier interface {
	ClassifyFailure(errText string) FailureClass
}

// Reason is the health reason a failure of this class marks a provider with,
// or "" for a class that marks nothing.
func (c FailureClass) Reason() string {
	switch c {
	case FailureAuth:
		return ReasonNotLoggedIn
	case FailureQuota:
		return ReasonQuotaExhausted
	case FailureRateLimit:
		return ReasonRateLimited
	}
	return ""
}

// ClassifyFailure is the class of errText for p: its own answer when it
// implements FailureClassifier, else other, because a provider that cannot tell
// is never held back.
func ClassifyFailure(p Provider, errText string) FailureClass {
	if classifier, ok := p.(FailureClassifier); ok {
		return classifier.ClassifyFailure(errText)
	}
	return FailureOther
}
