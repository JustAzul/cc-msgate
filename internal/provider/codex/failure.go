package codex

import (
	"strings"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

var _ provider.FailureClassifier = adapter{}

// failureMarkers are the pieces of Codex's own messages that tell a class. The
// classes are tried in order: a usage-limit message also offers a plan to
// upgrade to, and must not read as a plan problem.
var failureMarkers = []struct {
	class   provider.FailureClass
	markers []string
}{
	{provider.FailureRateLimit, []string{
		"hit your usage limit",
		"too many requests",
	}},
	{provider.FailureQuota, []string{
		"quota exceeded",
		"upgrade to plus:",
		"is out of credits",
		"spend cap",
	}},
	{provider.FailureAuth, []string{
		"401 unauthorized",
		"access token could not be refreshed",
		"codex login",
	}},
}

// ClassifyFailure is the class of a failed run's error text. Codex reports its
// errors as free text only, so the text is matched, ignoring case, against the
// markers of its known messages; anything else is other.
func (adapter) ClassifyFailure(errText string) provider.FailureClass {
	text := strings.ToLower(errText)
	for _, group := range failureMarkers {
		for _, marker := range group.markers {
			if strings.Contains(text, marker) {
				return group.class
			}
		}
	}
	return provider.FailureOther
}
