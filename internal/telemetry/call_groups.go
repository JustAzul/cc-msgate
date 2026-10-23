package telemetry

import "time"

// CallGroup names the model calls that share a source, a provider and a model.
type CallGroup struct{ Source, Provider, Model string }

// CallUsage is the token counts of one model call.
type CallUsage struct{ Input, Cached, CacheWrite, Output int64 }

// GroupModelCalls groups the usage of model calls, in the order given, by
// source, provider and model. A call is left out when its ts is unreadable or
// before since, or when it carries no usage object.
func GroupModelCalls(calls []map[string]any, since time.Time) map[CallGroup][]CallUsage {
	groups := map[CallGroup][]CallUsage{}
	for _, call := range calls {
		ts, _ := call["ts"].(string)
		at, ok := ParseTS(ts)
		usage, hasUsage := call["usage"].(map[string]any)
		if !ok || at.Before(since) || !hasUsage {
			continue
		}
		group := CallGroup{Source: stringField(call, "source"), Provider: stringField(call, "provider"), Model: stringField(call, "model")}
		groups[group] = append(groups[group], callUsageOf(usage))
	}
	return groups
}

func stringField(record map[string]any, key string) string {
	s, _ := record[key].(string)
	return s
}

func callUsageOf(usage map[string]any) CallUsage {
	count := func(key string) int64 {
		n, _ := asInt(usage[key])
		return n
	}
	return CallUsage{Input: count("input_tokens"), Cached: count("cached_input_tokens"), CacheWrite: count("cache_write_input_tokens"), Output: count("output_tokens")}
}
