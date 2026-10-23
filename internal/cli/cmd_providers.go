package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "providers", Summary: "show which providers are available; providers refresh and providers mark change that", Run: runProviders})
}

const (
	providersUsage = "usage: msgate providers [--json]\n       msgate providers refresh\n       msgate providers mark <provider> --reason <reason> [--until <RFC3339>]"
	// refreshCommand is the argument that makes `msgate providers` a refresh.
	refreshCommand = "refresh"
	// detachedFlag marks a refresh started in the background, whose reports go
	// to the refresh log instead of a terminal.
	detachedFlag = "--detached"
)

func runProviders(ctx *Context, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case refreshCommand:
			return runProvidersRefresh(ctx, args[1:])
		case "mark":
			return runProvidersMark(ctx, args[1:])
		}
	}
	return runProvidersShow(ctx, args)
}

// providerView is one provider's state as the command prints it. CheckedAt is
// null for a provider that has not been checked yet.
type providerView struct {
	Provider  string     `json:"provider"`
	Available bool       `json:"available"`
	Reason    string     `json:"reason"`
	CheckedAt *time.Time `json:"checked_at"`
	ResetsAt  *time.Time `json:"resets_at"`
	Source    string     `json:"source"`
	// Skipped names the checks that could not run, so a reader can tell a check
	// that passed from one that passed for lack of answers.
	Skipped []string `json:"skipped,omitempty"`
}

// providersAnswer is what `providers --json` prints.
type providersAnswer struct {
	Providers []providerView `json:"providers"`
	// SummaryModels is the cost ranking, which a later change fills.
	SummaryModels []struct{} `json:"summary_models"`
}

// runProvidersShow answers at once from the cache. A provider with no entry
// reads as available, and any stale or missing entry starts one detached
// refresh; the answer never waits for it.
func runProvidersShow(ctx *Context, args []string) int {
	var asJSON bool
	fset := flag.NewFlagSet("providers", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, providersUsage)
		return ExitOK
	}
	if err != nil {
		return failFlagParse(ctx, providersUsage, err)
	}
	ctx.JSON = asJSON
	if len(positional) != 0 {
		return ctx.Fail(ExitUsage, "providers takes no arguments besides refresh and mark, got %q", positional[0])
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	cache, err := st.ReadProviders()
	if err != nil {
		ctx.Warnf("reading the provider cache: %v", err)
	}
	views, stale := viewProviders(cache, ctx.Now())
	if stale {
		startRefresh(ctx, st)
	}
	if ctx.JSON {
		return printJSON(ctx, providersAnswer{Providers: views, SummaryModels: []struct{}{}})
	}
	for _, v := range views {
		fmt.Fprintln(ctx.Stdout, viewLine(v))
	}
	return ExitOK
}

// viewProviders is the state of every provider, with true when any entry is
// missing or stale.
func viewProviders(cache store.ProviderCache, now time.Time) ([]providerView, bool) {
	var views []providerView
	stale := false
	for _, name := range healthProviders() {
		e, ok := cache.Providers[name]
		if !ok {
			views = append(views, providerView{Provider: name, Available: true, Source: sourceNone})
			stale = true
			continue
		}
		stale = stale || isStale(e, now)
		checked := e.CheckedAt
		views = append(views, providerView{Provider: name, Available: e.Available, Reason: e.Reason, CheckedAt: &checked, ResetsAt: e.ResetsAt, Source: e.Source, Skipped: e.Skipped})
	}
	return views, stale
}

// viewLine is one provider's state as a line of text.
func viewLine(v providerView) string {
	state := "available"
	if !v.Available {
		state = "unavailable"
	}
	line := fmt.Sprintf("%s: %s", v.Provider, state)
	if v.Reason != "" {
		line += " (" + v.Reason + ")"
	}
	if v.ResetsAt != nil {
		line += " until " + v.ResetsAt.UTC().Format(time.RFC3339)
	}
	if v.CheckedAt == nil {
		return line + ", not checked yet"
	}
	return fmt.Sprintf("%s, %s %s", line, v.Source, v.CheckedAt.UTC().Format(time.RFC3339))
}

// startRefresh starts one detached `msgate providers refresh` in its own
// session, like the run worker, with its stderr in the refresh log. A refresh
// that cannot start is a warning: the answer in hand is still good.
func startRefresh(ctx *Context, st *store.Store) {
	exe, err := os.Executable()
	if err != nil {
		ctx.Warnf("starting a provider refresh: %v", err)
		return
	}
	cmd := exec.Command(exe, "providers", refreshCommand, detachedFlag)
	cmd.Env = ctx.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // its own session: the caller's terminal and group never reach it
	if log, err := st.OpenProvidersRefreshLog(); err != nil {
		ctx.Warnf("could not open the provider refresh log: %v", err)
	} else {
		defer log.Close()
		cmd.Stderr = log
	}
	refreshLog(ctx, "spawned")
	if err := cmd.Start(); err != nil {
		ctx.Warnf("starting a provider refresh: %v", err)
		return
	}
	cmd.Process.Release()
}
