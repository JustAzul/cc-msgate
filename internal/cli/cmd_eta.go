package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

func init() {
	register(Command{Name: "eta", Summary: "estimate how long a run of a purpose takes", Run: runETA})
}

// etaReport is what eta prints with --json.
type etaReport struct {
	SDKStatus string `json:"sdk_status"`
	ExitCode  int    `json:"exit_code"`
	Purpose   string `json:"purpose"`
	Repo      string `json:"repo"`
	Basis     string `json:"basis"`
	ETAMS     *int64 `json:"eta_ms"`
	Samples   int    `json:"samples"`
	Repos     int    `json:"repos"`
}

func runETA(ctx *Context, args []string) int {
	var purpose, cwd string
	var asJSON bool
	fset := flag.NewFlagSet("eta", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.StringVar(&purpose, "purpose", "", "purpose to estimate")
	fset.StringVar(&cwd, "cwd", "", "directory the run starts in (default: current)")
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	ctx.JSON = wantsJSON(args)
	positional, err := parseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(ctx.Stderr, "usage: msgate eta --purpose <label> [--cwd <dir>] [--json]")
		return ExitOK
	}
	if err != nil {
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	switch {
	case len(positional) != 0:
		return ctx.Fail(ExitUsage, "eta takes no arguments, got %q", positional[0])
	case purpose == "":
		return ctx.Fail(ExitUsage, "--purpose is required")
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return ctx.Fail(ExitInternal, "reading the working directory: %v", err)
		}
	}
	// Telemetry records absolute cwds, so a directory outside git must key by
	// its absolute path to match them.
	if cwd, err = filepath.Abs(cwd); err != nil {
		return ctx.Fail(ExitInternal, "resolving --cwd: %v", err)
	}
	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	folded, err := telemetry.Fold(home, nil)
	if err != nil {
		return ctx.Fail(ExitInternal, "reading telemetry: %v", err)
	}
	eta := telemetry.EstimateETA(folded.Runs, purpose, cwd, gitRepoKey)
	if asJSON {
		out, err := json.Marshal(etaReport{"ok", ExitOK, purpose, eta.Repo, eta.Basis, eta.MS, eta.Samples, eta.Repos})
		if err != nil {
			return ctx.Fail(ExitInternal, "encoding the estimate: %v", err)
		}
		fmt.Fprintf(ctx.Stdout, "%s\n", out)
		return ExitOK
	}
	fmt.Fprintln(ctx.Stdout, etaLine(eta))
	return ExitOK
}

// gitRepoKey names the repository a directory belongs to by its common git
// directory, which a work tree and its linked worktrees share. A directory git
// cannot resolve, for any reason, is its own key. Symbolic links in a directory
// that still exists are resolved first, so two spellings of one directory
// (/tmp and /private/tmp on macOS) share a key.
func gitRepoKey(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if key := strings.TrimSpace(string(out)); err == nil && key != "" {
		return key
	}
	return dir
}

// etaLine is the one-line text form of an estimate.
func etaLine(eta telemetry.ETA) string {
	switch eta.Basis {
	case "repo":
		return fmt.Sprintf("ETA ~%s (repo average, %s)", etaDuration(*eta.MS), plural(eta.Samples, "run"))
	case "global":
		return fmt.Sprintf("ETA ~%s (global average, %s)", etaDuration(*eta.MS), plural(eta.Repos, "repo"))
	}
	return "ETA unknown (no history)"
}

// etaDuration renders milliseconds as "<s>s" under a minute, else "<m>m <s>s",
// with the seconds floored.
func etaDuration(ms int64) string {
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
