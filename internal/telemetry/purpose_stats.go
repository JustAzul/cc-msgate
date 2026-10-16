package telemetry

import (
	"sort"
	"strings"
)

// The sources the engine's own mod writes: its tools, the agent rows it
// answers, and the runs of an interface caller, which carry the caller's name
// after the prefix. Every other source, absent ones included, is a cli run.
const (
	sourceMod          = "mod"
	sourceAgent        = "agent"
	sourcePluginPrefix = "plugin:"
)

const (
	originCLI    = "cli"
	originPlugin = "plugin"
)

// noPurpose names the purpose of a run that recorded none.
const noPurpose = "unknown"

// noOutcome names the outcome of a run that recorded none.
const noOutcome = "no-outcome"

// findingsKeys are the severities summed over the annotated runs.
var findingsKeys = []string{"total", "critical", "high", "medium", "low", "violations"}

// originOf says whether a run was started through the engine's mod or by a
// command line.
func originOf(source string) string {
	if source == sourceMod || source == sourceAgent || strings.HasPrefix(source, sourcePluginPrefix) {
		return originPlugin
	}
	return originCLI
}

// byPurpose counts the runs of each purpose, split by origin: an object keyed
// by purpose in name order, each holding a cli and a plugin object only for the
// origins that have runs of the purpose.
func byPurpose(entries []entry) *Obj {
	groups := map[string]map[string][]entry{}
	for _, e := range entries {
		purpose := e.purpose
		if purpose == "" {
			purpose = noPurpose
		}
		if groups[purpose] == nil {
			groups[purpose] = map[string][]entry{}
		}
		origin := originOf(e.source)
		groups[purpose][origin] = append(groups[purpose][origin], e)
	}
	purposes := make([]string, 0, len(groups))
	for purpose := range groups {
		purposes = append(purposes, purpose)
	}
	sort.Strings(purposes)
	out := &Obj{}
	for _, purpose := range purposes {
		origins := &Obj{}
		for _, origin := range []string{originCLI, originPlugin} {
			if runs := groups[purpose][origin]; len(runs) > 0 {
				origins.Set(origin, purposeBucket(runs))
			}
		}
		out.Set(purpose, origins)
	}
	return out
}

// purposeBucket is the run count, outcome counts and findings of the runs of
// one purpose and origin.
func purposeBucket(runs []entry) *Obj {
	outcomes := newCounter()
	for _, e := range runs {
		if e.outcome == "" {
			outcomes.add(noOutcome)
		} else {
			outcomes.add(e.outcome)
		}
	}
	return obj("runs", int64(len(runs)), "outcomes", outcomes.obj(true), "findings", findingsOf(runs))
}

// findingsOf sums the review.findings annotations of the runs that carry one.
func findingsOf(runs []entry) *Obj {
	var annotated []entry
	for _, e := range runs {
		if e.findings != nil {
			annotated = append(annotated, e)
		}
	}
	if len(annotated) == 0 {
		return obj("annotated", int64(0))
	}
	out := &Obj{}
	for _, key := range findingsKeys {
		var sum int64
		for _, e := range annotated {
			n, _ := asInt(e.findings[key])
			sum += n
		}
		out.Set(key, sum)
	}
	var withFindings int64
	for _, e := range annotated {
		if n, _ := asInt(e.findings["total"]); n > 0 {
			withFindings++
		}
	}
	out.Set("annotated", int64(len(annotated)))
	out.Set("with_findings", withFindings)
	out.Set("clean", int64(len(annotated))-withFindings)
	return out
}
