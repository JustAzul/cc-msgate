package cli

import (
	"flag"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/settings"
	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "review", Summary: "run a provider review of a branch diff, the working tree or a commit", Run: runReview})
}

func runReview(ctx *Context, args []string) int {
	var base, commit string
	var uncommitted bool
	spec := turnSpec{
		name:  "review",
		usage: "[flags] (--base <branch> | --uncommitted | --commit <sha>) [-- native flags]",
		extra: func(fs *flag.FlagSet) {
			fs.StringVar(&base, "base", "", "review the changes against this branch")
			fs.BoolVar(&uncommitted, "uncommitted", false, "review the uncommitted changes")
			fs.StringVar(&commit, "commit", "", "review this commit")
		},
	}
	p, exit, done := parseTurnArgs(ctx, spec, args)
	if done {
		return exit
	}

	prov, ok := provider.Get(p.provider)
	if !ok {
		return ctx.Fail(ExitUsage, "unknown provider %q (known: %v)", p.provider, provider.Names())
	}
	target, ref, ok := reviewTarget(base, uncommitted, commit)
	if !ok {
		return ctx.Fail(ExitUsage, "exactly one review target is required: --base <branch>, --uncommitted or --commit <sha>")
	}
	if len(p.positional) > 0 || p.promptFile != "" {
		return ctx.Fail(ExitUsage, "review targets take no prompt: pass a target flag and no prompt source")
	}
	eff := settings.Resolve(settings.Flags{Model: p.model, Effort: p.effort, Sandbox: p.sandbox, EffortSource: p.effortSource}, p.settingsSource)
	if code := checkedReview(ctx, prov, p, eff, target); code != 0 {
		return code
	}

	cwd, code := resolveCwd(ctx, p.cwd, p.dryRun)
	if code != 0 {
		return code
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	return runTurn(ctx, turn{
		command: "review", flags: p, prov: prov, eff: eff, cwd: cwd, st: store.Open(home),
		req: provider.Request{
			Command: "review", Cwd: cwd, Model: eff.Model, Effort: eff.Effort, Sandbox: eff.Sandbox,
			Passthrough: p.passthrough, ReviewTarget: target, ReviewRef: ref, Harness: p.harness,
		},
	})
}

// checkedReview refuses a review the provider cannot run with the effective
// settings, the harness level and the native flags. A review reads the tree
// with the provider's tools, so it never runs at the bare level.
func checkedReview(ctx *Context, prov provider.Provider, p parsedTurn, eff settings.Resolved, target string) int {
	if p.harness == provider.HarnessBare {
		return ctx.Fail(ExitUsage, "a review cannot run at the %s harness level: it needs the provider's tools", provider.HarnessBare)
	}
	if err := prov.Capabilities().Unsupported(provider.Needs{Command: "review", Sandbox: eff.Sandbox, ReviewTarget: target, Harness: p.harness}); err != nil {
		return ctx.Fail(ExitUsage, "provider %q %v", p.provider, err)
	}
	if flagText, reserved := reservedPassthrough(prov, p.harness, p.passthrough); reserved {
		return ctx.Fail(ExitUsage, "native flag %q is reserved by msgate and cannot be passed after --", flagText)
	}
	return 0
}

// reviewTarget returns the one target the caller chose; ok is false when none
// or more than one was given.
func reviewTarget(base string, uncommitted bool, commit string) (target, ref string, ok bool) {
	n := 0
	if base != "" {
		target, ref = "base", base
		n++
	}
	if uncommitted {
		target, ref = "uncommitted", ""
		n++
	}
	if commit != "" {
		target, ref = "commit", commit
		n++
	}
	return target, ref, n == 1
}
