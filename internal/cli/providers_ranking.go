package cli

import (
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/summaryrank"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

// rankSummaryModels ranks the models by the cost of one summary call; ok is
// false when there is no usable price table, in which case the caller keeps
// the ranking it already holds. What else it cannot read it does without and
// warns about, so a refresh never fails for the ranking: unreadable telemetry
// leaves every cost estimated, and a catalog that cannot be read leaves that
// provider's models out.
func rankSummaryModels(ctx *Context, st *store.Store, home string, now time.Time) (models []store.SummaryModel, ok bool) {
	cache := readPriceCache(ctx, st)
	if cache == nil {
		ctx.Warnf("no usable price table, so the summary ranking is left as it was")
		return nil, false
	}
	ranked := summaryrank.Rank(summaryrank.Inputs{
		Prices:              cache,
		Catalog:             catalogModels(ctx),
		Calls:               summaryCalls(ctx, home, now),
		Now:                 now,
		ImplicitCacheWrites: provider.HasImplicitCacheWrites,
	})
	models = make([]store.SummaryModel, len(ranked))
	for i, r := range ranked {
		models[i] = store.SummaryModel{Provider: r.Provider, Model: r.Model, CostUSD: r.CostUSD, Basis: r.Basis, Calls: r.Calls}
	}
	return models, true
}

// summaryCalls are the model calls of the ranking's window; telemetry that
// cannot be read gives none.
func summaryCalls(ctx *Context, home string, now time.Time) []map[string]any {
	since := now.Add(-summaryrank.Window)
	folded, err := telemetry.Fold(home, &since)
	if err != nil {
		ctx.Warnf("reading telemetry for the summary ranking: %v", err)
		return nil
	}
	return folded.ModelCalls
}

// catalogModels lists the models of each registered provider that has a
// catalog. A catalog that cannot be read is a warning, and leaves its
// provider out.
func catalogModels(ctx *Context) map[string][]string {
	catalogs := map[string][]string{}
	for _, name := range provider.Names() {
		p, _ := provider.Get(name)
		catalog, ok := p.(provider.ModelCatalog)
		if !ok {
			continue
		}
		models, err := catalog.CatalogModels(ctx.Env)
		if err != nil {
			ctx.Warnf("reading the %s model catalog for the summary ranking: %v", name, err)
			continue
		}
		catalogs[name] = models
	}
	return catalogs
}
