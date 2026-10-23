# Implementation frame

> This frame describes agentcli up to 0.6.3. [`split-PRD.md`](split-PRD.md) amends and supersedes the requirements it builds against for msgate, as its Appendix A maps.

Shared decisions every contributor builds against. The requirements live in
`docs/PRD.md` (FR ids) and `docs/test-cases.md`. This file fixes what crosses
package boundaries so no slice invents its own version.

## Toolchain

- Go 1.27 (`golang:1.27.1-bookworm`), `CGO_ENABLED=0`, built with `-trimpath`.
- Dependencies: the standard library, plus `golang.org/x/sys/unix` where the
  standard library has no equivalent (process start time on darwin). Nothing else.
- All Go commands run through `make` (Docker). The host needs only Docker.
  `make test TESTFLAGS='-run TestX ./internal/foo/...'` narrows a run.
- Version metadata comes from ldflags into `internal/version`
  (`Version`, `SourceCommit`, `BuildSeq`). Defaults: `dev`, `unknown`, `0`.

## Package layout

| Package | Owns |
|---|---|
| `cmd/agentcli` | `main`: calls `cli.Main(os.Args, env) int` and exits with its result |
| `internal/cli` | argument parsing, command registry, exit codes, stdout/stderr contract |
| `internal/provider` | the `Provider` interface, capability types, provider registry |
| `internal/provider/codex` | the Codex adapter: plan building and event parsing |
| `internal/runner` | plan → spawn → stream-parse → finalize, for foreground and worker modes |
| `internal/store` | home resolution, run directories, conversations, state files, locks |
| `internal/telemetry` | record types (`run`, `annotation`, `model_call`), append, fold, `runs` export, `stats`, the ETA estimate |
| `internal/prices` | the price cache, price-list parsing, wanted models, run and model-call cost, the implicit-cache flag |
| `internal/profile` | built-in scenario profiles and precedence resolution |
| `internal/link` | launcher resolution and writing |
| `internal/version` | build metadata |
| `internal/testutil/fakecodex` | the fake provider binary used by tests (a `main` package) |
| `test/e2e` | black-box tests against the built `agentcli` binary |
| `tools/import-legacy` | the legacy telemetry import program (FR37) |
| `plugin/` | plugin content copied into `dist`: skill, mod (including the progress summarizer), commands, hooks |

## Command registration

Each command lives in its own file in `internal/cli` (`cmd_exec.go`,
`cmd_review.go`, …) and registers itself from an `init()` into the package's
command table. No slice edits a shared dispatch switch.

## Exit codes

One file, `internal/cli/exitcodes.go`, holds the SDK codes from FR5:
`2` usage · `3` conversation busy · `4` not found · `5` wait timed out ·
`6` not resumable · `7` price list unavailable · `70` internal · `124` timeout · `125` lost ·
`127` provider missing · `130` cancelled by request. Signals map to `128+n`.
Provider exits pass through unchanged.

## Provider contract

```go
type Capabilities struct {
    Commands          []string // "exec", "review", "resume"
    ReviewTargets     []string // "base", "uncommitted", "commit"
    Sandboxes         []string // "read-only", "workspace-write"
    ReportsUsage      bool
    PreassignsSession bool
}

type Plan struct {
    Argv      []string          // argv[0] is the provider binary name
    Stdin     StdinSource       // StdinPrompt or StdinEmpty
    Dir       string
    EnvAdd    map[string]string
    EnvRemove []string
}

type Event struct {
    SessionID string  // non-empty when this event reveals the provider session
    Usage     *Usage  // non-nil on usage-bearing events
    ErrorMsg  string  // non-empty on error-bearing events
}

type Provider interface {
    Name() string
    Capabilities() Capabilities
    ReservedFlag(arg string) (reserved bool)
    BuildPlan(req Request) (Plan, error)
    ParseEvent(line []byte) (Event, bool) // false: unparseable line
    VersionArgs() []string
}

// Optional, like ModelReporter: a provider that can list the models it
// offers and names the price-list namespace of their entries.
type ModelCatalog interface {
    PriceNamespace() string                         // codex: "openai"
    CatalogModels(env []string) ([]string, error)  // codex: `codex debug models`, models[].slug
    CatalogEntries(env []string) ([]CatalogEntry, error) // every model in the provider's order: ID, Name, Visibility
}

// Optional: a provider that can tell what a run has used so far, from its own
// session records. ok is false when there is nothing to report yet.
type LiveUsageReporter interface {
    LiveUsage(env []string, sessionID string, since time.Time) (u Usage, model string, ok bool, err error)
}

// Optional: a provider that writes every uncached input token to its prompt
// cache while reporting no cache writes (implicit prompt caching).
type ImplicitCaching interface {
    ImplicitCacheWrites() bool // codex: true
}
```

Cost and implicit caching: `prices.Usage` has a field `ImplicitCacheWrites bool`.
`RunCost` estimates a zero `CacheWrite` as `max(0, Input − Cached)` only when it
is true; when it is false the usage is priced exactly as reported. A provider
that is not registered (`anthropic`) or does not implement `ImplicitCaching` is
false. Both `RunCost` callers set the field from the record's provider: `stats`
(runs and model calls) and the live cost of `progress`. A unit test pins that
Anthropic-shaped usage with no cache is priced at exactly the input rate.

Model-call providers: `anthropic` is the only one. It is not a `Provider` (the
binary never runs it); `usage add` accepts it, its price namespace is fixed as
`anthropic` → `anthropic`, and it has no catalog. `prices refresh` wants, per
provider, the models of that provider's model calls in the whole telemetry as
well, with empty and `unknown` excluded.

`Request` carries: command (`exec`/`review`/`resume`), prompt presence, cwd,
model, effort, sandbox, review target, provider session id, passthrough args,
output file path. Field names are the implementer's choice as long as the
semantics are these.

Codex specifics that are decided:
- Argv order exactly as FR13. The output file is always passed with `-o`.
- An `error` event or a `turn.failed` event provides `ErrorMsg`. When that
  message is itself a JSON document with `.error.message`, the inner message is
  used. `item.completed` events whose item type is `error` are not errors (they
  are warnings) and are ignored.
- Usage that is all zeros on a review becomes null.

## Persisted formats

All JSON, written with 0600 permissions under directories with 0700.
Atomic writes are a temp file in the same directory plus rename.

`runs/<run_id>/state.json`:

```json
{
  "run_id": "r-20261004T231500Z-a1b2c3d4",
  "conversation_id": "c-20261004T231500Z-9f8e7d6c",
  "turn": 1,
  "state": "queued|running|done|failed|cancelled|timeout|lost",
  "background": false,
  "admitted_at": "RFC3339 UTC",
  "started_at": "RFC3339 UTC or null",
  "ended_at": "RFC3339 UTC or null",
  "worker_pid": 0,
  "provider_pgid": 0,
  "provider_start_time": "opaque string or null",
  "exit_code": null,
  "outcome": null,
  "provider_exit": null,
  "sdk_status": null,
  "error_excerpt": null,
  "unparsed_events": 0,
  "output_path": "absolute path",
  "run_dir": "absolute path"
}
```

`provider_exit` (int or null) and `sdk_status` (string) are null until the run
is terminal and are written at every terminal transition: finish, abort, lost
and cancel-while-queued. Readers print what is recorded; they derive both from
`state` and `exit_code` only for a state file that has no `sdk_status`.

`runs/<run_id>/request.json`: the resolved request (command, provider,
scenario, model, effort, sandbox and their `_source`s, cwd, source, session id,
timeout, clean sentinel, material label, attrs, passthrough) plus the plan. A
run that relays a prompt through a parent session to one of its child sessions
also has `relay`:
`{"provider_session_id": "<child session id>", "address": "<the child's name to its parent>"}`.
`prompt.md` keeps the caller's prompt; a relayed run sends the provider the
prompt that asks the parent to pass it on.

`conversations/<conversation_id>.json`:

```json
{
  "conversation_id": "c-…",
  "provider": "codex",
  "provider_session_id": null,
  "cwd": "absolute path",
  "defaults": {"scenario": "…", "model": "…", "effort": "…", "sandbox": "…",
               "model_source": "flag|profile|provider-default|provider-session", "effort_source": "…", "sandbox_source": "…"},
  "turns": ["r-…"],
  "active_run_id": null,
  "created_at": "RFC3339 UTC",
  "updated_at": "RFC3339 UTC"
}
```

`status` (`busy`/`idle`) and `resumable` are derived on read, not stored.

A cancel request is the presence of `runs/<run_id>/cancel.request`.

Retention files: `index/sessions/<session id>` (one run id per line, appended at
admission; an empty session id uses the key `_`) and `prune.stamp` (the time of
the last `prune --auto`, written under `prune.lock`). Both live under the home.
Index appends are one O_APPEND write under `index/lock`, which prune also takes
when it rewrites or deletes an index file. A session id that is not file-safe
(a path separator, a leading dot, longer than 128, or a literal `_`) is keyed
`~<first 16 hex of sha256>`. A pruned run is first renamed to
`runs/.prune-<id>`, then deleted; readers ignore dot-prefixed run directories.
`prune --json` prints `sdk_status, exit_code, ran, dry_run, older_than_days,
removed, kept, skipped, failed, bytes_freed, stopped_early`.

`runs/<run_id>/worker.log` is the stderr of a job's worker (0600), bounded to
its last 64 KiB like `stderr.tail`; runner warnings raised inside a job land
there.

Lock files: `runs/<run_id>/lock`, `conversations/<conversation_id>.lock`,
`telemetry/.lock`, and the launcher's lock next to the launcher. All use
`flock(2)` exclusive locks; descriptors are close-on-exec.

Telemetry records: exactly the FR31 fields for `run`, `{v, kind, run_id, ts,
attrs}` for `annotation`, and the fields below for `model_call`, one JSON object
per line. Field order is the on-disk order:

```json
{"v":1,"kind":"model_call","call_id":"m-…","ts":"2026-10-08T15:40:02Z","session_id":"<session id>","provider":"anthropic","model":"claude-haiku-5-5","source":"mod-summary","run_id":"r-…","usage":{"input_tokens":458,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":18,"reasoning_output_tokens":0}}
```

- `call_id` is `m-<UTC yyyymmddThhmmssZ>-<8 lowercase hex>`, minted by `usage add`
  with the generator of run ids and the prefix `m`, unless `--call-id` gives it
  (`[A-Za-z0-9][A-Za-z0-9:._-]{0,199}`).
- `ts` is RFC 3339 UTC to the second, like a run record's. For a model call it is the time `usage add` records it, or `--ts`; the record goes in the month file of that time.
- `attrs` is present only when `usage add` is given `--attr key=value`: at most 32 keys of `[a-z_]{1,32}`, values up to 200 characters. It follows `usage`.
- `run_id` is the run the call was about, or `null`.
- `usage` has agentcli's usage shape (the `provider.Usage` JSON tags, in the order
  above). `input_tokens` is the total input, including the cached and cache-write
  tokens.
- Fold: a `model_call` line needs a non-empty `call_id`, `ts`, `provider` and
  `model`, otherwise it counts as `unparseable`. A repeated `call_id` keeps the
  first. Model calls are kept apart from runs, in the folded result as
  `ModelCalls`. They take the window of runs, by `ts`, and the session filter, by
  `session_id` equality. They appear in no `runs`, `status`, `total`,
  `by_provider` (run counts), reliability, outcomes, durations, findings or
  `by_source` output. A binary that predates the kind counts these lines as
  `skipped.unknown_kind`.

`prices.json` (FR71), written atomically under `prices.lock` by
`prices refresh`; prices are decimal strings in US dollars per million tokens,
already resolved (an omitted cache-read or cache-write price is the input
price):

```json
{
  "v": 1,
  "source": "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json",
  "etag": "\"…\"",
  "fetched_at": "RFC3339 UTC",
  "checked_at": "RFC3339 UTC",
  "unit": "usd_per_1m_tokens",
  "models": {
    "codex": {
      "gpt-6.1-sol": {"input": "2", "cached_input": "0.1", "cache_write": "2.5", "output": "10"}
    }
  },
  "unpriced": {"codex": ["codex-auto-review", "gpt-reserve"]}
}
```

Decimal strings are the shortest exact form: no exponent, no trailing zeros
after the point, and no point for whole numbers. `prices.lock` uses `flock(2)`
like the other locks. A model-call provider's models sit under its own key
(`"anthropic": {"claude-haiku-5-5": {…}}`) once a refresh has priced them.

## ETA estimate

`agentcli eta --scenario <name> [--cwd <dir>] [--json]` folds the whole
telemetry history (no window, no `--days` or `--all`) and passes the folded runs
to `telemetry.EstimateETA(runs, scenario, cwd, repoKey)`. The repo-key function
is a parameter: the command passes the git lookup below, and module tests pass a
stub that names which directories share a repository.

- Usage errors exit 2 with the usual error object: `--scenario` missing or
  empty, an unknown flag, a positional argument. `--cwd` defaults to the process
  working directory and is made absolute (`filepath.Abs`), because telemetry
  records absolute cwds and a relative directory outside git would match none.
  A working directory, home or telemetry that cannot be read exits 70.
- Samples: folded runs whose `scenario` equals `--scenario`, whose `cwd` is a
  non-empty string and whose `duration_ms` reads as a number (`asInt`: a JSON
  number, or a string holding an integer). Every outcome counts, `null`
  included. Model calls are not among the folded runs. A run whose
  `duration_ms` does not read as a number counts in neither `samples` nor
  `repos`. Skipped
  telemetry lines are not reported (`stats` reports them).
- Repo key of a directory: its symbolic links are resolved first
  (`filepath.EvalSymlinks`; a directory that is gone stays as written), so two
  spellings of one directory, such as `/tmp` and `/private/tmp` on macOS, share
  a key. The key is then the trimmed stdout of
  `git -C <dir> rev-parse --path-format=absolute --git-common-dir`, which a work
  tree, its subdirectories and its linked worktrees share. When that fails for
  any reason (the directory is gone, it is not a repository, git is missing, a
  non-zero exit) or prints nothing, the key is the resolved directory itself.
  Each distinct directory, `--cwd` included, is resolved once per invocation.
- The target key is the repo key of `--cwd`.
  - Some sample has the target key → `basis: "repo"`: `eta_ms` is the mean
    `duration_ms` of those samples, `samples` their count, `repos` 1.
  - Otherwise, with samples → `basis: "global"`: samples are grouped by repo
    key, `eta_ms` is the mean of the group means (every repository weighs the
    same), `samples` is every sample and `repos` the number of groups.
  - No samples → `basis: "none"`, `eta_ms: null`, `samples: 0`, `repos: 0`,
    exit 0.
- `eta_ms` is rounded to the nearest integer millisecond, half away from zero
  (`math.Round`).

With `--json`, stdout is one object, keys in this order, exit 0. `repo` is the
target key whatever the basis:

```json
{"sdk_status":"ok","exit_code":0,"scenario":"code-review","repo":"/work/repo/.git","basis":"repo","eta_ms":156000,"samples":12,"repos":1}
```

Without it, stdout is one line:

- `ETA ~2m 36s (repo average, 12 runs)`
- `ETA ~2m 23s (global average, 8 repos)`
- `ETA unknown (no history)`

`1 run` and `1 repo` are singular. The duration is `floor(eta_ms / 1000)`
seconds, written `<s>s` under 60 seconds and `<m>m <s>s` otherwise: for a
duration that is not negative, the text `formatElapsed` in
`plugin/hooks/subagent.js` gives.

## Mod: progress summaries

The plugin manifest (`plugin/.claude-plugin/plugin.json`) gains a `userConfig`
field `summaries`: boolean, default true, with a title and a description saying
that it summarizes progress with `claude-haiku-5-5` and sends progress text to the
Anthropic API. `register(on, options)` reads `options.summaries`.

- Active when `options.summaries !== false` and
  `(await $.session.surfaces()).length > 0`, read at each summary. Otherwise the
  mod keeps its behaviour from before the summaries: raw entries in the row and
  the band. The environment variable `MSGATE_SUMMARIES`, the intervals, the off
  mode and the steps of a stepped row amend this in "Mod: recorded summaries"
  below.
- While summaries are active, neither the agent row nor the band shows a raw
  progress entry. The row streams `» <label>` for each label that settles, and
  the band's last part is the newest label; before a run's first label the band
  line ends after the cost part (`labelled` gives the run an empty step, which
  `bandLine` drops). A failed or rejected summary keeps the previous label: the
  row streams nothing new and the failure is logged once per run. The row's
  first line `agentcli · <type>`, the `still working · <elapsed>` line and the
  final answer are unchanged.
- The call is
  `$.model.complete({ model: 'claude-haiku-5-5', effort: 'low', system: SYSTEM, prompt, maxTokens: 40, timeoutMs: 15000 })`
  inside try/catch, because a rejection counts as a failure.
- The window is the run's progress entries since the previous summary request,
  oldest first, at most the 10 newest. Each entry is a line formatted like
  `progressLine` and cut to 300 characters.
- A label is cleaned in this order: trim the reply; reject it if it holds `\n`
  or `\r`; strip the other control and ANSI characters; then reject it when it is
  empty, starts with `#`, a backtick, `*`, `-` or `>`, or is over 100 characters.
  A rejection is a failure.
- A summary request that is due while the in-flight caps (1 per run, 2 per
  session) are full or the 429 pause is on is deferred, never dropped. It is made
  once a slot frees or the pause ends, with the window as it stands then. Until
  then the band keeps its last label (no step part before the first label) and
  the row streams nothing new.
- After every completion whose usage has any non-zero count, the mod runs
  `[bin, 'usage', 'add', '--session-id', sid, '--provider', 'anthropic', '--model', 'claude-haiku-5-5', '--source', 'mod-summary', '--run-id', runId, '--json']`
  with stdin `JSON.stringify(usage)`. An exit 0 increments the module counter
  `usageAdds`, which `showStatus` compares with its value at the last successful
  `stats` read.

SYSTEM, as measured in the benchmark:

```
You label what a coding agent is doing right now, for a one-line status row that truncates around 40 characters.
Describe its most recent action in 3-5 words using present tense (-ing). Name the file, command or function, not the branch.
Reply with the label only: one line, no quotes, no markdown, no final period.

Good: "Reading workerlog.go"
Good: "Running store tests"
Good: "Reviewing the uncommitted diff"
Bad (past tense): "Read workerlog.go"
Bad (too vague): "Investigating the issue"
Bad (too long): "Reviewing the full branch diff and the store package integration"
```

Prompt:

```
Newest activity of the agent, oldest first:
- <entry>
- <entry>

Previous label: <label> (say something NEW).
```

The last paragraph is left out when there is no previous label.

## Mod: recorded summaries

The summaries above are the **live summaries**: a label streamed as `thinking`,
which the host shows and does not record. The **recorded summaries** put the
same progress in the history of the row that follows the run, as `text`, which
the host records (`claude-code.d.ts`, the `turn.step` chunks).

**Manifest.** `userConfig` gains two `number` options with no `min` or `max` (a
value outside them stops the whole module from loading, so the module checks the
range): `liveSummaryEvery`, default 3, and `recordedSummaryEvery`, default 8.
`plugin/hooks/settings.js` (`summarySettings({ options, env })`) reads them with
`MSGATE_SUMMARIES` and answers `{ isEnabled, liveEvery, recordedEvery, notes }`.
- `liveSummaryEvery` is an integer from 1 to 10 (`WINDOW_ENTRIES`), else 3;
  `recordedSummaryEvery` an integer from 1 to 24 (`RECORD_WINDOW_ENTRIES`), else
  8, or 0 for none. A value out of range or not an integer falls back, with one
  note per load in the debug log.
- `MSGATE_SUMMARIES` is read with `$.env.get('MSGATE_SUMMARIES')` at each use,
  so the name is listed by `claude plugin validate`. `0` turns the summaries
  off, `1` on, over the `summaries` option; unset, or a read that throws, leaves
  the option; any other value leaves the option, with one note.

**The recorder.** One per follow of a run (`plugin/hooks/recorder.js`), created
when the summaries are on, `recordedEvery` is above 0 and the session has a
surface. `watchRun` feeds it the raw progress lines of every slice, whether or
not the live summaries replace them on screen. Entries are numbered from 1.
- Once `recordedEvery` entries are unrecorded, and none of the follow's own
  requests is in flight, it asks for a record of all the unrecorded entries (the
  newest 24 of them, each cut to 300 characters). When the run ends and the turn
  was not interrupted, one more request covers what is left, awaited for at most
  15 s; its failure or timeout skips it.
- The call is `$.model.complete({ model: 'claude-haiku-5-5', effort: 'low',
  system: RECORD_SYSTEM, prompt, maxTokens: 120, timeoutMs: 15000 })` inside
  try/catch. `RECORD_SYSTEM` asks for one or two plain sentences in English, in
  the past tense, naming the file, command or function, at most 400 characters,
  with no markdown or quotes.
- A reply is cleaned (ANSI and control characters out, whitespace runs to one
  space); one over 400 characters is cut after its last whole sentence, else at
  its last whole word, else at 400. An unanswered or empty reply is no record:
  its window stays unrecorded, the failure is logged once for the run, and the
  next request covers it.
- Requests take their slots from the limiter shared with the live summaries
  (`plugin/hooks/limiter.js`: 2 in flight for the session, and the 429 pause),
  and their usage goes to `usage add` under the source `mod-summary`.

**In one step.** A row followed in one step (a type not backed with `stepped`,
or one of the cases under "In steps" that fall back to it) puts the records in
the same step response as the live `thinking`, as `text` chunks at block indices
1 to *k*, one per record, whose text is `» <first>–<last>: <sentence>` and a
newline (`<first>` alone when the record covers one entry). They come after the
`thinking` blocks (index 0) and before the hand-back `tool` chunk of a row
Claude called, or the final `text` of a hidden row, which sit at index *k* + 1.
The step's `answer` is the streamed text. The hand-back message, the report that
returns to Claude and every later step are unchanged, so they carry no record. A
turn interrupted before the records are ready streams none, and no request is
left running.

**In steps.** For a type backed with `stepped: true`, the records stay in the
progress steps and the answer in the final step. The turn is followed in steps
when the type is stepped, `isProgressRegistered()` says the `progress` tool
registered in this load, the summaries are on and `$.session.surfaces()` is not
empty; each is read at every step, and a turn that is already in steps when the
summaries go off finishes in the step it is in. A step covers
at least `liveEvery` entries of the run (a slice can bring more), or ends
with the run.
- A step that ends before the run does waits for the label request of its
  window (`settled` in `plugin/hooks/summarizer.js`), then streams `text`
  chunks from block index 1: the labels that settled (`» <label>` and a
  newline), then the records that settled since the previous step (the form
  above). The `thinking` blocks at index 0 come before them, as in one step. The
  step ends with a `tool` chunk named `mcp__msgate__progress`, with the id
  `msgate-progress-<turnId>-<index>`, an `input` chunk of `{}` and a `stop` of
  `tool_use`. The `tool.call` hook answers the marker with an empty result, for
  a call that carries an `agentId`, and the host asks for the next step. The
  step's `answer` is the streamed text, and the turn's remembered answer is left
  to the step that ends it.
- The step in which the run ends, or the turn is interrupted, streams the
  records not yet written (`finishFollow`), then the answer as in one step: the
  final `text` of a hidden row, the hand-back call of a row Claude called. A
  label that settles in this step is drawn as `thinking` only.
- A follow is the state of one run's reading: the next entry to read, the labels
  and the recorder. It is kept in `steppedTurns` by agent id, with the turn id,
  the run id and whether the row started the run, from the first step of a
  turn to its end. It is dropped when the run ends, on an error, when the
  generator closes, when the agent starts a new turn, and by the `turn.complete`
  hook, which also cancels a run the row started itself when the turn was
  aborted (a cancel that fails is logged with the run id). The map is cleared
  by `register()`. Each step hands the follow its own `$` and its own signal,
  because the follow outlives the dispatch it was made in; only a recorder
  request already in flight keeps the `$` of the step that started it.
- `isOverStepLimit` counts a step only while no state is kept for its turn, so
  a stepped turn counts its first step and no other.
- A later step that finds no state for a turn with `index` above 0 means the
  module loaded again. A hidden row starts a new follow, which reads the run's
  entries again, and logs one debug line. A row that started its own run cannot
  name that run, so its step ends with the hand-off failure that says to read
  the run with the `jobs` tool; the run goes on and nothing delivers it.
- A step waits with `$.process.run` calls (`msgate wait`, one slice at a time)
  and on the request of a label, and calls no `$.clock.sleep`.

**Off.** When the summaries are off the mod makes no summary request and builds no
recorder, and it adds nothing of its own to the row: the `msgate · <type>` line
that opened the row and the `still working · <elapsed>` line are not streamed.
Codex's entries still stream as `thinking`, and reasoning the provider encrypts
produces no entry. The spinner keeps its `msgate · <type>` message. Off with no
surface (`claude -p`) is as before: that is "inactive", and the opener and the
heartbeat stay. Switching the summaries off during a run lets go of its
recorder, and its unrecorded entries are not recorded.

## Mod: spinner

- A `ui.render` hook on `{ component: 'Spinner' }`, whose `.catch` draws the
  spinner untouched (`next(e)`).
- `turnAgents` is a module map, agent id → `{ runId, type }`: an entry is set
  once the turn's job is admitted and deleted when the turn ends, so it holds
  the agentcli subagents whose turn is running in this load.
- When `e.requestId` is in `turnAgents` and the engine has set no `message`
  (`e.props.message` is null or absent), the hook returns
  `next({ ...e, props: { ...e.props, message } })`. `message` is the run's
  newest label (`summaries.shown(runId)`), which exists only while summaries are
  active, otherwise `agentcli · <type>`. It is the bare label, without the row's
  `» ` mark. A message the engine set (a compaction's, say) stays, and any other
  spinner is `next(e)`, untouched.
- The first time the hook sees a request id, it logs
  `Spinner raised for <requestId> with the word <word>` with `{ to: 'debug' }`.
  Every spinner is logged, not only those of known agents. The ids already
  logged sit in the module-level set `loggedSpinners`, which `session.start`
  does not reset, so the line is written once per request id per load of the
  module.
- The summarizer's port `labelSettled(runId)`, called when a label settles,
  runs `$.ui.invalidate('ui.render')` when an agent in `turnAgents` follows that
  run, so the spinner is drawn again with the new label.

## Mod: spawn notice

Each run of the session gets one notice, shown with `$.ui.toast(text)` and
`$.ui.log(text)` (the transcript):

```
agentcli · spawned <scenario> · trigger <source> · ETA ~2m 36s (repo average, 12 runs)
agentcli · spawned <scenario> · trigger <source> · ETA ~2m 23s (global average, 8 repos)
agentcli · spawned <scenario> · trigger <source> · ETA unknown (no history)
agentcli · spawned <scenario> · trigger <source> · ETA unavailable
agentcli · spawned <scenario> · trigger <source> · finished in 3s
```

`<scenario>` and `<source>` are the run's own values.

- Claim: before a run is announced, `claimSpawn(runId)` records it in the
  `$.state` key `spawnNotified` (a `string[]` of run ids, newest last, the newest
  500 kept, declared in `plugin/types/index.d.ts`) and tells whether this call
  recorded it. A run this load has already claimed, or whose id the key already
  holds, is not announced again, so a run gets one notice across the admission
  path, the listings and reloads. Claims run one at a time (each reads, changes
  and writes the key back), so two made together both land. A write that fails
  is logged to the debug log every time, and the run is still announced once
  for the load; a later load may announce it again.
- Runs the mod admits itself (an agentcli agent type's turn, the `ask` and
  `send` tools) are announced by work that `startNotice` hands to
  `$.clock.after(0, …)` right after admission, so neither the tool result nor
  the turn waits for it (`notifyAdmitted`). An agent type's run has the type
  name as scenario and `agent` as source, which is what the type passes. For
  `ask` and `send`, `notifyTurn` first reads `[bin, 'status', <run_id>, '--json']`
  and takes the run's own `scenario` and `source` (`mod`), since `send` reuses
  the conversation's first-turn scenario, which the tool does not know. A read
  that exits non-zero, throws, cannot be parsed or names no string scenario and
  source leaves the run to the next listing and goes to the debug log once per
  load.
- Every other run (a hook's, or one started from the CLI with the session id)
  is found by `scanSpawns`, which a `$.clock.every(BAND_REFRESH_MS, …)` timer
  (5 seconds) started at `session.start` runs. It reads
  `[bin, 'status', '--json', '--session-id', <session id>]` and never overlaps
  itself. The listing holds the session's 20 newest runs (`statusLimit` in
  `internal/cli/cmd_status.go`), so when more than 20 runs start between two
  reads, the oldest of those not announced at admission get no notice. A listing that exits non-zero, throws, or is not an object with a
  `runs` array is a failed scan: it sets no baseline and goes to the debug log
  once, until a scan succeeds. This read is its own; the 15-second poll keeps
  doing what it did.
  - A scan first claims every run of its listing, oldest first, then announces
    the runs it claimed, so a notice that fails cannot leave a run of the
    baseline to be announced by a later scan.
  - A listed run with no non-empty `scenario`, `source` or `state` string
    (`isAnnounceable` in `plugin/hooks/spawn.js`) is neither claimed nor
    announced; the first one of a load goes to the debug log as
    `skipped run <id> in the spawn scan: the listing gives it no scenario,
    source or state`. A later listing that gives it all three announces it.
  - The first listing of a load that succeeds is its baseline. A claimed run
    that is not terminal is announced with its ETA. A claimed terminal run is
    announced as finished only when its `ended_at` falls in or after the second
    of the load (`loadedAt`, `$.clock.now()` read at `session.start`, compared
    from the start of its second, since run timestamps carry no fractions);
    the others stay silent. A missing or unreadable `ended_at` stays silent.
  - After the baseline, a claimed run is announced: not terminal → the ETA
    notice; terminal → `finished in <d>`, with no ETA lookup. `<d>` is
    `formatElapsed` of `ended_at` minus `started_at` (else `admitted_at`); when
    a timestamp is missing or unreadable the tail is `finished`, with no
    duration.
  - Each `session.start` starts a new baseline: the runs this load has seen,
    `loadedAt` and the logged-once flags are reset.
- ETA: `lookupEta` runs
  `[bin, 'eta', '--scenario', <scenario>, '--cwd', <session cwd>, '--json']`
  with `timeoutMs: ETA_TIMEOUT_MS` (10 seconds, so a stalled git cannot hold a
  scan), the session cwd being `e.cwd` of `session.start`, because the run
  listing carries no cwd; a run that works in another repository gets the
  estimate of the session's repository. The mod writes the ETA part from the
  JSON: `basis` `repo` → `ETA ~<formatElapsed(eta_ms)> (repo average, <samples> run[s])`,
  `global` → `ETA ~<formatElapsed(eta_ms)> (global average, <repos> repo[s])`,
  `none` → `ETA unknown (no history)`. Anything else gives `ETA unavailable`:
  a non-zero exit, a process error, output that is not JSON, an
  unknown basis, an `eta_ms` that is not a finite number under `repo` or
  `global`, or a `samples` (`repo`) or `repos` (`global`) that is not a
  non-negative integer. A failed lookup goes to the debug log once per load.
- A notice that cannot be shown (its toast or transcript line throws) goes to
  the debug log once per load and does not stop the scan.

## Test harness

- `test/e2e` builds `agentcli` and `fakecodex` once in `TestMain` into a temp
  directory, puts the fake first on `PATH` as `codex`, and gives each test its
  own `AGENTCLI_HOME`.
- `fakecodex` reads its behaviour from environment variables:
  - `FAKECODEX_FIXTURE`: path of a JSONL fixture streamed to stdout.
  - `FAKECODEX_OUTPUT`: text written to the `-o` file; when unset, the text of the
    fixture's last `agent_message` item.
  - `FAKECODEX_EXIT`: exit code.
  - `FAKECODEX_STDERR`: text written to stderr.
  - `FAKECODEX_SLEEP_MS`: delay before exiting.
  - `FAKECODEX_IGNORE_TERM=1`: ignore SIGTERM.
  - `FAKECODEX_SPAWN_CHILD=1`: start a sleeping grandchild in the same group.
  - `FAKECODEX_RECORD`: a path to write `{argv, stdin, env, cwd}` as JSON.
  - `FAKECODEX_VERSION`: the output of `--version`.
  - `FAKECODEX_PIPE_HOLDER=<pidfile>`: start a `setsid` grandchild that keeps
    stdout/stderr open and write its pid to the file.
  - `FAKECODEX_CHILD_IGNORE_TERM=1`: the spawned grandchild ignores SIGTERM.
  - `FAKECODEX_MODELS`: path of a JSON file printed as-is by `codex debug models`
    (exit 0). When unset, `debug models` prints an error and exits 1.
  - `FAKECODEX_MCP_LIST`: path of a JSON file printed as-is by `codex mcp list --json`
    (exit 0). When unset, it prints `[]`. It records nothing.
  - `FAKECODEX_LOGIN_EXIT`: the exit code of `codex login status` (0 when unset; it
    prints "Logged in using ChatGPT" on 0, "Not logged in" to stderr otherwise).
  - `FAKECODEX_RATE_LIMITS`: path of a JSON file served as the `result` of the
    `account/rateLimits/read` reply of `codex app-server` (stdio, one JSON object
    per line; it answers `initialize` too and ignores notifications). When unset
    it answers that request with a JSON-RPC error; a file that is not JSON is
    sent as it is. The fake exits when its stdin closes.
  - `FAKECODEX_SLEEP_MS` also delays `login status` before it exits and
    `app-server` before its first reply.
- Every e2e sandbox sets `AGENTCLI_PRICES_URL=off` unless the test sets it to the
  URL of an `httptest` server on 127.0.0.1 that serves a fixture price list
  (`testdata/prices/`).
- A model call is recorded in an e2e test by running `agentcli usage add` with
  usage on stdin; no fake binary is involved.
- The plugin test kit (`plugin/tests/kit.ts`) answers two seams beneath the
  plugin: `model.complete` (it records the requests it receives and answers
  scripted replies) and `session.surfaces` (it answers `[]` by default, so the
  tests that existed before the summaries stay headless and unchanged; a test
  that wants summaries opts in with a surface). A test sets the plugin options
  (`summaries`, `liveSummaryEvery`, `recordedSummaryEvery`) that
  `register(on, options)` receives, and the value `$.env.get` answers for
  `MSGATE_SUMMARIES` (or makes it throw). `readStep` reads a step chunk by chunk
  and can stop the turn between two chunks. The kit also keeps the
  texts sent to the debug log apart (`debugLogs`), records every event name the
  plugin asks to redraw (`invalidations`), and draws the engine's `Spinner` as
  its `message`, else its `word`.
- ETA tests write run records into the sandbox home's month file and build real
  git repositories, a linked worktree and plain directories under temp
  directories; `git` must be on `PATH`.
- Live-usage tests set `CODEX_HOME` to a temp directory and write a Codex session
  file there (`sessions/<y>/<m>/<d>/rollout-<time>-<thread id>.jsonl`) holding
  `turn_context` and `token_count` lines; the fake provider writes none itself.
- `AGENTCLI_TEST_PRICES_TIMEOUT_MS` and `AGENTCLI_TEST_PRICES_MAX_BYTES` shorten
  the refresh's 20-second timeout and 32 MiB body limit for tests.
- `AGENTCLI_TEST_SHUTDOWN_GRACE_MS` and `AGENTCLI_TEST_SHUTDOWN_DRAIN_MS`
  shorten the termination grace and the pipe drain for tests. Production
  defaults are 5 seconds each.
- Release tests build a temp git repository with a `VERSION` history and put a
  fake `gh` first on `PATH`: a shell script the test writes, which logs its
  arguments, answers `release view` with the exit code the test sets, and keeps
  a copy of the notes file `release create` is given. No test calls GitHub.
- Fixtures in `testdata/codex/` are recorded Codex streams with fictitious ids.
  Never put real ids, emails or home paths in the repository.

## Output contract details

- `sdk_status` takes one value per SDK outcome, mirroring the exit codes:
  `ok` (the SDK did its part; the provider's own exit may still be non-zero),
  `usage_error`, `busy`, `not_found`, `not_resumable`, `wait_timeout`,
  `internal_error`, `timeout`, `lost`, `provider_missing`, `cancelled`,
  `prices_unavailable`.
  `provider_exit` is null when the provider never ran.
- With `--json`, every exit path prints exactly one JSON object on stdout,
  usage errors included: `{"sdk_status": "...", "exit_code": N, "error": "..."}`
  plus whatever ids exist at that point.
- `--dry-run` prints `{command, provider, run_id, argv, stdin, cwd, env_add,
  env_remove}`; `stdin` is `prompt` or `empty`, and `argv` names the real
  future output path.
- `--skip-git-repo-check` sits after `-o <path>` and before passthrough flags.
  A work tree is detected by walking up from the cwd for a `.git` entry.
- An error event sets `error_excerpt` even when the provider exits 0.
- Flags may be interspersed with the positional prompt; everything after the
  first literal `--` is passthrough.
- `ReservedFlag` is called with each passthrough token alone and with each
  token joined to its successor by one space, so `-c model=x` arrives whole.
- A conversation's `defaults` hold the resolved model, effort and sandbox
  (profile applied), so later turns reuse them unless a flag overrides.
- In a run record an absent value is `null`, never an empty string
  (`session_id`, `provider_session_id`, `model`, …). `output_bytes` is `0` when
  the output file is missing. `ts` is the run's start at second resolution.
- The provider version probe runs beside the provider, is bounded at 5 seconds
  and keeps the first non-empty stdout line.
- Fold: an annotation that precedes its run record in append order (a running
  job annotated before it finishes) is applied once that record is read; an
  annotation whose run never appears creates nothing. Duplicate run records
  for one `run_id` keep the first.
- `annotate` exits 70 when the lock or the write fails: the annotation is the
  command's whole job, unlike a run's telemetry.
- Imported legacy records keep fields that have no run-record name (such as
  `status`) as given; native runs derive `status` from `outcome` on read.
- A conversation's active-turn marker is cleared only by the run it names.
- Termination: after SIGTERM to the provider group, SIGKILL follows as soon as
  the group leader exits or the 5-second grace ends, whichever comes first.
  On timeout or cancellation `provider_exit` is the observed wait status
  (for example 143 or 137); `sdk_status` is `timeout` or `cancelled`
  (`cancelled` for both 130 and 128+n). `--timeout` takes a positive integer
  of seconds.
- Jobs: the worker is the hidden `agentcli _worker <run_id>`, started with
  `setsid` and null stdio. `result` on a non-terminal run exits 3 and names
  `wait`. `status --json` prints `{"runs": [...]}`, `conversations --json`
  prints `{"conversations": [...]}`; a run object's `exit_code` is the run's
  recorded exit. `status` lists newest first by `admitted_at`, then run id.
- `wait` returns after finalization: the run is terminal and its run lock is
  released, so the conversation marker and the telemetry record are written.
  It waits at most 10 seconds past the terminal state for the lock; if it is
  still held it prints one warning on stderr and returns anyway (seam:
  `AGENTCLI_TEST_FINALIZE_WAIT_MS`). `--timeout` covers the whole wait and
  exits 5 when it passes during finalization.
- Test seams for jobs: `AGENTCLI_TEST_ADMISSION_WAIT_MS` (admission wait,
  default 10 seconds), `AGENTCLI_TEST_WORKER_STALL_MS` (the worker sleeps
  before taking the run lock) and `AGENTCLI_TEST_QUEUED_GRACE_MS` (how long a
  queued run may go without a lock holder before readers count it lost,
  default 30 seconds).
- Lost: every reader of a non-terminal run (`status`, `wait`, `cancel`,
  `result`, `send`) probes the run lock without blocking. A free lock with the
  state `running`, or with `queued` for longer than the grace, settles the
  run under the run lock itself, after re-reading the state, so racing readers
  settle it once; a reader that finds the lock held leaves the run alone. The
  provider group is killed only when its leader still has the recorded start
  time. The settled run is `lost`, exit 125, `sdk_status` `lost`,
  `provider_exit` null, and finishes in the usual order.
- Cancel: `agentcli cancel <run_id> [--json]` writes `cancel.request`, then a
  queued run no worker holds is marked `cancelled` (exit 130) by `cancel`
  itself, and a running run gets SIGTERM sent to its recorded worker pid. A
  queued run whose lock a worker holds is awaited until it is running (bounded
  by the admission wait). `cancel` exits 0 whichever way the request lands,
  4 for an unknown id, and prints the run like `status`; on a terminal run it
  writes nothing. A worker re-reads the state once it holds the run lock: a
  terminal state, or a cancel request on a queued run, ends the worker without
  spawning the provider and without touching the state. An admission whose
  worker left for that reason ends the run `cancelled`, not `failed`.
- Usage add: `agentcli usage add --session-id <id> --provider anthropic --model
  <model> --source <source> [--run-id <run_id>] [--json]` records one model call.
  - Stdin is one JSON object, Anthropic's usage exactly as `$.model.complete`
    returns it: `{"input_tokens":N,"output_tokens":N,"cache_read_input_tokens":N,"cache_creation_input_tokens":N}`.
    A missing key reads as 0, and any other key is ignored (the reply's usage
    object may gain fields). A value that is not a non-negative integer, or stdin
    that is not one JSON object, exits 2 before anything is written.
  - `--provider` must be a model-call provider; `anthropic` is the only one, and
    anything else exits 2. `--session-id`, `--model` and `--source` are required
    and non-empty (exit 2). `--run-id` is recorded as given. An unknown flag or a
    positional argument exits 2, like every other command.
  - The record's `ts` is the time the command records the call, UTC to the second.
  - Normalization, in Go: `input_tokens` = input + cache_read + cache_creation;
    `cached_input_tokens` = cache_read; `cache_write_input_tokens` =
    cache_creation; `output_tokens` = output; `reasoning_output_tokens` = 0.
  - When all four counts are 0 nothing is written, and the command exits 0 with
    `recorded: false`.
  - With `--json`, stdout is one object
    `{"sdk_status":"ok","exit_code":0,"recorded":true,"call_id":"m-…"}`
    (`"recorded":false,"call_id":null` when nothing was written); a usage error
    prints the usual `{"sdk_status":"usage_error","exit_code":2,"error":"…"}`.
    Without `--json`, stdout is the call id on one line, or nothing.
  - A telemetry lock or write failure exits 70 (`sdk_status` `internal_error`), as
    `annotate` does: the model call is the command's whole job.
- Stats: `usage_by_provider` is an object keyed by provider. Each value has
  exactly the shape of `usage_totals`: `input_tokens`, `cached_input_tokens`,
  `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`,
  `runs_with_usage`, `cost_usd`, `cost_complete`. For a model-call provider
  `runs_with_usage` counts its model calls with usage. A provider appears when it
  has at least one run or model call with usage in the window and session
  filter. Per provider, `cost_usd` is null exactly when none of its records with
  usage has a price. `usage_totals`, the text `tokens:` lines and the text
  `cost:` line stay runs-only. `usage_by_model`, `unpriced_models` and
  `missing_prices` include the models of model calls, and so do the per-model
  `cost <model>:` text lines.
- The mod reads the binary through `$.process.run`, which keeps the first
  4194304 bytes (4 MiB) of standard output and as many of standard error, reads
  the rest and drops it, and sets `isStdoutTruncated` or `isStderrTruncated`
  (`claude-code.d.ts`, `ProcessRunResult`; the limit counts bytes, not
  characters). A command whose output the mod must have whole stays under 4 MiB,
  and a read that must be whole checks the flag before it uses the text.
- The prompt a hidden row shows whole is capped far below that, at 1 MiB
  (1048576 bytes of UTF-8, `SHOWN_PROMPT_MAX_BYTES` in `plugin/hooks/hidden.js`):
  the host accepts a larger prompt at the spawn, but the turn of a row on the
  `haiku` model fails at once with "Prompt is too long". Over the cap the row
  opens with the plain `msgate run <run_id>` prompt.

## Rules for every slice

- Test-first: one failing test, minimal code to pass it, refactor on green.
  Tests exercise the CLI or a package's exported surface, never internals.
- Run `make test` (or a narrowed `TESTFLAGS`) and paste the output in the report.
- No git commits, no pushes, no network access other than module downloads, and
  never invoke a real `codex` binary. A test server on 127.0.0.1 inside the test
  process is not network access.
- Code comments state the behaviour or invariant. They never cite requirement
  or test-case ids (`FR…`); those belong in commit messages.
- Never stop, kill or remove a container, process or file you did not create.
  Every `make` container carries the label `agentcli.dev=1`; a hung test is
  stopped with `docker ps -q --filter label=agentcli.dev=1 | xargs -r docker kill`,
  never with a host-wide `docker ps -q`.
- A choice that would change a documented contract (CLI, exit codes, persisted
  formats, telemetry fields) is not made inside a slice: report it back.
