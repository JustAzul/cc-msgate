package codex

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/statuspage"
)

var _ provider.HealthChecker = adapter{}

// How long each layer may take before it counts as unable to run.
const (
	loginStatusTimeout = 5 * time.Second
	statusPageTimeout  = 5 * time.Second
	waitDelay          = time.Second // a grandchild holding a pipe must not outlast the timeout
)

type outcome int

const (
	passed  outcome = iota // the layer found nothing wrong
	failed                 // the layer found the provider unusable
	skipped                // the layer could not run: it says nothing
)

// verdict is what one layer found. why says briefly why a layer was skipped;
// it is empty for a layer switched off on purpose.
type verdict struct {
	outcome  outcome
	reason   string
	resetsAt *time.Time
	why      string
}

func skip(why string) verdict { return verdict{outcome: skipped, why: why} }

// errTimedOut is why a layer that ran out of time was skipped.
var errTimedOut = errors.New("timed out")

// layer is one check that follows the binary check. bin is the resolved binary.
type layer func(ctx context.Context, bin string, env []string) verdict

var healthLayers = []struct {
	name string
	run  layer
}{
	{"login status", loginLayer},
	{"app-server", rateLimitLayer},
	{"status page", statusPageLayer},
}

// CheckHealth reports whether Codex is usable now, without a model call. It
// runs the layers in order and the first one that fails decides; a layer that
// cannot run is passed over and named in Skipped, and when none could run the
// provider is available with the reason unknown.
func (adapter) CheckHealth(ctx context.Context, env []string) provider.Health {
	checkedAt := time.Now().UTC()
	bin, err := lookPath("codex", env)
	if err != nil {
		return provider.Health{Reason: provider.ReasonNotInstalled, CheckedAt: checkedAt}
	}
	var notRun []string
	ran := 0
	for _, l := range healthLayers {
		v := l.run(ctx, bin, env)
		switch v.outcome {
		case failed:
			return provider.Health{Reason: v.reason, CheckedAt: checkedAt, ResetsAt: v.resetsAt, Skipped: notRun}
		case passed:
			ran++
		case skipped:
			if v.why != "" {
				notRun = append(notRun, l.name+": "+v.why)
			}
		}
	}
	health := provider.Health{Available: true, CheckedAt: checkedAt, Skipped: notRun}
	if ran == 0 {
		health.Reason = provider.ReasonUnknown
	}
	return health
}

// loginLayer is `codex login status`: exit 0 means logged in, a non-zero exit
// means not. A run that was cut off or killed by a signal answered nothing.
func loginLayer(ctx context.Context, bin string, env []string) verdict {
	ctx, cancel := context.WithTimeout(ctx, loginStatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "login", "status")
	cmd.Env, cmd.WaitDelay = env, waitDelay
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return verdict{outcome: passed}
	case ctx.Err() != nil:
		return skip(errTimedOut.Error())
	case !errors.As(err, &exit):
		return skip(errCouldNotStart.Error())
	case exit.Exited():
		return verdict{outcome: failed, reason: provider.ReasonNotLoggedIn}
	}
	return skip("killed by a signal")
}

// statusURLEnv names the environment variable that points the status page layer
// at another source, or switches it off with "off".
const statusURLEnv = "MSGATE_STATUS_URL_OPENAI"

// defaultStatusURL is OpenAI's status page.
const defaultStatusURL = "https://status.openai.com/api/v2/summary.json"

// watchedComponents are the status page components whose major outage stops
// Codex. The page lists none for the Codex CLI itself, so these stand in.
var watchedComponents = []string{"Responses", "Codex in ChatGPT Desktop"}

// statusPageLayer fails on a major outage of a watched component; a page that
// cannot be read, or is switched off, says nothing.
func statusPageLayer(ctx context.Context, _ string, env []string) verdict {
	url := lookupEnv(env, statusURLEnv)
	if url == "" {
		url = defaultStatusURL
	}
	ctx, cancel := context.WithTimeout(ctx, statusPageTimeout)
	defer cancel()
	down, err := statuspage.MajorOutage(ctx, url, watchedComponents...)
	switch {
	case errors.Is(err, statuspage.ErrOff):
		return verdict{outcome: skipped}
	case err == nil && down:
		return verdict{outcome: failed, reason: provider.ReasonProviderOutage}
	case err == nil:
		return verdict{outcome: passed}
	case ctx.Err() != nil:
		return skip(errTimedOut.Error())
	case errors.Is(err, statuspage.ErrUnreadable):
		return skip("unreadable answer")
	}
	return skip("unreachable")
}
