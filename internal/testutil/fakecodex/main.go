// Command fakecodex is the fake provider used by tests. Its behaviour is
// driven by FAKECODEX_* environment variables; see docs/FRAME.md.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "__sleeper" || os.Args[1] == "__holder") {
		// Grandchild started by FAKECODEX_SPAWN_CHILD or FAKECODEX_PIPE_HOLDER:
		// sleeps, then goes away. FAKECODEX_CHILD_IGNORE_TERM=1 makes it ignore SIGTERM.
		if os.Getenv("FAKECODEX_CHILD_IGNORE_TERM") == "1" {
			signal.Ignore(syscall.SIGTERM)
		}
		time.Sleep(60 * time.Second)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(os.Getenv("FAKECODEX_VERSION"))
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "debug" && os.Args[2] == "models" {
		os.Exit(debugModels())
	}
	os.Exit(dispatch())
}

// dispatch runs the subcommands that take no prompt, else a turn.
func dispatch() int {
	if len(os.Args) == 4 && os.Args[1] == "mcp" && os.Args[2] == "list" && os.Args[3] == "--json" {
		return mcpList()
	}
	if len(os.Args) == 3 && os.Args[1] == "login" && os.Args[2] == "status" {
		return loginStatus()
	}
	if len(os.Args) == 2 && os.Args[1] == "app-server" {
		return appServer()
	}
	return run()
}

// loginStatus is `codex login status`: after FAKECODEX_SLEEP_MS it exits with
// the code FAKECODEX_LOGIN_EXIT names, 0 when unset. It never reads stdin.
func loginStatus() int {
	sleepFor("FAKECODEX_SLEEP_MS")
	code, _ := strconv.Atoi(os.Getenv("FAKECODEX_LOGIN_EXIT"))
	if code == 0 {
		fmt.Println("Logged in using ChatGPT")
	} else {
		fmt.Fprintln(os.Stderr, "Not logged in")
	}
	return code
}

func run() int {
	// Drain stdin first so the caller never blocks writing the prompt.
	stdin, _ := io.ReadAll(os.Stdin)

	if path := os.Getenv("FAKECODEX_RECORD"); path != "" {
		record(path, stdin)
	}
	if os.Getenv("FAKECODEX_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if os.Getenv("FAKECODEX_SPAWN_CHILD") == "1" {
		spawnChild()
	}
	if path := os.Getenv("FAKECODEX_PIPE_HOLDER"); path != "" {
		spawnPipeHolder(path)
	}

	sleepFor("FAKECODEX_DELAY_MS") // before any event, so the session is announced late

	var fixture []byte
	if f := os.Getenv("FAKECODEX_FIXTURE"); f != "" {
		var err error
		if fixture, err = os.ReadFile(f); err != nil {
			fmt.Fprintf(os.Stderr, "fakecodex: %v\n", err)
			return 99
		}
		os.Stdout.Write(fixture)
	}

	if out := outputArg(os.Args[1:]); out != "" {
		text, ok := os.LookupEnv("FAKECODEX_OUTPUT")
		if !ok {
			text, ok = lastAgentMessage(fixture)
		}
		if ok {
			if err := os.WriteFile(out, []byte(text), 0o600); err != nil {
				fmt.Fprintf(os.Stderr, "fakecodex: %v\n", err)
				return 99
			}
		}
	}

	if s := os.Getenv("FAKECODEX_STDERR"); s != "" {
		fmt.Fprint(os.Stderr, s)
	}
	sleepFor("FAKECODEX_SLEEP_MS")
	code, _ := strconv.Atoi(os.Getenv("FAKECODEX_EXIT"))
	return code
}

// sleepFor sleeps for the milliseconds the environment variable names, if any.
func sleepFor(env string) {
	if ms, _ := strconv.Atoi(os.Getenv(env)); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// debugModels is `codex debug models`: it prints the file FAKECODEX_MODELS
// names, verbatim, or fails when that variable is unset. It never reads stdin.
func debugModels() int {
	if path := os.Getenv("FAKECODEX_RECORD"); path != "" {
		record(path, nil)
	}
	path := os.Getenv("FAKECODEX_MODELS")
	if path == "" {
		fmt.Fprintln(os.Stderr, "fakecodex: no model catalog (FAKECODEX_MODELS is unset)")
		return 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakecodex: %v\n", err)
		return 1
	}
	os.Stdout.Write(data)
	return 0
}

// mcpList is `codex mcp list --json`: it prints the file FAKECODEX_MCP_LIST
// names, verbatim, or an empty list when that variable is unset. It records
// nothing, so a run's own record is not overwritten, and never reads stdin.
func mcpList() int {
	path := os.Getenv("FAKECODEX_MCP_LIST")
	if path == "" {
		fmt.Println("[]")
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakecodex: %v\n", err)
		return 1
	}
	os.Stdout.Write(data)
	return 0
}

func record(path string, stdin []byte) {
	cwd, _ := os.Getwd()
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	data, _ := json.Marshal(map[string]any{
		"argv": os.Args, "stdin": string(stdin), "env": env, "cwd": cwd,
		"pid": os.Getpid(), "instructions": instructionsText(),
	})
	_ = os.WriteFile(path, data, 0o600)
}

// instructionsText is what the file named by a model_instructions_file override
// holds right now, or "" when there is no such override or file. It is read at
// the call, because the caller may remove the file once the call is over.
func instructionsText() string {
	for _, arg := range os.Args[1:] {
		if path, ok := strings.CutPrefix(arg, "model_instructions_file="); ok {
			text, _ := os.ReadFile(path)
			return string(text)
		}
	}
	return ""
}

// spawnChild starts a sleeping grandchild in this process's group, detached
// from stdout/stderr so it never holds the caller's pipes open.
func spawnChild() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "__sleeper")
	_ = cmd.Start()
}

// spawnPipeHolder starts a sleeping grandchild in a session of its own, so it
// leaves the provider's process group, that keeps this process's stdout and
// stderr open. Its pid is written to pidFile so the test can clean it up.
func spawnPipeHolder(pidFile string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "__holder")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
}

func outputArg(args []string) string {
	for i, a := range args {
		if (a == "-o" || a == "--output-last-message") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// lastAgentMessage returns the text of the fixture's last agent_message item.
func lastAgentMessage(fixture []byte) (string, bool) {
	var text string
	found := false
	sc := bufio.NewScanner(bytes.NewReader(fixture))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "item.completed" && ev.Item.Type == "agent_message" {
			text, found = ev.Item.Text, true
		}
	}
	return text, found
}
