package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

func init() {
	register(Command{Name: "models", Summary: "print the models a provider offers", Run: runModels, NoHome: true})
}

// modelsResult is what `models --json` prints: the catalog as the provider
// gives it, unfiltered and in its order.
type modelsResult struct {
	Provider string                  `json:"provider"`
	Models   []provider.CatalogEntry `json:"models"`
}

const modelsUsage = "usage: msgate models [--provider <name>] [--json]"

func runModels(ctx *Context, args []string) int {
	var asJSON bool
	name := "codex"
	fset := flag.NewFlagSet("models", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.StringVar(&name, "provider", name, "provider whose models to list")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if err != nil {
		return failFlagParse(ctx, modelsUsage, err)
	}
	ctx.JSON = asJSON
	if len(positional) != 0 {
		return ctx.Fail(ExitUsage, modelsUsage)
	}

	prov, ok := provider.Get(name)
	if !ok {
		return ctx.Fail(ExitUsage, "unknown provider %q (known: %v)", name, provider.Names())
	}
	catalog, ok := prov.(provider.ModelCatalog)
	if !ok {
		return ctx.Fail(ExitUsage, "provider %q has no model catalog", name)
	}
	entries, err := catalog.CatalogEntries(ctx.Env)
	if errors.Is(err, provider.ErrBinaryMissing) {
		return ctx.Fail(ExitProviderMissing, "%v", err)
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	if asJSON {
		if printJSON(ctx, modelsResult{Provider: name, Models: entries}) != 0 {
			return ExitInternal
		}
		return ExitOK
	}
	for _, e := range entries {
		fmt.Fprintf(ctx.Stdout, "%s\t%s\t%s\n", e.ID, e.Name, e.Visibility)
	}
	return ExitOK
}
