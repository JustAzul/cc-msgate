package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"

	"github.com/JustAzul/cc-msgate/internal/version"
)

func init() {
	register(Command{Name: "version", Summary: "print version, source commit, build_seq and platform", Run: runVersion, NoHome: true})
}

type versionInfo struct {
	Version      string `json:"version"`
	SourceCommit string `json:"source_commit"`
	BuildSeq     int    `json:"build_seq"`
	Platform     string `json:"platform"`
}

func runVersion(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Fprintln(ctx.Stderr, "usage: msgate version [--json]") }
	asJSON := fs.Bool("json", false, "print one JSON object")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ctx.Fail(ExitUsage, "%v", err)
	}
	if fs.NArg() != 0 {
		ctx.Errorf("version takes no arguments")
		return ExitUsage
	}
	info := versionInfo{
		Version:      version.Version,
		SourceCommit: version.SourceCommit,
		BuildSeq:     version.BuildSeqNumber(),
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
	}
	if *asJSON {
		b, err := json.Marshal(info)
		if err != nil {
			ctx.Errorf("%v", err)
			return ExitInternal
		}
		fmt.Fprintf(ctx.Stdout, "%s\n", b)
		return ExitOK
	}
	fmt.Fprintf(ctx.Stdout, "msgate %s (commit %s, build_seq %d, %s)\n", info.Version, info.SourceCommit, info.BuildSeq, info.Platform)
	return ExitOK
}
