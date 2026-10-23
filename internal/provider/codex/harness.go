package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

// setting is one Codex config key a harness level sets, with its TOML value.
type setting struct{ key, value string }

// instructionsKey names the config key that replaces Codex's base instructions
// with the file at the path it is given.
const instructionsKey = "model_instructions_file"

// leanSettings turn off the layers Codex adds on its own to the instructions:
// the skills catalog, the collaboration mode, the apps and plugins notes and
// the multi-agent role. The shell and editing tools stay.
var leanSettings = []setting{
	{"skills.include_instructions", "false"},
	{"include_collaboration_mode_instructions", "false"},
	{"include_apps_instructions", "false"},
	{"features.plugins", "false"},
	{"agents.enabled", "false"},
}

// bareSettings turn off what lean keeps: the environment note and the tools.
// Codex has no switch for MCP servers as a whole, so each one the config names
// is switched off on top of these.
var bareSettings = []setting{
	{"include_environment_context", "false"},
	{"features.shell_tool", "false"},
	{"features.apps", "false"},
	{"features.browser_use", "false"},
	{"features.computer_use", "false"},
	{"features.image_generation", "false"},
	{"features.goals", "false"},
	{"features.skill_search", "false"},
	{"features.sleep_tool", "false"},
	{"features.tool_suggest", "false"},
	{"features.hooks", "false"},
	{"tools.view_image", "false"},
	{"web_search", "disabled"},
}

// harnessSettings are the settings a level adds to the plan, in the order they
// are passed.
func harnessSettings(req provider.Request) ([]setting, error) {
	if req.Harness == "" || req.Harness == provider.HarnessFull {
		return nil, nil
	}
	if req.HarnessInstructions == "" {
		return nil, fmt.Errorf("codex: harness %q needs an instructions file", req.Harness)
	}
	out := append([]setting{{instructionsKey, req.HarnessInstructions}}, leanSettings...)
	if req.Harness != provider.HarnessBare {
		return out, nil
	}
	out = append(out, bareSettings...)
	names, err := mcpServerNames(req.Env, req.Cwd)
	if err != nil {
		return nil, err
	}
	if len(names) > 0 {
		out = append(out, setting{"mcp_servers", mcpOffTable(names)})
	}
	return out, nil
}

// mcpListTimeout bounds the `codex mcp list` call.
const mcpListTimeout = 10 * time.Second

// mcpServerNames lists, sorted, the MCP servers Codex would start for a run in
// cwd under env: those of every config layer it reads, which only Codex itself
// resolves (the user config, and the config of a trusted project). Codex has no
// switch for all MCP servers, and an override naming a server it does not know
// fails the run, so the names must be exact.
func mcpServerNames(env []string, cwd string) ([]string, error) {
	bin, err := lookPath("codex", env)
	if err != nil {
		return nil, fmt.Errorf("codex mcp list: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "mcp", "list", "--json")
	cmd.Env, cmd.Dir = env, cwd
	cmd.WaitDelay = time.Second // a grandchild holding the pipe must not outlast the timeout
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("codex mcp list: %w", err)
	}
	var servers []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &servers); err != nil {
		return nil, fmt.Errorf("codex mcp list: output is not a JSON list: %w", err)
	}
	var names []string
	for _, srv := range servers {
		if srv.Name != "" {
			names = append(names, srv.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// mcpOffTable is the TOML inline table that switches every named server off.
// One table override merges into the config whatever the names hold: a -c path
// splits on every dot, so a name with a dot or quote has no path of its own.
func mcpOffTable(names []string) string {
	entries := make([]string, len(names))
	for i, name := range names {
		entries[i] = tomlString(name) + "={enabled=false}"
	}
	return "{" + strings.Join(entries, ",") + "}"
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// configArgs is settings as -c flags.
func configArgs(settings []setting) []string {
	var argv []string
	for _, s := range settings {
		argv = append(argv, "-c", s.key+"="+s.value)
	}
	return argv
}

// levelKeys are the config keys a level sets. Bare also owns the tools and
// mcp_servers tables whole, whichever tool or server a config names.
func levelKeys(level string) []string {
	keys := []string{instructionsKey}
	for _, s := range leanSettings {
		keys = append(keys, s.key)
	}
	if level == provider.HarnessBare {
		for _, s := range bareSettings {
			keys = append(keys, s.key)
		}
		keys = append(keys, "tools", "mcp_servers")
	}
	return keys
}

// keyPath splits a dotted config key into its segments, without the spaces and
// quotes around each.
func keyPath(key string) []string {
	segments := strings.Split(key, ".")
	for i, seg := range segments {
		segments[i] = strings.Trim(strings.TrimSpace(seg), `"'`)
	}
	return segments
}

// related reports whether one path is the other or lies below it. An override
// of a table replaces what is set below it, so it undoes a key as surely as an
// override of the key itself.
func related(a, b []string) bool {
	n := min(len(a), len(b))
	return slices.Equal(a[:n], b[:n])
}

// HarnessReservedFlag reports whether a native token undoes what the level
// sets: a config override of one of its keys or of a table holding one, or a
// --enable/--disable of one of its features. Full sets nothing.
func (adapter) HarnessReservedFlag(level, arg string) bool {
	if level != provider.HarnessLean && level != provider.HarnessBare {
		return false
	}
	keys := levelKeys(level)
	if override, ok := configOverride(arg); ok {
		key, _, _ := strings.Cut(override, "=")
		path := keyPath(key)
		return slices.ContainsFunc(keys, func(k string) bool { return related(path, keyPath(k)) })
	}
	feature, ok := featureFlagValue(arg)
	return ok && slices.Contains(keys, "features."+feature)
}

// featureFlagValue is the feature a "--enable X", "--enable=X", "--disable X"
// or "--disable=X" token names.
func featureFlagValue(arg string) (string, bool) {
	for _, flag := range []string{"--enable", "--disable"} {
		if rest, ok := strings.CutPrefix(arg, flag); ok && rest != "" && (rest[0] == '=' || rest[0] == ' ') {
			return strings.TrimSpace(rest[1:]), true
		}
	}
	return "", false
}
