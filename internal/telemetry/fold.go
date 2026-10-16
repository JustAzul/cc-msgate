package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Skipped counts the lines a reader could not use.
type Skipped struct {
	UnknownKind    int `json:"unknown_kind"`
	UnknownVersion int `json:"unknown_version"`
	Unparseable    int `json:"unparseable"`
}

// Total is the number of skipped lines.
func (s Skipped) Total() int { return s.UnknownKind + s.UnknownVersion + s.Unparseable }

// Folded is the result of reading telemetry: one generic object per run, in
// the order the run records were appended, plus what was skipped. Numbers are
// json.Number so integers survive untouched.
type Folded struct {
	Runs       []map[string]any
	ModelCalls []map[string]any
	Skipped    Skipped
}

var monthFileName = regexp.MustCompile(`^\d{4}-\d{2}\.jsonl$`)

// MonthFiles lists the month files under home in ascending month order. A
// missing telemetry directory is an empty list.
func MonthFiles(home string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(home, "telemetry"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && monthFileName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Fold reads the month files under home, oldest month first and lines in file
// order, and folds annotations into their runs. Model-call records are kept
// apart in ModelCalls. A run's attrs start from its
// run record; each annotation then shallow-merges its attrs on top, one
// top-level key at a time, later wins, null stored as a value. Annotations
// are folded before the window filter, which applies to the run's ts.
//
// A nil since reads every month. Otherwise reading starts at the month of
// since (a run's record is never appended before its ts) and only runs with
// ts at or after since are returned.
//
// An annotation appended before its run's record (a job annotated while it
// still runs) is applied once the record arrives. An annotation whose run was
// never read creates nothing. Lines with an unknown v or kind, and lines that
// are not usable JSON objects, are skipped and counted; v is checked first.
// When the same run_id has several run records, the first one wins; so does
// the first of several model calls with the same call_id. A model call needs a
// call_id, ts, provider and model, and takes the same ts window as a run.
func Fold(home string, since *time.Time) (Folded, error) {
	files, err := MonthFiles(home)
	if err != nil {
		return Folded{}, err
	}
	startMonth := ""
	if since != nil {
		startMonth = since.UTC().Format("2006-01")
	}
	f := &folder{index: map[string]int{}, calls: map[string]bool{}, pending: map[string][]map[string]any{}}
	for _, name := range files {
		if name[:7] < startMonth {
			continue
		}
		if err := f.readFile(filepath.Join(home, "telemetry", name)); err != nil {
			return Folded{}, err
		}
	}
	out := Folded{Skipped: f.skipped}
	for _, run := range f.runs {
		if since != nil {
			ts, _ := run["ts"].(string)
			t, ok := ParseTS(ts)
			if !ok || t.Before(*since) {
				continue
			}
		}
		out.Runs = append(out.Runs, run)
	}
	for _, call := range f.modelCalls {
		if since != nil {
			ts, _ := call["ts"].(string)
			t, ok := ParseTS(ts)
			if !ok || t.Before(*since) {
				continue
			}
		}
		out.ModelCalls = append(out.ModelCalls, call)
	}
	return out, nil
}

type folder struct {
	runs       []map[string]any
	modelCalls []map[string]any
	calls      map[string]bool
	index      map[string]int
	pending    map[string][]map[string]any
	skipped    Skipped
}

func (f *folder) readFile(path string) error {
	return forEachLine(path, f.foldLine)
}

// forEachLine calls fn with every non-blank line of the file, newline
// included when present. Lines may be arbitrarily long.
func forEachLine(path string, fn func(line []byte)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	r := bufio.NewReaderSize(file, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// InSession keeps the records (runs or model calls) recorded with the given
// session id. The result is never nil.
func InSession(records []map[string]any, sessionID string) []map[string]any {
	kept := []map[string]any{}
	for _, rec := range records {
		if id, _ := rec["session_id"].(string); id == sessionID {
			kept = append(kept, rec)
		}
	}
	return kept
}

// RunIDs returns the run_id of every run record in every month file,
// whatever its schema version.
func RunIDs(home string) (map[string]bool, error) {
	files, err := MonthFiles(home)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, name := range files {
		err := forEachLine(filepath.Join(home, "telemetry", name), func(line []byte) {
			rec, ok := decodeObject(line)
			if !ok || rec["kind"] != "run" {
				return
			}
			if id, _ := rec["run_id"].(string); id != "" {
				ids[id] = true
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// RunFields lists a run record's fields in their on-disk order.
func RunFields() []string { return append([]string(nil), fieldOrder...) }

func (f *folder) foldLine(line []byte) {
	rec, ok := decodeObject(line)
	if !ok {
		f.skipped.Unparseable++
		return
	}
	if n, isNum := rec["v"].(json.Number); !isNum {
		f.skipped.UnknownVersion++
		return
	} else if v, err := n.Int64(); err != nil || v != Version {
		f.skipped.UnknownVersion++
		return
	}
	runID, _ := rec["run_id"].(string)
	switch rec["kind"] {
	case "run":
		if runID == "" {
			f.skipped.Unparseable++
			return
		}
		if _, dup := f.index[runID]; dup {
			return
		}
		readScenarioAsPurpose(rec)
		attrs, isObj := rec["attrs"].(map[string]any)
		if !isObj {
			attrs = map[string]any{}
			rec["attrs"] = attrs
		}
		for _, ann := range f.pending[runID] {
			mergeAttrs(attrs, ann)
		}
		delete(f.pending, runID)
		f.index[runID] = len(f.runs)
		f.runs = append(f.runs, rec)
	case "annotation":
		attrs, isObj := rec["attrs"].(map[string]any)
		if runID == "" || !isObj {
			f.skipped.Unparseable++
			return
		}
		if i, known := f.index[runID]; known {
			mergeAttrs(f.runs[i]["attrs"].(map[string]any), attrs)
		} else {
			f.pending[runID] = append(f.pending[runID], attrs)
		}
	case "model_call":
		for _, key := range []string{"call_id", "ts", "provider", "model"} {
			if v, _ := rec[key].(string); v == "" {
				f.skipped.Unparseable++
				return
			}
		}
		callID := rec["call_id"].(string)
		if f.calls[callID] {
			return
		}
		f.calls[callID] = true
		f.modelCalls = append(f.modelCalls, rec)
	default:
		f.skipped.UnknownKind++
	}
}

// readScenarioAsPurpose gives a record written before the purpose had its name
// the purpose it recorded as a scenario, so every reader sees the purpose only.
// A record that holds both keeps its purpose; a purpose that is null, as an
// import of the legacy format leaves it, counts as absent.
func readScenarioAsPurpose(rec map[string]any) {
	scenario, hasScenario := rec["scenario"]
	if !hasScenario {
		return
	}
	if purpose, _ := rec["purpose"].(string); purpose == "" {
		rec["purpose"] = scenario
	}
	delete(rec, "scenario")
}

func mergeAttrs(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

// decodeObject parses exactly one JSON object, keeping numbers as json.Number.
func decodeObject(line []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok && m != nil
}

// ParseTS reads a record timestamp: RFC 3339 with or without fractional
// seconds, or the same without a zone (taken as UTC).
func ParseTS(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// fieldOrder is the on-disk order of a run record's fields; MarshalRun puts
// these first and any other field after them, sorted.
var fieldOrder = []string{
	"v", "kind", "run_id", "ts", "provider", "provider_version", "command", "purpose",
	"model", "model_source", "effort", "effort_source", "sandbox", "source", "session_id",
	"conversation_id", "turn", "cwd", "background", "exit_code", "outcome", "duration_ms",
	"timeout_s", "output_file", "output_bytes", "error_excerpt", "usage", "provider_session_id", "attrs",
}

// MarshalRun encodes a run object on one line with the record's fields in
// their on-disk order and unknown fields last, so output stays stable and
// readable without dropping anything.
func MarshalRun(run map[string]any) ([]byte, error) {
	known := make(map[string]bool, len(fieldOrder))
	keys := make([]string, 0, len(run))
	for _, k := range fieldOrder {
		known[k] = true
		if _, ok := run[k]; ok {
			keys = append(keys, k)
		}
	}
	var extra []string
	for k := range run {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	keys = append(keys, extra...)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := encodeValue(run[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// encodeValue is json.Marshal without HTML escaping, so text such as "<" or
// "&" reads the same in the output as in the record.
func encodeValue(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
