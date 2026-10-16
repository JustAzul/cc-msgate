package telemetry

import "math"

// ETA is the estimated duration of a run of one purpose.
type ETA struct {
	Repo    string
	Basis   string
	MS      *int64
	Samples int
	Repos   int
}

// durations is the sum and count of the durations of one repo's runs.
type durations struct {
	sum float64
	n   int
}

func (d durations) mean() float64 { return d.sum / float64(d.n) }

// EstimateETA estimates how long a run of purpose started in cwd takes.
func EstimateETA(runs []map[string]any, purpose, cwd string, repoKey func(dir string) string) ETA {
	resolved := map[string]string{}
	keyOf := func(dir string) string {
		key, ok := resolved[dir]
		if !ok {
			key = repoKey(dir)
			resolved[dir] = key
		}
		return key
	}
	target := keyOf(cwd)
	byRepo := map[string]*durations{}
	samples := 0
	for _, run := range runs {
		if name, _ := run["purpose"].(string); name != purpose {
			continue
		}
		dir, _ := run["cwd"].(string)
		d, hasDuration := asInt(run["duration_ms"])
		if dir == "" || !hasDuration {
			continue
		}
		key := keyOf(dir)
		if byRepo[key] == nil {
			byRepo[key] = &durations{}
		}
		byRepo[key].sum += float64(d)
		byRepo[key].n++
		samples++
	}
	if samples == 0 {
		return ETA{Repo: target, Basis: "none"}
	}
	if d, ok := byRepo[target]; ok {
		ms := int64(math.Round(d.mean()))
		return ETA{Repo: target, Basis: "repo", MS: &ms, Samples: d.n, Repos: 1}
	}
	var sumOfMeans float64
	for _, d := range byRepo {
		sumOfMeans += d.mean()
	}
	ms := int64(math.Round(sumOfMeans / float64(len(byRepo))))
	return ETA{Repo: target, Basis: "global", MS: &ms, Samples: samples, Repos: len(byRepo)}
}
