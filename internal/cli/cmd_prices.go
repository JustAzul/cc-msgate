package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math/big"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "prices", Summary: "show the price cache; prices refresh updates it", Run: runPrices})
}

// defaultPricesURL is the LiteLLM price list a refresh reads unless
// MSGATE_PRICES_URL names another source or switches the refresh off.
const defaultPricesURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

func runPrices(ctx *Context, args []string) int {
	if len(args) > 0 && args[0] == "refresh" {
		return runPricesRefresh(ctx, args[1:])
	}
	return runPricesShow(ctx, args)
}

// runPricesShow prints the price cache: its fields plus cached: true with
// --json, else the source, the times, each provider's models with their four
// prices and the unpriced models.
func runPricesShow(ctx *Context, args []string) int {
	var asJSON bool
	fset := flag.NewFlagSet("prices", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, "usage: msgate prices [--json]\n       msgate prices refresh [--max-age <duration>] [--json]")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	if len(positional) != 0 {
		return ctx.Fail(ExitUsage, "prices takes no arguments besides refresh, got %q", positional[0])
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	cache := readPriceCache(ctx, store.Open(home))
	if cache == nil {
		if ctx.JSON {
			return printJSON(ctx, map[string]any{"cached": false})
		}
		fmt.Fprintln(ctx.Stdout, "no price cache: run msgate prices refresh")
		return ExitOK
	}
	if ctx.JSON {
		return printCacheJSON(ctx, cache)
	}
	return printCacheText(ctx, cache)
}

// printCacheJSON prints the cache file's fields plus cached: true.
func printCacheJSON(ctx *Context, cache *prices.Cache) int {
	data, err := cache.Encode()
	if err != nil {
		return ctx.Fail(ExitInternal, "encoding the price cache: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ctx.Fail(ExitInternal, "encoding the price cache: %v", err)
	}
	fields["cached"] = json.RawMessage("true")
	return printJSON(ctx, fields)
}

func printCacheText(ctx *Context, cache *prices.Cache) int {
	w := ctx.Stdout
	fmt.Fprintf(w, "source: %s\n", cache.Source)
	fmt.Fprintf(w, "fetched_at: %s\n", cache.FetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "checked_at: %s\n", cache.CheckedAt.UTC().Format(time.RFC3339))
	fmt.Fprintln(w, "prices in US dollars per million tokens")
	for _, provider := range slices.Sorted(maps.Keys(cache.Models)) {
		fmt.Fprintf(w, "%s:\n", provider)
		for _, model := range slices.Sorted(maps.Keys(cache.Models[provider])) {
			p := cache.Models[provider][model]
			fmt.Fprintf(w, "  %s input %s cached_input %s cache_write %s output %s\n", model,
				decimalText(p.Input), decimalText(p.CachedInput), decimalText(p.CacheWrite), decimalText(p.Output))
		}
	}
	if len(cache.Unpriced) > 0 {
		fmt.Fprintln(w, "unpriced:")
		for _, provider := range slices.Sorted(maps.Keys(cache.Unpriced)) {
			for _, model := range slices.Sorted(slices.Values(cache.Unpriced[provider])) {
				fmt.Fprintf(w, "  %s/%s\n", provider, model)
			}
		}
	}
	return ExitOK
}

// decimalText is a cached price as its shortest exact decimal.
func decimalText(r *big.Rat) string {
	s, err := prices.FormatDecimal(r)
	if err != nil {
		return r.RatString()
	}
	return s
}

// refreshReport is what a refresh prints with --json.
type refreshReport struct {
	SDKStatus    string   `json:"sdk_status"`
	ExitCode     int      `json:"exit_code"`
	Ran          bool     `json:"ran"`
	Changed      bool     `json:"changed"`
	Reason       string   `json:"reason"`
	Source       string   `json:"source"`
	CheckedAt    *string  `json:"checked_at"`
	Priced       []string `json:"priced"`
	Unpriced     []string `json:"unpriced"`
	CatalogError *string  `json:"catalog_error"`
	Error        string   `json:"error,omitempty"`
}

// maxAge is the --max-age flag: a positive duration.
type maxAge struct {
	d     *time.Duration
	given *bool
}

func (f maxAge) String() string { return "" }

func (f maxAge) Set(raw string) error {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fmt.Errorf("--max-age needs a positive duration such as 24h or 90m, got %q", raw)
	}
	*f.d, *f.given = d, true
	return nil
}

func runPricesRefresh(ctx *Context, args []string) int {
	var asJSON, ageGiven bool
	var age time.Duration
	fset := flag.NewFlagSet("prices refresh", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.Var(maxAge{&age, &ageGiven}, "max-age", "skip the refresh when the cache was checked more recently than this")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, "usage: msgate prices refresh [--max-age <duration>] [--json]")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	if len(positional) != 0 {
		return ctx.Fail(ExitUsage, "prices refresh takes no arguments, got %q", positional[0])
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)

	url := ctx.Getenv("MSGATE_PRICES_URL")
	if url == "" {
		url = defaultPricesURL
	}
	rep := refreshReport{SDKStatus: sdkStatusFor(ExitOK), Source: url}
	if url == "off" {
		rep.Reason = "off"
		return printRefresh(ctx, rep, readPriceCache(ctx, st))
	}
	release, err := st.LockPrices()
	if errors.Is(err, store.ErrPricesLockHeld) {
		rep.Reason = "busy"
		return printRefresh(ctx, rep, readPriceCache(ctx, st))
	}
	if err != nil {
		return ctx.Fail(ExitInternal, "locking the price cache: %v", err)
	}
	defer release()

	cache := readPriceCache(ctx, st)
	if ageGiven && cache != nil && cache.CheckedAt.Before(ctx.Now()) && ctx.Now().Sub(cache.CheckedAt) < age {
		rep.Reason = "fresh"
		return printRefresh(ctx, rep, cache)
	}
	wanted, namespaces, catalogErr, err := wantedModels(ctx, home)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading telemetry: %v", err)
	}
	if catalogErr != "" {
		rep.CatalogError = &catalogErr
	}
	if len(wanted) == 0 {
		rep.Reason = "nothing_wanted"
		return printRefresh(ctx, rep, cache)
	}
	return fetchPrices(ctx, refreshInput{st: st, url: url, cache: cache, wanted: wanted, namespaces: namespaces}, rep)
}

// refreshInput is what a refresh that goes to the source works from: the
// store, the source URL, the cache as read, and the wanted models per provider
// with each provider's price-list namespace.
type refreshInput struct {
	st         *store.Store
	url        string
	cache      *prices.Cache
	wanted     map[string][]string
	namespaces map[string]string
}

// fetchPrices asks the source for the wanted models' prices and stores what it
// answers. A 304 only advances checked_at; a 200 replaces the cache, as long
// as it prices at least one wanted model.
func fetchPrices(ctx *Context, in refreshInput, rep refreshReport) int {
	st, url, cache, wanted, namespaces := in.st, in.url, in.cache, in.wanted, in.namespaces
	// The cached entity tag vouches only for the same source and for a cache
	// that already holds a verdict on every wanted model.
	etag := ""
	if cache != nil && cache.Source == url && cache.Covers(wanted) {
		etag = cache.ETag
	}
	res, err := prices.Fetch(url, etag, fetchOptions(ctx))
	if err != nil {
		return unavailable(ctx, rep, cache, err)
	}
	now := ctx.Now().UTC().Truncate(time.Second)
	next := cache
	rep.Ran, rep.Reason = true, "not_modified"
	if res.NotModified {
		next.CheckedAt = now
	} else {
		models, unpriced, err := prices.ParseList(res.Body, wanted, namespaces)
		if err != nil {
			return unavailable(ctx, rep, cache, err)
		}
		if len(models) == 0 {
			return unavailable(ctx, rep, cache, errors.New("price list: it prices none of the wanted models"))
		}
		next = &prices.Cache{Source: url, ETag: res.ETag, FetchedAt: now, CheckedAt: now, Models: models, Unpriced: unpriced}
		rep.Reason, rep.Changed = "updated", cache == nil || !sameVerdicts(cache, next)
	}
	data, err := next.Encode()
	if err != nil {
		return ctx.Fail(ExitInternal, "encoding the price cache: %v", err)
	}
	if err := st.WritePrices(data); err != nil {
		return ctx.Fail(ExitInternal, "writing the price cache: %v", err)
	}
	return printRefresh(ctx, rep, next)
}

// unavailable reports a refresh that could not get usable prices: the cache is
// left as it was, the report still describes it, and the error goes to stderr
// in both modes. Without --json nothing is printed on stdout.
func unavailable(ctx *Context, rep refreshReport, cache *prices.Cache, cause error) int {
	rep.SDKStatus, rep.ExitCode = sdkStatusFor(ExitPricesUnavailable), ExitPricesUnavailable
	rep.Ran, rep.Changed, rep.Reason, rep.Error = true, false, "unavailable", cause.Error()
	rep = describePriceCache(rep, cache)
	if ctx.JSON {
		if printJSON(ctx, rep) != 0 {
			return ExitInternal
		}
	}
	ctx.Errorf("%s", rep.Error)
	return ExitPricesUnavailable
}

// sameVerdicts reports whether two caches hold the same priced and unpriced
// models at the same prices.
func sameVerdicts(a, b *prices.Cache) bool {
	if len(a.Models) != len(b.Models) || len(a.Unpriced) != len(b.Unpriced) {
		return false
	}
	for provider, models := range a.Models {
		if len(models) != len(b.Models[provider]) {
			return false
		}
		for model, p := range models {
			q, ok := b.Models[provider][model]
			if !ok || p.Input.Cmp(q.Input) != 0 || p.CachedInput.Cmp(q.CachedInput) != 0 ||
				p.CacheWrite.Cmp(q.CacheWrite) != 0 || p.Output.Cmp(q.Output) != 0 {
				return false
			}
		}
	}
	for provider, models := range a.Unpriced {
		if !slices.Equal(slices.Sorted(slices.Values(models)), slices.Sorted(slices.Values(b.Unpriced[provider]))) {
			return false
		}
	}
	return true
}

// fetchOptions are the refresh's request bounds, shortened by the test seams.
func fetchOptions(ctx *Context) prices.FetchOptions {
	var opts prices.FetchOptions
	if d, ok := envMillis(ctx.Getenv, "MSGATE_TEST_PRICES_TIMEOUT_MS"); ok {
		opts.Timeout = d
	}
	if n, err := strconv.ParseInt(ctx.Getenv("MSGATE_TEST_PRICES_MAX_BYTES"), 10, 64); err == nil && n > 0 {
		opts.MaxBytes = n
	}
	return opts
}

// readPriceCache returns the usable price cache, or nil: an absent, unreadable
// or unparseable file is no cache. A file that exists but cannot be read is
// reported with one warning.
func readPriceCache(ctx *Context, st *store.Store) *prices.Cache {
	data, err := st.ReadPrices()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			ctx.Warnf("reading the price cache: %v", err)
		}
		return nil
	}
	cache, err := prices.Decode(data)
	if err != nil {
		return nil
	}
	return cache
}

// wantedModels is what a refresh prices, per provider: the models of each
// provider that declares a catalog plus every model its runs used anywhere in
// the telemetry, and the telemetry models of the providers without one. The
// models of the model calls in the telemetry are wanted too, under their
// provider. namespaces maps each provider with a catalog, and each model-call
// provider, to its price-list namespace.
// An unreadable catalog leaves the telemetry models and is reported in
// catalogErr.
func wantedModels(ctx *Context, home string) (wanted map[string][]string, namespaces map[string]string, catalogErr string, err error) {
	folded, err := telemetry.Fold(home, nil)
	if err != nil {
		return nil, nil, "", err
	}
	wanted, namespaces = map[string][]string{}, map[string]string{}
	for _, run := range folded.Runs {
		name, _ := run["provider"].(string)
		model := modelOfRun(run)
		if name != "" && model != "" && model != "unknown" {
			wanted[name] = append(wanted[name], model)
		}
	}
	for _, call := range folded.ModelCalls {
		name, _ := call["provider"].(string)
		model, _ := call["model"].(string)
		if name != "" && model != "" && model != "unknown" {
			wanted[name] = append(wanted[name], model)
		}
	}
	for name, namespace := range modelCallProviders {
		namespaces[name] = namespace
	}
	var failures []string
	for _, name := range provider.Names() {
		p, _ := provider.Get(name)
		catalog, ok := p.(provider.ModelCatalog)
		if !ok {
			continue
		}
		namespaces[name] = catalog.PriceNamespace()
		models, err := catalog.CatalogModels(ctx.Env)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if len(models) > 0 {
			wanted[name] = append(wanted[name], models...)
		}
	}
	for name, models := range wanted {
		slices.Sort(models)
		wanted[name] = slices.Compact(models)
	}
	return wanted, namespaces, strings.Join(failures, "; "), nil
}

// modelOfRun is the model a run used: what the provider reported, else what
// was asked for, else "".
func modelOfRun(run map[string]any) string {
	for _, key := range []string{"model_used", "model"} {
		if m, ok := run[key].(string); ok && m != "" {
			return m
		}
	}
	return ""
}

// printRefresh prints a refresh that did not fail, describing the cache as it
// stands: one JSON object with --json, else one line.
func printRefresh(ctx *Context, rep refreshReport, cache *prices.Cache) int {
	rep = describePriceCache(rep, cache)
	if ctx.JSON {
		return printJSON(ctx, rep)
	}
	fmt.Fprintf(ctx.Stdout, "prices: %s (%d priced, %d unpriced)\n", rep.Reason, len(rep.Priced), len(rep.Unpriced))
	return ExitOK
}

// describePriceCache returns rep with the fields that describe the cache set;
// both lists are sorted provider/model names and never nil.
func describePriceCache(rep refreshReport, cache *prices.Cache) refreshReport {
	rep.Priced, rep.Unpriced, rep.CheckedAt = []string{}, []string{}, nil
	if cache == nil {
		return rep
	}
	for provider, models := range cache.Models {
		for model := range models {
			rep.Priced = append(rep.Priced, provider+"/"+model)
		}
	}
	for provider, models := range cache.Unpriced {
		for _, model := range models {
			rep.Unpriced = append(rep.Unpriced, provider+"/"+model)
		}
	}
	slices.Sort(rep.Priced)
	slices.Sort(rep.Unpriced)
	at := cache.CheckedAt.UTC().Format(time.RFC3339)
	rep.CheckedAt = &at
	return rep
}
