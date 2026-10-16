// Command import-legacy loads the retired codex-hub telemetry file into the
// msgate telemetry store.
//
// Usage: import-legacy --from <legacy jsonl> [--home <dir>] [--lock-wait <duration>]
//
// The home is resolved like the CLI does (MSGATE_HOME, XDG_STATE_HOME,
// ~/.local/state/msgate) unless --home is given. Exit 0: done, 1: I/O or
// lock failure, 2: usage error. The last stdout line summarizes the run:
// imported=N skipped_existing=N unparseable=N.
//
// A line that is not a JSON object, or whose ts cannot be read, cannot be
// placed in a month file and counts as unparseable. Imported records are
// appended month by month while the telemetry lock is held; an interrupted
// import is finished by running it again, since existing run ids are skipped.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
	"github.com/JustAzul/cc-msgate/internal/telemetry"
)

const (
	exitIO    = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	fset.SetOutput(stderr)
	fset.Usage = func() {
		fmt.Fprintln(stderr, "usage: import-legacy --from <legacy jsonl> [--home <dir>] [--lock-wait <duration>]")
	}
	from := fset.String("from", "", "legacy telemetry file")
	homeFlag := fset.String("home", "", "msgate home (default: resolved like the CLI)")
	lockWait := fset.Duration("lock-wait", 30*time.Second, "how long to wait for the telemetry lock")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exitUsage
	}
	if *from == "" || fset.NArg() != 0 {
		fset.Usage()
		return exitUsage
	}
	home := *homeFlag
	if home == "" {
		var err error
		if home, err = store.ResolveHome(getenv); err != nil {
			fmt.Fprintf(stderr, "import-legacy: %v\n", err)
			return exitIO
		}
	}
	lines, err := readLines(*from)
	if err != nil {
		fmt.Fprintf(stderr, "import-legacy: %v\n", err)
		return exitIO
	}

	lock, err := telemetry.AcquireLock(home, *lockWait)
	if err != nil {
		fmt.Fprintf(stderr, "import-legacy: telemetry lock: %v\n", err)
		return exitIO
	}
	defer lock.Close()
	existing, err := telemetry.RunIDs(home)
	if err != nil {
		fmt.Fprintf(stderr, "import-legacy: reading existing telemetry: %v\n", err)
		return exitIO
	}

	var sum summary
	byMonth := map[string][][]byte{}
	monthAt := map[string]time.Time{}
	seen := map[string]int{}
	for _, raw := range lines {
		occurrence := seen[string(raw)]
		seen[string(raw)]++
		rec, at, ok := convert(raw, occurrence)
		if !ok {
			sum.unparseable++
			continue
		}
		if existing[rec.id] {
			sum.skipped++
			continue
		}
		existing[rec.id] = true
		month := at.Format("2006-01")
		byMonth[month] = append(byMonth[month], rec.line)
		monthAt[month] = at
		sum.imported++
	}
	months := make([]string, 0, len(byMonth))
	for m := range byMonth {
		months = append(months, m)
	}
	sort.Strings(months)
	for _, m := range months {
		if err := lock.AppendLines(monthAt[m], byMonth[m]...); err != nil {
			fmt.Fprintf(stderr, "import-legacy: writing %s: %v\n", m, err)
			return exitIO
		}
	}
	fmt.Fprintf(stdout, "imported=%d skipped_existing=%d unparseable=%d\n", sum.imported, sum.skipped, sum.unparseable)
	return 0
}

type summary struct{ imported, skipped, unparseable int }

// readLines returns the non-blank lines of the file without their newline.
func readLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var lines [][]byte
	for {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
		if err == io.EOF {
			return lines, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

type converted struct {
	id   string
	line []byte
}

// convert maps one legacy line to a msgate run record and returns the time
// whose month file should hold it. ok is false when the line is not a JSON
// object or its ts cannot be read.
func convert(raw []byte, occurrence int) (converted, time.Time, bool) {
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(raw, &legacy); err != nil || legacy == nil {
		return converted{}, time.Time{}, false
	}
	var ts string
	if err := json.Unmarshal(legacy["ts"], &ts); err != nil {
		return converted{}, time.Time{}, false
	}
	at, ok := telemetry.ParseTS(ts)
	if !ok {
		return converted{}, time.Time{}, false
	}

	sum := sha256.Sum256(append(append(append([]byte(nil), raw...), '\n'), strconv.Itoa(occurrence)...))
	id := "legacy-" + hex.EncodeToString(sum[:])[:16]

	rec := map[string]any{
		"v": telemetry.Version, "kind": "run", "run_id": id, "provider": "codex",
	}
	for _, field := range telemetry.RunFields() {
		if _, set := rec[field]; set || field == "attrs" {
			continue
		}
		if v, present := legacy[field]; present {
			rec[field] = v
		} else {
			rec[field] = nil
		}
	}
	attrs := map[string]any{"legacy": true}
	if findings, present := legacy["findings"]; present {
		attrs["review.findings"] = findings
	}
	rec["attrs"] = attrs
	// Legacy fields with no run-record name travel along unchanged.
	for k, v := range legacy {
		if _, set := rec[k]; !set && k != "findings" {
			rec[k] = v
		}
	}
	line, err := telemetry.MarshalRun(rec)
	if err != nil {
		return converted{}, time.Time{}, false
	}
	return converted{id: id, line: line}, at, true
}
