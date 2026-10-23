// Package codex adapts the Codex CLI: plan building and event parsing.
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JustAzul/cc-msgate/internal/provider"
)

func init() { provider.Register(New()) }

type adapter struct{}

// New returns the Codex adapter.
func New() provider.Provider { return adapter{} }

func (adapter) Name() string { return "codex" }

// ImplicitCacheWrites is true: Codex caches prompts on its own and reports no
// cache writes.
func (adapter) ImplicitCacheWrites() bool { return true }

func (adapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Commands:      []string{"exec", "review", "resume"},
		ReviewTargets: []string{"base", "uncommitted", "commit"},
		Sandboxes:     []string{"read-only", "workspace-write"},
		ReportsUsage:  true,
		Harnesses:     []string{provider.HarnessFull, provider.HarnessLean, provider.HarnessBare},
	}
}

// ReservedFlag reports whether a native passthrough token is one the SDK owns.
// The CLI passes each token alone and each token joined by a space to
// its successor, so a "-c key=value" pair arrives as one string.
func (adapter) ReservedFlag(arg string) bool {
	if value, ok := configOverride(arg); ok {
		return reservedConfigKey(value)
	}
	name := arg
	if strings.HasPrefix(arg, "--") {
		name, _, _ = strings.Cut(arg, "=")
	}
	switch name {
	case "-o", "--output-last-message", "--json", "-C", "--cd", "-m", "--model",
		"-s", "--sandbox", "-p", "--profile", "--approve-for-me", "--ephemeral", "--yolo":
		return true
	}
	if strings.HasPrefix(name, "--dangerously-") {
		return true
	}
	// Short flags with an attached value: -mfoo, -s=read-only, -C/x, -ofile.
	if !strings.HasPrefix(arg, "--") && len(arg) > 2 {
		switch arg[:2] {
		case "-o", "-C", "-m", "-s", "-p":
			return true
		}
	}
	return false
}

// configOverride extracts the "key=value" of a -c / --config override written
// as "-c k=v", "-ck=v", "-c=k=v", "--config k=v" or "--config=k=v". A bare
// "-c" or "--config" carries no value and is not an override on its own.
func configOverride(arg string) (string, bool) {
	rest := ""
	switch {
	case strings.HasPrefix(arg, "--config"):
		rest = strings.TrimPrefix(arg, "--config")
		if rest == "" || (rest[0] != '=' && rest[0] != ' ') {
			return "", false
		}
	case strings.HasPrefix(arg, "-c") && !strings.HasPrefix(arg, "--"):
		rest = strings.TrimPrefix(arg, "-c")
		if rest == "" {
			return "", false
		}
	default:
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, "=")), true
}

// reservedConfigKey is true for the config keys the SDK controls through its
// own flags or that select a provider config profile (which can itself set
// the model and sandbox): model, model_reasoning_effort, profile and any
// sandbox* key.
func reservedConfigKey(override string) bool {
	key, _, _ := strings.Cut(override, "=")
	key = strings.TrimSpace(key)
	return key == "model" || key == "model_reasoning_effort" || key == "profile" || strings.HasPrefix(key, "sandbox")
}

func (adapter) VersionArgs() []string { return []string{"--version"} }

func (adapter) BuildPlan(req provider.Request) (provider.Plan, error) {
	tail, stdin, err := planTail(req)
	if err != nil {
		return provider.Plan{}, err
	}
	levelSettings, err := harnessSettings(req)
	if err != nil {
		return provider.Plan{}, err
	}
	argv := []string{"codex", "exec", "-C", req.Cwd}
	if req.Sandbox != "" {
		argv = append(argv, "-s", req.Sandbox)
	}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	if req.Effort != "" {
		argv = append(argv, "-c", "model_reasoning_effort="+req.Effort)
	}
	argv = append(argv, configArgs(levelSettings)...)
	argv = append(argv, "--json", "-o", req.OutputPath)
	argv = append(argv, sessionFlags(req)...)
	argv = append(argv, req.Passthrough...)
	argv = append(argv, tail...)
	return provider.Plan{
		Argv:      argv,
		Stdin:     stdin,
		Dir:       req.Cwd,
		EnvAdd:    map[string]string{},
		EnvRemove: []string{},
	}, nil
}

// sessionFlags are the flags that say where the turn runs and whether it keeps
// a session: an ephemeral turn keeps none and skips the repository check
// wherever it runs, any other skips it outside a git work tree.
func sessionFlags(req provider.Request) []string {
	var flags []string
	if req.Ephemeral {
		flags = append(flags, "--ephemeral")
	}
	if req.Ephemeral || !insideGitWorkTree(req.Cwd) {
		flags = append(flags, "--skip-git-repo-check")
	}
	return flags
}

// planTail is the part of the argv that follows the shared flags, and what
// the provider receives on standard input. A review takes no prompt, so its
// standard input is empty.
func planTail(req provider.Request) ([]string, provider.StdinSource, error) {
	switch req.Command {
	case "exec":
		return []string{"-"}, provider.StdinPrompt, nil
	case "resume":
		if req.SessionID == "" {
			return nil, "", fmt.Errorf("codex: resume needs a provider session id")
		}
		return []string{"resume", req.SessionID, "-"}, provider.StdinPrompt, nil
	case "review":
		switch req.ReviewTarget {
		case "uncommitted":
			return []string{"review", "--uncommitted"}, provider.StdinEmpty, nil
		case "base", "commit":
			if req.ReviewRef == "" {
				return nil, "", fmt.Errorf("codex: review --%s needs a value", req.ReviewTarget)
			}
			return []string{"review", "--" + req.ReviewTarget, req.ReviewRef}, provider.StdinEmpty, nil
		}
		return nil, "", fmt.Errorf("codex: review needs a target (base, uncommitted or commit), got %q", req.ReviewTarget)
	}
	return nil, "", fmt.Errorf("codex: unknown command %q", req.Command)
}

// insideGitWorkTree walks up from dir looking for a .git entry (a directory,
// or a file for worktrees and submodules).
func insideGitWorkTree(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

type rawEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Usage    *struct {
		InputTokens           int64 `json:"input_tokens"`
		CachedInputTokens     int64 `json:"cached_input_tokens"`
		CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
		ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Item *struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Command string `json:"command"`
	} `json:"item"`
}

func (adapter) ParseEvent(line []byte) (provider.Event, bool) {
	var raw rawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return provider.Event{}, false
	}
	switch raw.Type {
	case "thread.started":
		return provider.Event{SessionID: raw.ThreadID}, true
	case "turn.completed":
		if raw.Usage == nil {
			return provider.Event{}, true
		}
		u := provider.Usage(*raw.Usage)
		return provider.Event{Usage: &u}, true
	case "error":
		return provider.Event{ErrorMsg: unwrapMessage(raw.Message)}, true
	case "turn.failed":
		if raw.Error == nil {
			return provider.Event{}, true
		}
		return provider.Event{ErrorMsg: unwrapMessage(raw.Error.Message)}, true
	case "item.started", "item.completed":
		return provider.Event{Progress: itemProgress(raw)}, true
	}
	return provider.Event{}, true
}

// itemProgress reports a command as it starts and text as it completes; every
// other item, and the other half of each, shows nothing new.
func itemProgress(raw rawEvent) *provider.Progress {
	if raw.Item == nil {
		return nil
	}
	switch {
	case raw.Type == "item.started" && raw.Item.Type == "command_execution" && raw.Item.Command != "":
		return &provider.Progress{Kind: "command", Text: shellCommand(raw.Item.Command)}
	case raw.Type == "item.completed" && raw.Item.Type == "agent_message" && raw.Item.Text != "":
		return &provider.Progress{Kind: "message", Text: raw.Item.Text}
	case raw.Type == "item.completed" && raw.Item.Type == "reasoning" && raw.Item.Text != "":
		return &provider.Progress{Kind: "reasoning", Text: raw.Item.Text}
	}
	return nil
}

// shellWrapper matches how codex runs a command: `<shell> -lc <command>`, the
// command quoted when it holds spaces.
var shellWrapper = regexp.MustCompile(`^\S*sh -lc (.+)$`)

// shellCommand is the command codex ran, without the shell that ran it.
func shellCommand(command string) string {
	m := shellWrapper.FindStringSubmatch(command)
	if m == nil {
		return command
	}
	inner := m[1]
	if len(inner) >= 2 && inner[0] == '\'' && inner[len(inner)-1] == '\'' {
		return inner[1 : len(inner)-1]
	}
	return inner
}

// unwrapMessage returns .error.message when msg is itself a JSON document
// carrying one, else msg unchanged.
func unwrapMessage(msg string) string {
	var inner struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(msg), &inner) == nil && inner.Error != nil && inner.Error.Message != "" {
		return inner.Error.Message
	}
	return msg
}
