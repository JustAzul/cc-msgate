package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "annotate", Summary: "attach attributes to a finished run's telemetry", Run: runAnnotate})
}

// annotation is the telemetry line annotate appends.
type annotation struct {
	V     int            `json:"v"`
	Kind  string         `json:"kind"`
	RunID string         `json:"run_id"`
	TS    string         `json:"ts"`
	Attrs map[string]any `json:"attrs"`
}

const annotateUsage = "usage: msgate annotate <run_id | output path> [--attr key=value]... [--attr-json key=<json>]... [--json]"

func runAnnotate(ctx *Context, args []string) int {
	var attrs attrList
	var asJSON bool
	fset := flag.NewFlagSet("annotate", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	fset.Var(attrFlag{list: &attrs}, "attr", "attribute key=value (repeatable)")
	fset.Var(attrFlag{list: &attrs, asJSON: true}, "attr-json", "attribute key=<json> (repeatable)")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, annotateUsage)
		return ExitOK
	}
	if err != nil {
		return failFlagParse(ctx, annotateUsage, err)
	}
	ctx.JSON = asJSON
	if len(positional) != 1 {
		return ctx.Fail(ExitUsage, "annotate takes exactly one run id or output path")
	}
	if len(attrs.values) == 0 {
		return ctx.Fail(ExitUsage, "annotate needs at least one --attr or --attr-json")
	}

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	st := store.Open(home)
	runID, found := resolveRunTarget(st, positional[0])
	if !found {
		return ctx.Fail(ExitNotFound, "no run matches %q", positional[0])
	}
	ctx.IDs["run_id"] = runID
	readerGate(ctx)
	if !st.RunExists(runID) {
		return ctx.Fail(ExitNotFound, "no run matches %q", positional[0])
	}

	now := ctx.Now().UTC().Truncate(time.Second)
	line, err := json.Marshal(annotation{V: telemetry.Version, Kind: "annotation", RunID: runID, TS: now.Format(time.RFC3339), Attrs: attrs.object()})
	if err != nil {
		return ctx.Fail(ExitInternal, "encoding the annotation: %v", err)
	}
	if err := telemetry.AppendLine(home, line, now, telemetry.Options{}); err != nil {
		return ctx.Fail(ExitInternal, "writing the annotation: %v", err)
	}
	if asJSON {
		out, err := json.Marshal(map[string]any{
			"sdk_status": "ok", "exit_code": ExitOK, "run_id": runID, "ts": now.Format(time.RFC3339), "attrs": attrs.object(),
		})
		if err != nil {
			return ctx.Fail(ExitInternal, "%v", err)
		}
		fmt.Fprintf(ctx.Stdout, "%s\n", out)
		return ExitOK
	}
	fmt.Fprintln(ctx.Stdout, runID)
	return ExitOK
}

// parseInterspersed parses flags that may follow or precede positionals.
func parseInterspersed(fset *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fset.Parse(rest); err != nil {
			return nil, err
		}
		if fset.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fset.Arg(0))
		rest = fset.Args()[1:]
	}
}

// resolveRunTarget maps a run id or an output file path to a run id. A path is
// matched against each run's recorded output_path after both sides have their
// symlinks resolved, so a path reached through a symlinked directory still
// finds its run.
func resolveRunTarget(st *store.Store, target string) (string, bool) {
	if !strings.ContainsRune(target, filepath.Separator) {
		if store.ValidateRunID(target) != nil || !st.RunExists(target) {
			return "", false
		}
		return target, true
	}
	want := canonicalPath(target)
	// The run directory is normally the path's parent, so try it first.
	if id := filepath.Base(filepath.Dir(target)); store.ValidateRunID(id) == nil && outputMatches(st, id, want) {
		return id, true
	}
	entries, err := os.ReadDir(filepath.Join(st.Home, "runs"))
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() && outputMatches(st, e.Name(), want) {
			return e.Name(), true
		}
	}
	return "", false
}

func outputMatches(st *store.Store, runID, want string) bool {
	state, err := st.ReadState(runID)
	if err != nil || state.OutputPath == "" {
		return false
	}
	return canonicalPath(state.OutputPath) == want
}

// canonicalPath is an absolute path with symlinks resolved. When the file
// itself is gone it resolves the directory and keeps the base name.
func canonicalPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	} else if !errors.Is(err, fs.ErrNotExist) {
		return abs
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}
