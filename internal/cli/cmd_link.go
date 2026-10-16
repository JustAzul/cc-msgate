package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JustAzul/cc-msgate/internal/link"
	"github.com/JustAzul/cc-msgate/internal/version"
)

func init() {
	register(Command{Name: "link", Summary: "put a msgate launcher on PATH (~/.local/bin)", Run: runLink, NoHome: true})
}

func runLink(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(ctx.Stderr, "usage: msgate link [--quiet] [--target <msgate shim or binary>] [--dir <launcher dir>]")
	}
	quiet := fs.Bool("quiet", false, "print nothing unless something fails")
	target := fs.String("target", "", "msgate shim or binary the launcher executes")
	dir := fs.String("dir", "", "launcher directory (default ~/.local/bin)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 0 {
		ctx.Errorf("link takes no positional arguments")
		return ExitUsage
	}
	if ctx.Getenv("MSGATE_NO_LINK") == "1" {
		return ExitOK
	}

	execPath := *target
	if execPath != "" {
		abs, err := filepath.Abs(execPath)
		if err != nil {
			ctx.Errorf("%v", err)
			return ExitUsage
		}
		if _, err := os.Stat(abs); err != nil {
			ctx.Errorf("--target %s: %v", execPath, err)
			return ExitUsage
		}
		execPath = abs
	} else {
		root, err := link.PluginsRoot(ctx.Getenv)
		if err != nil {
			ctx.Errorf("%v", err)
			return ExitInternal
		}
		install, err := link.FindInstall(root)
		switch {
		case errors.Is(err, link.ErrNoInstallRecord):
			ctx.Errorf("no install record for %s in %s; install the plugin, or pass --target <path to a msgate shim or binary>", link.PluginKey, root)
			return ExitNotFound
		case err != nil:
			ctx.Errorf("%v", err)
			return ExitInternal
		}
		execPath = filepath.Join(install, "bin", "msgate")
	}

	launcherDir := *dir
	if launcherDir == "" {
		home := ctx.Getenv("HOME")
		if home == "" {
			ctx.Errorf("cannot locate ~/.local/bin: HOME is not set (pass --dir)")
			return ExitInternal
		}
		launcherDir = filepath.Join(home, ".local", "bin")
	}

	action, err := link.Ensure(link.Options{Dir: launcherDir, Target: execPath, BuildSeq: version.BuildSeqNumber()})
	if err != nil {
		ctx.Errorf("writing the launcher: %v", err)
		return ExitInternal
	}
	path := link.LauncherPath(launcherDir)
	switch action {
	case link.Foreign:
		if !*quiet {
			ctx.Errorf("not overwriting %s: it is not a msgate launcher", path)
		}
	case link.Written:
		if !*quiet {
			fmt.Fprintf(ctx.Stdout, "linked %s -> %s\n", path, execPath)
		}
	case link.Unchanged:
		if !*quiet {
			fmt.Fprintf(ctx.Stdout, "launcher %s is up to date\n", path)
		}
	}
	return ExitOK
}
