// Package cli parses arguments, dispatches commands and owns the
// stdout/stderr/exit-code contract.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	// Provider adapters register themselves from init().
	_ "github.com/JustAzul/cc-msgate/internal/provider/codex"
)

// Context is what a command sees of its process.
type Context struct {
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time

	// JSON is true once the caller asked for --json; every exit path then also
	// prints one JSON object on stdout. IDs are the ids that exist so far.
	JSON bool
	IDs  map[string]string
}

// Getenv returns the last value set for key in the caller environment.
func (c *Context) Getenv(key string) string {
	v := ""
	prefix := key + "="
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, prefix) {
			v = kv[len(prefix):]
		}
	}
	return v
}

// Warnf prints one warning line on stderr.
func (c *Context) Warnf(format string, a ...any) {
	fmt.Fprintf(c.Stderr, "msgate: warning: "+format+"\n", a...)
}

// Errorf prints one error line on stderr.
func (c *Context) Errorf(format string, a ...any) {
	fmt.Fprintf(c.Stderr, "msgate: "+format+"\n", a...)
}

// Fail prints the error on stderr and, in JSON mode, the FRAME error object on
// stdout, then returns the exit code. It is the single exit path for SDK
// errors, so --json never misses one.
func (c *Context) Fail(code int, format string, a ...any) int {
	msg := fmt.Sprintf(format, a...)
	c.Errorf("%s", msg)
	c.emitErrorJSON(code, msg)
	return code
}

// emitErrorJSON prints {"sdk_status","exit_code","error"} plus known ids when
// JSON mode is on.
func (c *Context) emitErrorJSON(code int, msg string) {
	if !c.JSON {
		return
	}
	obj := map[string]any{"sdk_status": sdkStatusFor(code), "exit_code": code, "error": msg}
	for k, v := range c.IDs {
		obj[k] = v
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return
	}
	c.Stdout.Write(append(data, '\n'))
}

// sdkStatusFor maps an SDK exit code to its sdk_status value.
func sdkStatusFor(code int) string {
	switch code {
	case ExitOK:
		return "ok"
	case ExitUsage:
		return "usage_error"
	case ExitBusy:
		return "busy"
	case ExitNotFound:
		return "not_found"
	case ExitWaitTimeout:
		return "wait_timeout"
	case ExitNotResumable:
		return "not_resumable"
	case ExitPricesUnavailable:
		return "prices_unavailable"
	case ExitTimeout:
		return "timeout"
	case ExitLost:
		return "lost"
	case ExitProviderMissing:
		return "provider_missing"
	case ExitCancelled:
		return "cancelled"
	}
	return "internal_error"
}

// wantsJSON scans raw arguments (up to the first "--") for --json, for exit
// paths reached before the flag set could be parsed.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "json" || !strings.HasPrefix(a, "-") {
			continue
		}
		if !hasValue {
			return true
		}
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return false
}

// Command is one registered subcommand.
type Command struct {
	Name    string
	Summary string
	Run     func(ctx *Context, args []string) int
	// Hidden commands are internal entry points; help does not list them.
	Hidden bool
	// NoHome commands never read the home, so they run before any migration.
	NoHome bool
}

var commands = map[string]Command{}

// register adds a command; each command file calls it from init().
func register(c Command) {
	if _, dup := commands[c.Name]; dup {
		panic("command registered twice: " + c.Name)
	}
	commands[c.Name] = c
}

// Main runs msgate with os.Args-shaped args (args[0] is the program name)
// and the caller environment, and returns the process exit code.
func Main(args []string, env []string) int {
	return Run(args, env, os.Stdin, os.Stdout, os.Stderr)
}

// Run is Main with injectable streams.
func Run(args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx := &Context{Env: env, Stdin: stdin, Stdout: stdout, Stderr: stderr, Now: time.Now, IDs: map[string]string{}}
	if len(args) < 2 {
		usage(stderr)
		return ExitUsage
	}
	switch args[1] {
	case "-h", "--help", "help":
		usage(stdout)
		return ExitOK
	}
	cmd, ok := commands[args[1]]
	if !ok {
		ctx.JSON = wantsJSON(args[1:])
		usage(stderr)
		return ctx.Fail(ExitUsage, "unknown command %q", args[1])
	}
	if !cmd.NoHome {
		if code := migrateHome(ctx, args[2:]); code != ExitOK {
			return code
		}
	}
	return cmd.Run(ctx, args[2:])
}

func usage(w io.Writer) {
	names := make([]string, 0, len(commands))
	for n, c := range commands {
		if !c.Hidden {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	fmt.Fprintln(w, "usage: msgate <command> [flags] [prompt] [-- native flags]")
	fmt.Fprintln(w, "commands:")
	for _, n := range names {
		fmt.Fprintf(w, "  %-10s %s\n", n, commands[n].Summary)
	}
}
