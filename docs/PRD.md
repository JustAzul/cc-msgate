# Product Requirements Document

> This document describes agentcli up to 0.6.3. [`split-PRD.md`](split-PRD.md) amends and supersedes it for msgate, as its Appendix A maps.

**Project / Feature**: agentcli (`agentcli`)
**Date**: 2026-10-04 (updated 2026-10-08: session cost, group Q; progress summaries and Claude spend, group R; release tags, FR93; spawn notice, ETA and spinner, group S)

---

## 1. Executive Summary

### Problem Statement
Claude Code reaches other agent CLIs (today only Codex) through three unrelated paths: a wrapper script used by a skill, three autonomous review hooks that call the provider directly, and a parallel-review pattern that launches the provider under `nohup` with no telemetry. These paths write two incompatible telemetry schemas, cannot continue a conversation, have no managed background execution, and must each be rewritten to add another provider.

### Proposed Solution
A single static Go binary, `agentcli`, that is the only way any consumer talks to an agent CLI. It offers one provider-agnostic command surface for one-shot runs and resumable conversations, foreground or as background jobs, and one extensible, append-only telemetry stream. It ships as a Claude Code plugin with zero runtime dependencies: a skill, a thin mod that gives Claude native tools and completion notices, and a launcher for shell callers. All existing consumers are migrated to it, and the old wrapper is removed.

### Business Impact
- One dispatch path: adding Claude Code as the next provider changes one adapter, not every consumer.
- Every dispatch becomes observable: token usage, outcome, error reason and arbitrary consumer attributes in one stream that never loses history.
- Claude Code gains agent-to-agent conversations it can continue later and background jobs that announce their own completion.

### Success Metrics
- **Direct provider call sites left in migrated consumers:** 0. Today there are 5 executable call sites (Observed by text search: three hook cores, the wrapper script, and the parallel-review reference). Verified by the same text search after migration (FR56).
- **Runs with a telemetry record:** 100% of `agentcli` runs, including the parallel-review path, which records 0 today (Observed: its dispatch has no telemetry step).
- **Error runs carrying an `error_excerpt`:** ≥ 90%. Baseline Observed (30-day window ending 2026-10-05): 327 of 335 exit-1 errors from the reflection hook carry none. *Assumption: 90% target — needs validation against the first 30 days of post-migration telemetry.*
- **`stats` parity on imported history:** 100% equality, against the retired analyzer over the same legacy file, for every key the retired analyzer emits.
- **Wrapper overhead:** ≤ 50 ms p50 added wall time for a foreground run against a no-op fake provider. *Assumption: needs validation by benchmark. Measured interpreter startups for comparison: bash+jq ≈ 10 ms, python ≈ 25 ms, node ≈ 21 ms.*
- **Runtime dependencies for plugin users:** 0 beyond the provider CLI itself. Verified in a clean container that has no Go, Python, Node or jq.
- **Session cost exactness:** 0 difference, at 6 decimal places, between the `cost_usd` that `stats` reports and an independent exact recomputation under FR74 from the same run usage and cached prices. Verified on fixed fixtures in the test suite and once on the owner's telemetry.

---

## 2. Problem Definition

### Target Users
- **Primary:** the owner's Claude Code sessions. Skills and Claude itself ask other agents for second opinions, reviews, cross-checks, expert personas and delegated work.
- **Secondary:** the owner's autonomous automation: the post-commit review hook, the end-of-turn dirty-tree review hook, the end-of-turn reflection hook, the parallel-review step of the code-review skill, and its eval harness.
- **Tertiary:** public plugin users who install the plugin from GitHub and want the same capability.

### Problem Statement
Observed in the owner's telemetry (1,000 records, 2026-09-11 to 2026-10-05): 961 records come from hooks that bypass the wrapper and 39 from the wrapper itself. The two writers share one file but disagree on schema: 9 fields vs 12, and only the hooks record outcome, findings and error reason. A third path, the parallel review, writes nothing. The file is truncated to its last 1,000 lines on every write, which keeps about three weeks. Model pins have been moved by hand across several files at least four times (Observed in version history). Nothing supports continuing an earlier exchange or tracking work left running in the background.

### User Pain Points
- Adding a second agent CLI means rewriting every consumer.
- Telemetry cannot answer "what did this cost" (no token counts) or "why did it fail" (most errors carry no reason), and it cannot take new attributes without editing every writer.
- History older than about three weeks is gone.
- Claude can only fire one-shot prompts. It cannot hold a conversation with another agent or be told when background work finishes.
- The plugin `bin/` directory reaches Claude's Bash tool but not settings hooks (Observed), so shell automation cannot rely on it.
- A token count says little about what a session would cost: most input is read from the prompt cache at a tenth of the input price or less (Observed: 84.7% of all recorded input tokens on 2026-10-08), so the same count can mean very different amounts.
- A run's newest raw step is often a long shell command or a paragraph of message text, more than a one-line row shows (the summary prompt targets a row that truncates around 40 characters). Once the mod labels steps with a model, that spend is a cost like the runs' and has to show beside it.

### Business Case
Cross-model review is a recurring part of the owner's workflow: about 42 dispatches per day (Inferred: 1,000 observed records over 24 days). A single, observable, extensible dispatch layer turns that from three fragile scripts into one maintained tool, makes cost and reliability measurable, and opens the path to more providers without touching consumers.

---

## 3. Solution Scope

### In Scope
- The `agentcli` binary: CLI surface, provider interface, Codex adapter, conversations, jobs, run artifacts, telemetry, profiles, launcher.
- Distribution as a Claude Code plugin from a new public repository: marketplace manifest on the default branch, prebuilt plugin on a CI-built `dist` branch, and a tag and GitHub release for each version.
- A thin mod: one agent type per scenario that dispatches show as native background agents, native tools for Claude, job-completion notices, a status line with running jobs and the session's costs in US dollars (Codex runs and Claude progress summaries), a `/agentcli-jobs` command, and a band above the prompt that shows the runs other callers flag, with their cost so far and a short label of what each is doing.
- A local price cache, refreshed from a public price list and holding only the models the providers offer or have run, plus the models of the recorded model calls, from which `stats` and the status line compute cost (groups Q and R).
- Progress summaries: the mod labels what a run is doing with `claude-haiku-5-5`, shown in the agent row and in the band. Each call is recorded in telemetry as a model call, by a new `usage add` command, and priced and reported per provider by `stats` (group R).
- Spawn notices: the mod says when a run of the session starts, with how long runs of its scenario usually take, from a new `eta` command that reads the telemetry; the spinner of an agentcli agent says what its run is doing (group S).
- A provider-agnostic dispatch skill (a rewrite of the current Codex skill) with evals.
- Migration of every live consumer in the owner's private configuration, import of the legacy telemetry, and removal of the old wrapper and skill.
- Installation on the owner's machine and end-to-end validation (real per-scenario smokes, a real post-commit hook review).

### Out of Scope
- Implementing adapters for other providers (Claude Code, Gemini). The interface must accommodate Claude Code, but no Claude adapter ships in v1. Claude appears only as a model-call provider (group R): agentcli records and prices the mod's calls to it but never runs it.
- Summarizing conversations. (Group R labels a run's newest step, not a conversation.)
- Moving the owner's review and reflection hooks into the plugin. They stay in the owner's configuration and call `agentcli`.
- An importable Go API. The v1 public contract is the CLI, its documented JSON outputs and the mod's tools; Go packages are internal.
- Windows.
- A user-editable profile override file.
- Concurrency caps, queues or a resident daemon.
- The dormant proof-of-concept that references the old wrapper path.
- Fixing why the reflection hook fails so often. v1 only makes the failure reason visible.
- Subscription credits. Session cost is the API list-price equivalent in US dollars, whatever the provider's sign-in method. Claude spend is the same kind of estimate: it can differ from what Bedrock, Vertex or a subscription bills, and it covers only the model calls the mod records, not the Claude Code conversation itself.
- Long-context, Fast, Flex and Batch pricing. Cost uses the standard tier's short-context prices.

### MVP Definition
Every P0 row in FR groups A–S. The mod (group K) is part of v1 but is built after the CLI core (groups A–H) is complete and verified. P1 rows (FR10, FR17, FR36, FR93) are wanted but do not gate v1. v1 is done when every P0 FR is met, the per-scenario smokes and the post-commit E2E pass, the old wrapper is deleted, and both repositories are pushed.

---

## 4. User Stories & Requirements

### User Stories

```
US1 — Second opinion from Claude
As a Claude Code session
I want to ask another agent a question through one tool call
So that I get an independent answer without composing provider-specific flags

Acceptance Criteria:
- [ ] Claude calls the mod tool `ask` with a prompt and optional scenario; it returns a conversation id and a job id immediately
- [ ] When the job finishes, a notice starts a new turn containing the outcome and the output (inline up to 8 KiB, otherwise the first 8 KiB plus the output path)
- [ ] The run appears in telemetry with source `mod`, the scenario, token usage and outcome
```

```
US2 — Continue the conversation
As a Claude Code session
I want to send a follow-up to an earlier answer
So that the other agent keeps the context of the first exchange

Acceptance Criteria:
- [ ] `send <conversation_id> "<follow-up>"` (or the mod tool `send`) resumes the same provider session
- [ ] The follow-up is recorded as turn 2 of the same conversation
- [ ] A follow-up sent while a turn of that conversation is still queued or running is refused with exit 3 and a message naming the active job
```

```
US3 — Autonomous review hook
As the post-commit review hook
I want to run a provider review in the foreground with a hard timeout and a pinned model
So that I keep my current behaviour while gaining unified telemetry

Acceptance Criteria:
- [ ] The hook runs `agentcli exec` with explicit model, effort, timeout 300, source, clean sentinel and material label, and reads the output path from the last stdout line
- [ ] A provider that exceeds the timeout returns exit 124 within the timeout plus 15 seconds, with no surviving descendant processes
- [ ] The hook attaches parsed findings with `annotate`, and `stats` reports them in the findings summary
```

```
US4 — Parallel review that a later shell harvests
As the code-review skill
I want to start a provider review in the background and collect it later
So that both reviewers run concurrently without a hand-rolled nohup pattern

Acceptance Criteria:
- [ ] `review --base <branch> --background --json` returns the job id and paths at once
- [ ] `wait <job_id>` from another shell returns the provider's exit code and prints the output path as its last line
- [ ] The job writes a telemetry record like any other run
```

```
US5 — Install with nothing else to install
As a public plugin user
I want to install the plugin from GitHub and have `agentcli` work
So that I don't need any language runtime besides the provider CLI

Acceptance Criteria:
- [ ] `claude plugin marketplace add <owner>/agentcli` plus `claude plugin install agentcli@agentcli` installs a working binary for linux/darwin × amd64/arm64
- [ ] `agentcli` resolves in Claude's Bash tool, and after the first session start also in settings hooks via the launcher
- [ ] No Go, Python, Node or jq is required
```

```
US6 — Telemetry that grows with new questions
As the owner
I want to attach new attributes to runs, at dispatch time or later, without changing the tool
So that I can answer questions I can't predict today

Acceptance Criteria:
- [ ] `--attr key=value` and `--attr-json key=<json>` at dispatch time land in the run record's `attrs`
- [ ] `annotate <run_id> --attr key=value` after completion is folded into the run by every reader
- [ ] `runs --json` exports folded runs that `jq` can query with no code change
```

```
US7 — One source of truth for numbers
As the owner
I want the existing stats report to keep working over the full history
So that nothing I read today breaks after the migration

Acceptance Criteria:
- [ ] The legacy telemetry is imported once and re-running the import adds nothing
- [ ] `stats --all --json` over the imported history equals the retired analyzer's output for every key that analyzer emits
- [ ] The stats slash command runs `agentcli stats`
```

```
US8 — Session cost at a glance
As the owner, working in a Claude Code session
I want the status line to show what the session's agent runs would cost at API list prices
So that I know the cost even on a subscription, without reading token counts

Acceptance Criteria:
- [ ] With prices cached, the status line shows `💸 Codex $X.XX` for every run of the session, hook runs included
- [ ] When part of the session's usage has no price it shows `Codex ≥$X.XX`; when none of it has, it shows nothing for Codex
- [ ] Compacting the main conversation refreshes the price cache without delaying the compaction
- [ ] `agentcli stats --session-id <id> --all --json` reports the same cost, exact to 6 decimal places
- [ ] A run shown in the band above the prompt shows its cost so far right after its elapsed time
```

```
US9 — Know what a run is doing, and what knowing costs
As the owner, working in a Claude Code session with a screen
I want each run's newest step shown as a short label
So that I can follow work that would otherwise scroll past as long commands, and see what the labels cost

Acceptance Criteria:
- [ ] With summaries on, an agentcli subagent's row streams `» <label>` lines instead of raw entries, and the band above the prompt ends each line with the run's newest label; before a run's first label its band line ends after the cost
- [ ] When a summary fails or its label is rejected, the last label stays and no raw entry is shown, in the row or the band
- [ ] Every billed summary call is recorded as a model call; `agentcli stats --session-id <id> --all --json` reports its cost under `usage_by_provider.anthropic`, and `usage_totals` is unchanged
- [ ] The status line shows `💸 1 job running · Codex $5.82 | Claude $0.03`, and `💸 Claude $0.03` when only Claude has a cost
- [ ] With the `summaries` setting off, or in a session with no screen, no summary call is made and the row and band show the raw entries
```

```
US10 — Know when a run starts and how long it usually takes
As the owner, working in a Claude Code session
I want a notice whenever a run of the session starts, with how long runs of its scenario usually take
So that I see work that hooks and agents start, and roughly when it will end

Acceptance Criteria:
- [ ] A run of the session, whoever started it, shows a toast and a transcript line `agentcli · spawned <scenario> · trigger <source> · ETA ~2m 36s (repo average, 12 runs)`, once per run, reloads included, within the limits of FR96 (the session's 20 newest runs per read, and a record of announced runs that could be written)
- [ ] With no run of the scenario in the session's repository the ETA is the mean of the per-repository means (`ETA ~2m 23s (global average, 8 repos)`); with no run of the scenario at all it is `ETA unknown (no history)`; when the estimate cannot be read it is `ETA unavailable`
- [ ] A run first seen already ended is announced as `… · finished in 3s`, with no ETA, except that in the first listing a load reads successfully, a run that had ended before the second the load started in is not announced
- [ ] `agentcli eta --scenario <name> [--cwd <dir>] --json` prints the estimate the notice uses
- [ ] While an agentcli agent's turn runs, its spinner says `agentcli · <type>`, and with summaries on the run's newest label once one exists
```

### Functional Requirements

#### A. CLI surface and exit contract

| ID | Requirement | Priority |
|----|-------------|----------|
| FR1 | `agentcli exec` starts a new conversation and runs its first turn. The prompt comes from a positional argument, from standard input when the argument is `-`, or from `--prompt-file`. Exactly one prompt source is required. | P0 |
| FR2 | `agentcli review` runs a provider review against exactly one target: `--base <branch>`, `--uncommitted` or `--commit <sha>`. It accepts no prompt. A prompt with a target is refused before spawning, with exit 2. | P0 |
| FR3 | `agentcli send <conversation_id or run_id>` runs the next turn of the referenced conversation. A run id resolves to its conversation. Prompt sources are as in FR1. | P0 |
| FR4 | Common flags on exec/review/send: `--provider` (default `codex`), `--scenario` (default `adhoc`), `--effort`, `--model`, `--sandbox` (`read-only` or `workspace-write`), `--cwd`, `--source` (default `cli`), `--session-id` (default the `CLAUDE_CODE_SESSION_ID` environment value, else empty), `--timeout <seconds>` (default none), `--run-id`, `--clean-sentinel`, `--material-label` (default `ok`), `--attr key=value` (repeatable), `--attr-json key=<json>` (repeatable), `--background`, `--json`, and `--` followed by native provider flags. | P0 |
| FR5 | Foreground contract. **Strategy:** without `--json`, stdout carries exactly one line, the absolute path of the run's output file (so it is also the last line). Warnings and progress go to stderr. The process exit code is the provider's exit code, except for these SDK codes: 2 usage error detected before spawning; 3 conversation busy; 4 run or conversation not found; 6 conversation not resumable; 7 price list unavailable (FR72); 70 internal SDK error; 124 provider timeout; 125 job lost; 127 provider binary not found; 130 run cancelled by request (FR24); 128+n when `agentcli` itself is terminated by signal n. With `--json`, stdout is one JSON object with `conversation_id`, `run_id`, `state`, `outcome`, `sdk_status`, `provider_exit`, `exit_code`, `output_path` and `run_dir`, so a provider exit 2 is distinguishable from an SDK usage error. | P0 |
| FR6 | `--background` admits a job and returns. **Strategy:** exit 0 once the worker is confirmed running. The last stdout line is the job id. With `--json`, stdout carries `{conversation_id, run_id, state, run_dir, output_path}`. | P0 |
| FR7 | `agentcli status [run_id] [--session-id <id>] [--json]` prints one run's state, or with no id the active and recent runs of the given session id (default as in FR4; most recent 20, foreground and jobs). `agentcli wait <run_id> [--timeout <seconds>] [--json]` blocks until the run is terminal, then prints like FR5 for that run and exits with the run's recorded exit code. If its own timeout expires first, it exits 5 and leaves the run running. `agentcli result <run_id>` prints the output file's content. `agentcli cancel <run_id>` requests cancellation (FR24). `agentcli conversations [--json]` lists conversations with provider, turn count, status and last activity. | P0 |
| FR8 | `--dry-run` on exec/review/send resolves profile, flags, conversation and provider plan, prints the plan (argv, stdin source, cwd, environment additions) as JSON, and executes nothing. No run directory, conversation or telemetry is created. | P0 |
| FR9 | `--run-id` is caller-supplied, must match `[A-Za-z0-9._-]{1,128}`, must not be `.` or `..`, must not start with `-` or `.`, and must not already exist (exit 2 otherwise). Generated ids are `r-<UTC yyyymmddThhmmssZ>-<8 lowercase hex>`. Conversation ids are `c-<same shape>`. | P0 |
| FR10 | `agentcli version [--json]` prints the version (from the repository's version file), source commit, `build_seq` and target platform. | P1 |

#### B. Provider interface and Codex adapter

| ID | Requirement | Priority |
|----|-------------|----------|
| FR11 | Each provider declares its capabilities: supported commands (exec, review, resume), review targets, sandbox modes, whether it reports usage, and whether it pre-assigns session ids. A request using an unsupported capability is refused before spawning, with exit 2 and a message naming the capability. | P0 |
| FR12 | Each provider builds a plan from a request: argv, stdin source, working directory, and environment additions/removals. **Strategy:** the provider process inherits the caller's environment by default, then the plan's additions and removals apply. An inherited value the caller sets for the provider reaches it unchanged. | P0 |
| FR13 | Codex plan. **Strategy:** every command runs as `codex exec`. Parent flags come first: `-C <cwd>`; `-s <sandbox>` when set; `-m <model>` when set; `-c model_reasoning_effort=<effort>` when set; `--json`; `-o <output file>`; then native passthrough flags. Then exactly one tail: `-` for exec; `review <target flag>` for review; `resume <provider session id> -` for a follow-up. The prompt is always written to the provider's standard input through a pipe. Review gets an empty standard input. `--skip-git-repo-check` is added when the working directory is not inside a git work tree. `--ephemeral` is never added. | P0 |
| FR14 | Reserved native flags are refused with exit 2 when passed after `--`: `-o`/`--output-last-message`, `--json`, `-C`/`--cd`, `-m`/`--model`, `-s`/`--sandbox`, `-p`/`--profile`, any `--dangerously-*` flag, `--approve-for-me`, `--ephemeral`, and `-c` overrides of `model`, `model_reasoning_effort`, `profile` or any `sandbox*` key (a provider config profile can set the model and the sandbox). `--sandbox danger-full-access` is refused. | P0 |
| FR15 | Codex event parsing. **Strategy:** read the JSONL event stream line by line as it arrives. On `thread.started` take `thread_id` as the provider session id and persist it immediately (FR18). On `turn.completed` take `usage` (input, cached input, cache-write input, output, reasoning output tokens); a review whose usage is all zeros records usage as null. On `error` or `turn.failed` keep the last message as the error excerpt candidate. Unknown event types are ignored. Unparseable lines are counted, not fatal, and the count is stored in the run's state file as `unparsed_events`. | P0 |
| FR16 | The error excerpt is the last error event message, or, when none exists and the exit is non-zero, the last non-empty line of the provider's stderr. It is stripped of ANSI escapes and truncated to 200 characters. | P0 |
| FR17 | `provider_version` is captured for every run by invoking the provider's version flag. A failure to capture leaves it null and never fails the run. | P1 |

#### C. Conversations

| ID | Requirement | Priority |
|----|-------------|----------|
| FR18 | Every run belongs to a conversation. A conversation record holds its provider (immutable), provider session id, working directory, the defaults of its first turn (scenario, model, effort, sandbox), its ordered list of turn run ids, its active-turn marker, and its status: `busy` while the marker is set, otherwise `idle`; plus `resumable` (true once a provider session id exists). **Strategy:** the provider session id is written to the conversation record as soon as FR15 sees it, before the run finishes. | P0 |
| FR19 | Conversation reservation. **Strategy:** admitting a turn takes an exclusive lock on the conversation, checks that no turn is queued or running, allocates the next turn number, records the new run as the active turn, and releases the lock. The active-turn marker is cleared when that run reaches a terminal state (FR22 ordering). A competing admission sees the marker and exits 3. A marker whose run is `lost` (FR23) is cleared by the reconciliation that detects the loss. | P0 |
| FR20 | Follow-ups run in the conversation's working directory with its first-turn defaults, unless the follow-up passes flags. Per-turn flags apply to that turn only and do not change the defaults. A conversation with no provider session id is not resumable: `send` exits 6 and names the reason. A conversation whose working directory no longer exists is refused by `send` with exit 2, unless `--cwd` is passed for that turn. A provider rejecting the session surfaces as that turn's provider error. A `send` passing a `--provider` different from the conversation's provider exits 2. | P0 |

#### D. Jobs (background runs)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR21 | Admission and handoff. **Strategy:** with `--background`, the parent fully reads the prompt (including standard input), writes the prompt and the resolved request into the run directory, records state `queued`, starts the worker as a new process session (detached from the caller's terminal and process group), and waits until the state file shows `running` (the worker writes it only after taking the run lock), polling for at most 10 seconds. Only then does it exit 0 (FR6). If the worker fails to start, or does not reach `running` within 10 seconds, the parent kills it, records state `failed` with an error excerpt, clears the conversation marker, appends the run's telemetry record (outcome `error`), and exits 70. | P0 |
| FR22 | State machine. **Strategy:** the state file is the single authoritative record. States are `queued → running → done or failed or cancelled or timeout`, plus `lost`. The worker owns every transition after `queued`. The state also records the worker's pid and, once spawned, the provider's process group id and process start time. Each terminal transition records exit code, outcome and timestamps, and is written only after the output file is final. Order: output file final → state file terminal (atomic replace) → conversation active-turn marker cleared → telemetry record appended. | P0 |
| FR23 | Liveness and loss. **Strategy:** the worker holds an exclusive advisory lock on the run's lock file for its whole life, through a descriptor the provider does not inherit. Any command that reads a non-terminal run (status, wait, cancel, result, send) checks the lock without blocking. The run is lost when the lock is free and either the state is `running`, or the state is `queued` and more than 30 seconds have passed since admission (beyond the 10-second admission window, so an admission in progress is never misread). That command then marks the run `lost` (exit code 125), kills the recorded provider process group only when its leader still exists with the recorded start time (otherwise it kills nothing), clears the conversation marker, and appends the telemetry record. | P0 |
| FR24 | Cancellation. **Strategy:** `cancel` writes a durable cancel request into the run directory. If the run is still `queued` and the worker has not taken the lock, `cancel` marks it `cancelled` itself; a worker that then starts finds the terminal state or the cancel request, exits without spawning the provider, and leaves the state untouched. Otherwise it sends SIGTERM to the worker. The worker terminates the provider's process group: SIGTERM, a 5-second grace period, then SIGKILL. The worker then finalizes as `cancelled` (exit 130). A cancel request that arrives after the provider has already exited has no effect: the worker finalizes with the provider's result. A `cancel` on a terminal run is a no-op and exits 0 with its terminal state. | P0 |
| FR25 | Jobs survive the end of the Claude Code session that started them. There is no daemon and no concurrency limit. | P0 |

#### E. Foreground execution

| ID | Requirement | Priority |
|----|-------------|----------|
| FR26 | A foreground run holds the run lock and maintains its state file exactly like a job, so `status` and `wait` from another shell see it. The provider runs in its own process group. **Strategy:** on `--timeout` expiry, terminate the whole group (SIGTERM, 5-second grace, SIGKILL), drain the pipes for at most 5 seconds, record outcome `timeout`, and exit 124. When `agentcli` receives SIGINT/SIGTERM in the foreground, it forwards termination to the provider group the same way and records `cancelled`. It exits 130 if a cancel request (FR24) exists for the run, otherwise 128+n. With `--timeout 300` the command returns within 300 seconds plus 15 seconds of shutdown margin. | P0 |

#### F. Run artifacts

| ID | Requirement | Priority |
|----|-------------|----------|
| FR27 | Home directory: `AGENTCLI_HOME`, else `$XDG_STATE_HOME/agentcli`, else `~/.local/state/agentcli`. Each run owns `runs/<run_id>/` containing `prompt.md` (the full prompt), `request.json` (the resolved request and plan), `output.md` (the provider's final message), `state.json`, `stderr.tail` (the last 64 KiB of provider stderr) and a lock file. Conversations live in `conversations/<conversation_id>.json`. All paths are fixed when the run is admitted. | P0 |
| FR28 | Outcome classification: `error` when the exit is non-zero and the run is not timeout/cancelled/lost; `timeout`; `cancelled`; `lost`; `empty` when the exit is 0 and the output file is missing or empty; `clean` when the whole output, whitespace-stripped and lowercased, equals the `--clean-sentinel` likewise normalized; otherwise the `--material-label` value. | P0 |
| FR29 | A failure to write the telemetry record or `stderr.tail` never changes the exit code; it prints one warning line on stderr. A failure to write the terminal state file also leaves the exit code unchanged and prints a warning; other readers then reconcile that run as `lost` (FR23). | P0 |

#### G. Telemetry v1

| ID | Requirement | Priority |
|----|-------------|----------|
| FR30 | Telemetry files are `telemetry/<YYYY-MM>.jsonl` under the home directory, one per UTC month, append-only, never truncated or rewritten. Each line is a record of kind `run`, `annotation` (FR32) or `model_call` (FR81). A record goes to the file of the UTC month in which it is appended (imported records excepted, FR37). **Strategy:** each record is written with a single write call while holding an exclusive lock on the telemetry lock file, waiting at most 5 seconds for the lock (after which the record is skipped with an FR29 warning). Before appending, if the file does not end in a newline, a newline is written first. | P0 |
| FR31 | Run record fields: `v` (1), `kind` (`run`), `run_id`, `ts` (UTC start, RFC 3339), `provider`, `provider_version`, `command` (`exec`, `review` or `send`), `scenario`, `model`, `model_source`, `effort`, `effort_source`, `sandbox`, `source`, `session_id`, `conversation_id`, `turn`, `cwd`, `background` (bool), `exit_code`, `outcome`, `duration_ms`, `timeout_s`, `output_file`, `output_bytes`, `error_excerpt`, `usage` (object or null), `provider_session_id`, `attrs` (object). `model_source` and `effort_source` are `flag`, `profile` or `provider-default`. A `model_call` record has its own fields (FR81). | P0 |
| FR32 | `agentcli annotate <run_id or output path> --attr key=value --attr-json key=<json>` appends an annotation record `{v, kind: "annotation", run_id, ts, attrs}`. An unknown run id exits 4. | P0 |
| FR33 | Fold rules for every reader. **Strategy:** read month files in ascending order and lines in file order; that append order is the replay order. A windowed read covers every month file from the window start's month through the current month (a run's record is always appended at or after its `ts`). A run's `attrs` start from its run record. Each later annotation for the same run shallow-merges its attrs into it, one top-level key at a time, later value wins, and `null` is stored as a value (not a deletion). Annotations are folded before any date filter; the filter applies to the run's `ts`. An annotation that precedes its run record in append order (a job annotated while running) is applied once that record is read; one whose run never appears creates nothing. Model calls (FR81) are folded apart from runs: they take the same time window (by `ts`) and the same session filter (by `session_id`), a repeated `call_id` keeps the first, and a `model_call` line without a non-empty `call_id`, `ts`, `provider` or `model` is unparseable. Records with an unknown `v` or `kind`, and unparseable lines, are skipped and counted: `runs` reports the counts in one stderr line, `stats --json` in a `skipped` object (`unknown_kind`, `unknown_version`, `unparseable`). | P0 |
| FR34 | `agentcli runs [--days N or --all] [--json]` emits folded runs as JSONL (default window 7 days), one object per run, for ad-hoc analysis with `jq`. | P0 |
| FR35 | `agentcli stats [--days N or --all] [--json]` (default window 7 days) emits the same top-level keys and value semantics as the retired analyzer: `total`, `empty`, `span`, `by_source`, `by_source_status`, `instrumentation`, `reliability`, `outcomes`, `duration_ms`, `findings`, `review_findings_proxy`, `window_days`. **Strategy:** a run's legacy-style `status` is derived from its outcome (`error`, `timeout`, `cancelled`, `lost` → `error`; `empty` → `empty-output`; others → `ok`), except imported records, which keep their original `status`. A null outcome (imported records that predate it) counts as `no-outcome`. The findings summary reads `attrs["review.findings"]`, or `findings` on imported records. It adds the keys `by_provider`, `usage_totals`, `usage_by_model` (FR70), `usage_by_provider` (FR85), `skipped` (FR33), `unpriced_models`, `missing_prices`, `prices_checked_at` (FR75) and `model_calls_by_source`, and no other. `model_calls_by_source` is an object keyed by a model call's `source`, in name order, each value `{calls, input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, cost_usd}`, counted after the fold keeps one record per `call_id` (`cost_usd` is null when none of the source's calls is priced); the text output prints one `model calls by source:` line with the counts, when there is any. Model calls (FR81) feed only the usage and price keys (FR75, FR85) and `model_calls_by_source`; every other key counts runs only. | P0 |
| FR36 | Consumer attribute keys are namespaced by convention, e.g. `review.findings`. A key is any string. The value is any JSON value. | P1 |
| FR37 | Legacy import (a maintenance script in the repository, not a CLI subcommand). **Strategy:** read the legacy file line by line. Each line becomes a run record with `v: 1`, `kind: "run"`, `provider: "codex"`, `run_id: "legacy-" + first 16 hex of sha256(raw line + "\n" + zero-based occurrence index of that exact line within the file)`, the legacy fields mapped by name (fields the legacy record lacks, such as `command`, are null), `findings` moved to `attrs["review.findings"]`, `attrs.legacy: true`, and the record placed into the month file of its `ts`. A record whose `run_id` already exists is skipped, which makes the import idempotent. The import holds the telemetry lock for its duration. | P0 |

#### H. Profiles

| ID | Requirement | Priority |
|----|-------------|----------|
| FR38 | Built-in profiles per scenario, shipped in the binary. Precedence: explicit flag, then profile, then provider default. **Strategy:** defaults for the codex provider: `second-opinion` model gpt-6.1-sol, effort high, sandbox read-only; `code-review` model gpt-6.1-sol, effort high, sandbox read-only; `cross-check` model gpt-6.1-sol, effort high, sandbox read-only; `expert-persona` model gpt-6.1-sol, effort xhigh, sandbox read-only; `delegation` model gpt-6.1-sol, effort medium, sandbox workspace-write; `adhoc` none. A scenario name not in the table runs with no profile and is recorded as given. | P0 |

#### I. Distribution and launcher

| ID | Requirement | Priority |
|----|-------------|----------|
| FR39 | The repository's default branch holds the source and a marketplace manifest whose single plugin, `agentcli`, has source `{github, repo: <owner>/agentcli, ref: dist}` with no pinned sha. | P0 |
| FR40 | CI checks every push to the default branch and builds the `dist` branch only for a release (FR93). **Strategy:** the dist workflow runs on every push. Its `check` job builds the dist tree, scans that tree for personal data and runs `scripts/release-gate.sh`, whose answer decides whether the `publish` job runs; its `ci` job calls the whole ci workflow (`ci.yml`), the one definition of the tests. The `publish` job needs both jobs: it runs only for a release, only after the whole ci workflow has passed on the release commit, in a concurrency group that serializes releases and never cancels one in progress. It builds static binaries for linux/darwin × amd64/arm64 with cgo disabled and path trimming, embedding source commit and `build_seq` (the default branch's commit count, computed from a full-history checkout), and publishes them as FR93 says. The single orphan commit on `dist` contains the plugin manifest (no version field; msgate FR6 amends this), `bin/agentcli`, the four binaries, `SHA256SUMS`, the skill, the mod, commands and hooks. The former check that the built commit is still the default branch's HEAD is gone: a release must publish even when the default branch has moved on, and a rerun always runs after it has. What keeps an older run from overwriting `dist` is the source `dist` holds (FR93). A local `make dist` reproduces the same tree inside Docker. | P0 |
| FR41 | `bin/agentcli` in the plugin is a POSIX shell shim that maps `uname -s` (`Linux` → linux, `Darwin` → darwin) and `uname -m` (`x86_64`/`amd64` → amd64, `aarch64`/`arm64` → arm64) to one of the four binaries and executes it with all arguments. An unsupported platform prints the supported list and exits 70. | P0 |
| FR42 | The plugin declares a SessionStart hook that runs `agentcli link --quiet`. | P0 |
| FR43 | `agentcli link [--quiet]`. **Strategy:** resolve this plugin's authoritative install path from the Claude Code installed-plugins record. Under an exclusive lock, write the launcher (default `~/.local/bin/agentcli`) via temp file plus rename, as a POSIX shell script that executes that install's `bin/agentcli` and records its `build_seq`. It writes only when: the launcher is absent; or the existing launcher is an agentcli launcher with a lower `build_seq`; or it names an install path that no longer exists. It never overwrites a file that is not an agentcli launcher (for example a pipx-installed entry point), and says so unless quiet. `AGENTCLI_NO_LINK=1` makes it a no-op. A missing `~/.local/bin` is created. The install record used is the one for `agentcli@agentcli`, preferring user scope. When no record exists (for example a development build), `link` exits 4 with a message unless `--target <path to an agentcli shim or binary>` is given, in which case that path is used with the running binary's `build_seq`. A launcher whose target no longer exists prints that the agentcli plugin is not installed and exits 127. | P0 |
| FR93 | Each released version has a tag `v<VERSION>` and a GitHub release, and nothing else is published. A release is a commit whose subject is exactly `chore: release <V>`, whose diff against its first parent touches only `VERSION`, and whose `VERSION` is `<V>`, a Semantic Versioning 2.0.0 version with no pre-release part; `scripts/release-gate.sh` decides this, and a push whose last commit is not one publishes nothing. **Strategy:** the publish job (FR40) runs `scripts/publish-release.sh`, which does these in order, skipping what is already done. (1) It looks the release up by its exact tag through the API: found means it exists, HTTP 404 means it does not, any other failure stops the run. (2) It pushes the build as one orphan commit tagged `v<V>`, which names a tree with the plugin at its root, unless the tag exists; an existing tag is reused after checking that it points at a dist build of the source commit, and is never rebuilt or moved. (3) It creates the release on that tag. (4) It force-pushes `dist` to the tagged commit last, unless `dist` holds a build of a commit after the source, which means a newer release is on `dist`; it stops before changing anything when `dist` holds a commit it cannot place in this history. Rerunning a failed run at the same source commit therefore finishes what is missing. The release notes come from git: a line naming the source commit, then the subjects of the commits after the previous version's commit up to the source commit, oldest first, grouped as Features (`feat`), Fixes (`fix`) and Other, without the `chore: release` commits. The previous version's commit is the newest commit that changed `VERSION` and whose `VERSION` differs from the source commit's; when there is none, every commit up to the source commit is listed. | P1 |

#### J. Public-repository privacy

| ID | Requirement | Priority |
|----|-------------|----------|
| FR44 | No tracked file and no `dist` artifact contains a personal email, a home-directory path, or a real provider session/thread id. Recorded provider event fixtures are sanitized with fixed fictitious ids and neutral paths. **Strategy:** a CI test scans tracked files and the built dist tree. It fails on any email not ending in `@users.noreply.github.com` or `@example.com`/`@example.org`, on any `/home/<name>` or `/Users/<name>` path, and on any UUID-shaped id not in the fixtures' allowlist of fictitious ids. Binaries are scanned through their printable strings. | P0 |
| FR45 | Commits use the owner's global git identity. LICENSE is MIT with the owner's name. | P0 |

#### K. Mod (built after the CLI core)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR46 | The mod registers three tools, all executing the plugin's own `bin/agentcli`. `ask` takes `prompt` (required), `provider`, `scenario`, `model`, `effort`, `sandbox`, `cwd`; it always admits a job and returns `{conversation_id, run_id}`. `send` takes `conversation_id` and `prompt` (required) plus the same optional fields, and always admits a job. `jobs` takes `action` (`list`, `status`, `result`, `cancel`) and `run_id` (required except for `list`). Each call passes `--source mod` and the session id. | P0 |
| FR47 | Completion notices for the jobs the mod's tools start (a job started from the CLI belongs to its starter). **Strategy:** each job the `ask` and `send` tools admit gets an entry in a delivery queue before the tool returns, `{run_id, conversation_id, purpose, admitted_at, attempted_at}`, kept in the plugin store under one key per session (`delivery:<session id>`), so two Claude Code processes never write the same key; the key goes when its queue is empty. A timer every 15 seconds, skipping a tick while the previous one runs, reads each pending entry's run by id (`status <run_id> --json`), oldest first and at most 50 per tick, a longer queue in turns that each start after the last entry the previous tick read, so no listing window applies and no entry waits behind unfinished ones. An entry whose write fails at admission is held in memory, written again at every tick and delivered from memory meanwhile; the first such failure of a load shows a toast. For a terminal run it records the attempt, then submits the notice: `agentcli job <run_id> (<scenario>, conversation <conversation_id>) finished: <outcome>`, followed by the output inline if it is at most 8 KiB, otherwise its first 8 KiB plus the output path. An accepted submit removes the entry and shows a toast; a `{ drop }` or a rejection clears the attempt and leaves the entry for the next tick. A run that is no longer found (pruned) leaves the queue, logged once to the debug log; a failed status read leaves the entry, logged once per load. Delivery is at least once: an entry a new load finds with an attempt recorded (the previous load may have stopped between the submit and the removal) is delivered again with ` (possibly repeated)` after the outcome. At session start the mod removes the queues of other sessions whose newest entry is more than 30 days old (FR60's retention default). | P0 |
| FR48 | A status line shows the count of this session's running jobs started by the mod while it is non-zero. `/agentcli-jobs` prints the session's recent jobs immediately, even while Claude is working; the command is not `/agentcli:jobs` because a mod command cannot carry the plugin namespace. | P0 |
| FR49 | Before implementing the mod, confirm the mods API types on the installed Claude Code build. A mismatch with FR46–FR48 is a blocker, reported, not worked around. | P0 |

#### L. Dispatch skill

| ID | Requirement | Priority |
|----|-------------|----------|
| FR50 | The plugin ships a provider-agnostic skill, `dispatch`, replacing the current Codex skill. It covers: the scenarios and their profiles; when to use the mod tools vs the CLI; foreground vs job; continuing conversations; reading results; quoting another agent's findings with attribution and disagreeing openly; never applying another agent's patches unless the scenario is delegation; and cost awareness. It ships trigger evals carried over from the current skill's eval set with renamed targets. | P0 |

#### M. Consumer migration (owner's private configuration)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR51 | Cutover order. **Strategy:** (1) dist published and plugin installed from the GitHub marketplace, launcher present; (2) dispatch skill live and the old skill's references renamed; (3) the three review/reflection hooks migrated; (4) parallel review migrated; (5) analyzer, its slash command and the eval harness migrated; (6) legacy telemetry imported and the old file archived (renamed, no longer written); (7) old wrapper and old skill directory deleted. Each step is verified before the next starts. | P0 |
| FR52 | The post-commit review, dirty-tree review and reflection hooks call `${AGENTCLI_BIN:-$HOME/.local/bin/agentcli} exec` in the foreground with explicit `--model` (gpt-6.1-sol, gpt-6.1-sol, gpt-6-luna) and `--effort` (high for all three), `--sandbox read-only`, `--timeout` (300, 300 and 240), `--source` (`hook-post-commit`, `hook-stop`, `hook-stop-claude-md`), `--scenario` (`code-review`, `code-review`, `claude-md-update`), `--cwd <repo root>`, `--clean-sentinel` (`No material findings.`, the same, `no new entry`) and `--material-label` (`findings`, `findings`, `appended`). They keep their range markers and exit-124 marker advance, take the output path from the last stdout line, and annotate parsed findings as `review.findings`. The reflection hook annotates `reflection.applied` (true only when it wrote to its target file). They no longer delete output files, which now live in the run directory. They stop emitting their own telemetry. Their event preflight checks the agentcli binary instead of the provider. Their test suites stub `AGENTCLI_BIN`. The reflection hook's inherited environment marker reaches the provider (FR12). | P0 |
| FR53 | The code-review skill's parallel mode starts `agentcli review --background --json --scenario code-review --source cr-parallel` with the resolved target, and harvests with `agentcli wait <job_id> --timeout <seconds>`. A files-only review with no git target still skips the provider, as today. | P0 |
| FR54 | The stats slash command and the dispatch skill's stats guidance run `agentcli stats`. The retired analyzer script is deleted. The eval harness measures provider dispatches during an attempt as the count of `agentcli runs` whose `ts` falls inside the attempt window and whose `cwd` is the attempt's repository. | P0 |
| FR55 | The skill-gate registry entry binds `agentcli:dispatch` to Bash commands whose first token, after the existing wrapper/env stripping, matches an optional path prefix followed by `agentcli` or `codex`. Every textual reference to the old skill name is updated: in the code-review, spec-audit, brainstorm, goal-generate and orchestrator-mode skills, in the decision-harvesting rule, in the hook documentation, and in the code-review skill's eval files. | P0 |
| FR56 | The old wrapper script, its test and the old skill directory are deleted after FR51 step 6. No script, hook, command file or skill script in the consumer tree invokes `codex exec`, `codex review` or the provider binary directly. Skill and rule prose reaches providers only through `agentcli`. | P0 |

#### N. Validation

| ID | Requirement | Priority |
|----|-------------|----------|
| FR57 | Real smokes on the owner's machine with the installed binary, one per scenario at its profile defaults (second-opinion, code-review, cross-check, expert-persona, delegation), plus one two-turn conversation and one job with `wait`. Each must end with outcome other than `error`/`lost` and a telemetry record. | P0 |
| FR58 | End-to-end: a real commit in a repository during a live Claude Code session triggers the migrated post-commit hook, which reviews through `agentcli` and records `source: hook-post-commit`. | P0 |
| FR59 | Install check: after installing from the GitHub marketplace, `agentcli` resolves from Claude's Bash tool, and after a session start the launcher resolves from a settings hook. | P0 |

#### O. Retention

| ID | Requirement | Priority |
|----|-------------|----------|
| FR60 | `agentcli prune [--older-than <days>] [--dry-run] [--json]` removes the directories of terminal runs whose `ended_at` is older than the given number of days (default 30, or `AGENTCLI_RETENTION_DAYS`). **Strategy:** a run is removed only when its state is terminal with a parseable `ended_at`, its run lock can be taken without blocking, its state is still terminal and old enough when re-read under that lock, and no conversation's active-turn marker names it. Runs with a missing or malformed state are skipped and counted. Telemetry files and conversation records are never removed. After a run is pruned, every reader (`status`, `wait`, `result`, `cancel`, `annotate`, `send` by run id) treats its id as not found (exit 4); a reader that finds its run directory gone mid-read also exits 4. | P0 |
| FR61 | Automatic retention: the plugin's `SessionStart` hook runs `agentcli prune --auto` after `link`. `--auto` prunes at most once per 24 hours per home (a stamp file under a lock), stops after a 3-second time budget and resumes on a later day, prints nothing unless it fails, and is disabled by `AGENTCLI_NO_PRUNE=1`. | P0 |
| FR62 | `status` without a run id reads a per-session index instead of every run directory. **Strategy:** admission appends the run id to the index of the run's session id (`index/sessions/<session id>`; an empty session id uses a fixed key); `status` reads the newest entries of that one index and skips ids whose run directory is gone. A session without an index has no runs. Runs admitted before the index existed are not listed by session. `prune` removes an index file once none of its runs remain. | P0 |
| FR63 | A conversation record keeps the sources of its defaults (`model_source`, `effort_source`, `sandbox_source`), so `send` does not depend on the first turn's run directory. Conversations written before this field existed fall back to the first turn's request when it still exists, else to `profile`. | P0 |

#### P. Agent types, progress and agent feedback

| ID | Requirement | Priority |
|----|-------------|----------|
| FR64 | The mod registers one background agent type per scenario (`<plugin>:second-opinion`, `:code-review`, `:cross-check`, `:expert-persona`, `:delegation`, `:adhoc`), dispatched through the Agent tool. **Strategy:** a `turn.step` hook answers every model request of these subagents with an agentcli job (`--background`, source `agent`, the scenario's profile), never with a Claude model. The job's prompt is the newest user message of the agent's conversation, system reminders removed; when there is none, the text the turn's `turn.start` carried for that agent and turn. A session that saves no transcript (`--no-session-persistence`) has no messages to read back, which is why the start text is there; it comes second because on a turn a SendMessage woke, it wraps the message in Claude Code's own framing. Every failure answers with a line saying the hand-off failed: the hook's own errors, its `.catch`, a refused tool call inside the subagent, and a registered fallback (cheapest model, one read-only tool, a prompt that makes it say so). The types are offered to the model only once the dispatch skill has loaded in the session, recorded in that session's own state. | P0 |
| FR65 | A SendMessage to such an agent runs `send` on its conversation (agent id → conversation id kept in the plugin store, the 500 most recent). A turn continues the conversation when the store records one for the agent, with or without a saved transcript; a turn whose agent's saved messages hold an earlier answer but whose conversation is no longer recorded answers that it cannot resume instead of starting a new one. `:code-review` reads its whole prompt as the target: `uncommitted`, `base <branch>` or `commit <sha>`. Interrupting the agent (Esc, TaskStop) cancels its run. The answer is the output cut at 64 KiB, led by how the run ended when it did not deliver, and closed by `[agentcli run <run_id> · conversation <conversation_id> · <outcome>]`. **Strategy:** the answer, a hand-off failure included, is handed back through a `SubagentHandback({ message })` call, the only way a subagent's report reaches its caller in Claude Code's auto permission mode; the tool guard lets that one call through. Where the tool does not exist (the other permission modes), the next step of the same turn answers with the same text as plain text. A later step of a turn never starts, continues or waits on a run: it answers with the text the turn already produced. | P0 |
| FR66 | While a run works, the runner appends what the provider does to `runs/<run_id>/progress.jsonl`: one line per entry `{seq, at, kind, text}`, `kind` one of `command`, `message`, `reasoning`, the text folded to one line of at most 500 bytes. `agentcli progress <run_id> [--from <n>] [--json]` prints the entries numbered from `n`; `--json` prints `{run_id, state, next, entries, usage, cost_usd}` (FR79 for the last two). A malformed complete line is an error; an unterminated last line is left for the next read. | P0 |
| FR67 | An agentcli subagent's row shows its run as it works: the step hook waits in 5-second slices and streams each slice's new progress as thinking (shown, not recorded), with an elapsed-time line after 30 seconds with no new progress. With summaries active (FR86), the row streams each summary label as `» <label>` instead of the raw entries, and the elapsed-time line stays (FR91). | P0 |
| FR68 | `exec`, `review` and `send` take `--agent-feedback`; `AGENTCLI_AGENT_FEEDBACK=1` sets its default. The run records `agent_feedback` in `request.json` and `status --json`. It needs a session id: without one the run goes ahead with `agent_feedback: false` and one stderr line saying why. | P0 |
| FR69 | A run of the session with `agent_feedback: true`, not started by the agent types and not yet terminal, shows in a band above the prompt (a `ui.render` hook on `AbovePrompt`): one line per run with its scenario, its source, how long it has run and, as its last part, its newest progress entry (FR66) with summaries off, or its newest label with summaries active (FR91), with no last part before the first label. The 15-second poll finds the runs; the band reads their progress again every 5 seconds and drops a run as soon as a read finds it ended. The lines live in the session's own state (`hookRuns`), so a write redraws the band and a reload keeps it. Only the person sees the band: the run's text never enters the conversation with Claude, and the caller that started it delivers its result. With summaries active (FR86), the run's cut progress text is sent to the Anthropic API for the label (FR87), by the mod and outside that conversation. **Strategy:** a band rather than an agent row. An agent spawned in a chain that the mod's own timer started steps past the mod's hooks, so a model, not the mod, would answer it; only another plugin's timer can spawn a row the mod answers, and the band keeps the feature in this one plugin. | P0 |
| FR70 | Each run record carries `model_used` and `effort_used`, what the provider reports it ran with, read before the run releases its conversation (for codex, the last `turn_context` of its session file; null when there is none). `stats` adds `usage_by_model` (the models of recorded model calls too, FR85); `stats` and `runs` take `--session-id`. The status line adds the session's costs (FR78), read again when another of its runs ends, after a price refresh that changed the cache (FR77) and after a model call was recorded (FR92). | P0 |

#### Q. Session cost

| ID | Requirement | Priority |
|----|-------------|----------|
| FR71 | Price cache. `prices.json` under the home directory holds `v` (1), `source` (the URL it was fetched from), `etag` (the source's entity tag, or null), `fetched_at` (when a body was last downloaded), `checked_at` (when the source last answered a refresh, a 304 included), `unit` (`usd_per_1m_tokens`), `models` (provider → model → `{input, cached_input, cache_write, output}`, each a decimal string in US dollars per million tokens) and `unpriced` (provider → sorted list of wanted models the source gave no usable price). It is written atomically, with the home directory created when missing, and read by `stats` and `prices`. **Strategy:** prices are stored resolved: a cache-read price the source omits is stored as the input price, and a cache-write price the source omits, sets to null or sets to 0 is stored as the input price (the provider charges nothing extra for those writes). A file that cannot be parsed, or carries another `v`, is treated as no cache by every reader. | P0 |
| FR72 | `agentcli prices refresh [--max-age <duration>] [--json]` updates the price cache from the LiteLLM price list (`https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`), or from `AGENTCLI_PRICES_URL` when it is set. Usage errors (exit 2, below) are detected before anything else. **Strategy**, in order: (1) `AGENTCLI_PRICES_URL=off` → exit 0, `ran: false`, `reason: off`. (2) Take `prices.lock` without waiting; held → exit 0, `ran: false`, `reason: busy`. (3) With `--max-age`, a cache whose `checked_at` is younger than the duration → exit 0, `ran: false`, `reason: fresh`; this decision reads the price cache and nothing else. (4) Wanted models, per provider: the provider's catalog (FR76) plus every distinct model of that provider's runs in the whole telemetry (`model_used`, else `model`; empty and `unknown` excluded), and every distinct model of that provider's model calls in the whole telemetry (FR84; empty and `unknown` excluded). A catalog that cannot be read leaves the telemetry models and is reported in `catalog_error`. When no provider has a wanted model, exit 0 with `ran: false`, `reason: nothing_wanted`, no request and no write. (5) GET the source accepting gzip, with a 20-second timeout and a 32 MiB limit on the decoded body. `If-None-Match` carries the cached `etag` only when the cached `source` equals the current one and every wanted model is already in the cache's `models` or `unpriced`. A 304 updates `checked_at` only (`changed: false`, `reason: not_modified`). (6) On a 200, each wanted model takes the entry whose key equals its name exactly and whose `litellm_provider` equals the provider's price namespace (FR76); the models of a provider that declares no catalog, and so no namespace, are unpriced, except those of a model-call provider, whose namespace is fixed (FR84). That entry's `input_cost_per_token` and `output_cost_per_token` must be JSON numbers, otherwise the model is unpriced; `cache_read_input_token_cost` and `cache_creation_input_token_cost` are optional. Every price is parsed from its JSON text as an exact rational and multiplied by 1,000,000, never through binary floating point. (7) When no wanted model got a price, exit 7 and leave the cache untouched. Otherwise write the cache and exit 0 with `ran: true`, `reason: updated`, and `changed` true exactly when `models` or `unpriced` differ from the previous cache. A network error, a timeout, a status other than 200 or 304, a body over the limit or unparseable JSON also exits 7 with the cache untouched. With `--json`, stdout is one object `{sdk_status, exit_code, ran, changed, reason, source, checked_at, priced, unpriced, catalog_error}`, where `priced`, `unpriced` and `checked_at` describe the cache as it stands after the call (`priced` and `unpriced` as sorted `provider/model` lists, empty with no cache, and `checked_at` null with no cache) and an exit 7 carries `sdk_status: prices_unavailable`, `reason: unavailable` and `error`. Without `--json`, stdout is one line `prices: <reason> (<n> priced, <m> unpriced)`, and an exit 7 prints `agentcli: <error>` on stderr instead. A `--max-age` that is not a positive duration such as `24h` or `90m`, an unknown flag or a positional argument exits 2. | P0 |
| FR73 | `agentcli prices [--json]` prints the price cache: its source, `fetched_at`, `checked_at`, each provider's models with their four prices, and the unpriced models. With `--json`, stdout is the cache's fields plus `cached: true`. With no usable cache, it exits 0 and prints `no price cache: run agentcli prices refresh` (`--json`: `{"cached": false}`). | P0 |
| FR74 | Run cost. **Strategy:** for a run with usage whose provider and model (resolved as in FR72 step 4) have cached prices, with `i`, `c`, `w` and `o` its input, cached input, cache-write input and output tokens (a negative count read as 0): the written tokens are `W = w` when `w > 0`; otherwise `W = max(0, i − c)` for a provider that declares implicit caching (FR83), because such a provider that reports no cache writes is taken to have written every uncached input token, as implicit prompt caching does, and `W = 0` for any other provider, whose usage is priced exactly as reported; the ordinary input is `max(0, i − c − W)`; the cost in US dollars is `(ordinary × input + c × cached_input + W × cache_write + o × output) / 1,000,000`. Reasoning tokens are part of output and are not added again. Every run, and every model call (FR81), is priced at its model's current cached prices, short-context standard tier, whatever its date. A run or model call with usage whose model has no cached price is unpriced. Costs are summed as exact rationals and rounded once, half to even, to 6 decimal places when printed. | P0 |
| FR75 | `stats` reports cost for its window and session filter. `usage_totals` and each `usage_by_model` entry add `cost_usd`: the FR74 sum over their priced runs (and, for `usage_by_model`, model calls), a decimal string with 6 decimal places, or null when none of their runs with usage is priced. `usage_totals` also adds `cost_complete`, true exactly when no run with usage is unpriced. The top level adds `unpriced_models` (the sorted models of unpriced runs and model calls, `unknown` included), `missing_prices` (the sorted subset that is in neither the cache's `models` nor its `unpriced` for the record's provider, `unknown` excluded) and `prices_checked_at` (the cache's `checked_at`, or null with no usable cache). Text output keeps the `tokens` lines without the cost keys and adds `cost: $X.XX`, followed by `(incomplete: <models>)` when not complete, and one `cost <model>: $X.XX` line per priced model, rounded half up to cents; a null cost prints no line. `usage_totals`, the `tokens` lines and the `cost:` line cover runs only; `usage_by_model`, `unpriced_models`, `missing_prices` and the `cost <model>:` lines also cover the models of model calls (FR85). | P0 |
| FR76 | A provider may declare a model catalog: the model names it offers and the namespace its entries use in the price list. **Strategy:** the codex adapter declares the namespace `openai` and reads its catalog by running `codex debug models` with the caller's environment and a 10-second timeout, taking each `models[].slug`. A failure to run it or to parse its output is reported by FR72 and never ends the refresh. | P0 |
| FR77 | The mod refreshes prices without making any hook wait. **Strategy:** every refresh runs `prices refresh --json` inside a `$.clock.after(0, …)` callback, at most one at a time per load of the mod: a refresh requested while one is running is queued, at most one queued refresh per load, which drops `--max-age` when any request merged into it had none and starts as soon as the running one ends. `session.start` schedules one with `--max-age 24h`. `session.compact` with trigger `manual` or `auto` and no `agentId` schedules one without `--max-age` and returns `next(e)` at once; other triggers and subagent compactions schedule nothing. A stats read (FR78) whose `missing_prices` names a model this load has not yet refreshed for schedules one without `--max-age`, once per model per load; a request merged into the queued refresh counts as that model's one refresh. A refresh that answers `ran: true` and `changed: true` makes the next poll read the session's stats again. A failed refresh is logged once to the debug log until one succeeds. | P0 |
| FR78 | The status line shows the session's costs, read from `stats --session-id <id> --all --json` as in FR70 (sources, re-read and the Claude cost: FR92), after the running-jobs count and joined to it by ` · `; whatever the line holds, it is led by `💸 `. **Strategy:** the Codex cost is shown as `Codex <amount>` and the Claude cost as `Claude <amount>`, Codex first; two costs are joined by ` | `, so the line reads `💸 1 job running · Codex $5.82 | Claude $0.03`. A null `cost_usd` shows nothing for that provider. A complete cost of at least half a cent shows `$X.XX`, rounded half up; a complete cost above zero and below half a cent shows `<$0.01`; a complete cost of zero shows `$0.00`. An incomplete cost shows `≥$X.XX`, truncated to cents so it stays a lower bound. Rounding works on the decimal string with integer arithmetic, never through binary floating point. | P0 |
| FR79 | `progress <run_id> --json` adds `usage`, what a run that has not ended has used so far, and `cost_usd`, its cost; once the run is terminal both are null, since its session file may already hold later turns of its conversation, and its recorded cost is in `stats`. **Strategy:** a provider may report live usage (an optional capability, FRAME): given the caller's environment, the run's provider session id (from its conversation record) and the run's start, it returns the usage since that start and the model in use. The start passed is the end of the second the run started in: `started_at` is kept to the second, the previous turn of a resumed conversation can end inside that second, and no model call answers within a second. The codex adapter reads its session file, found as in FR70, backwards from the end: the newest `token_count` event's `total_token_usage` minus that of the newest `token_count` stamped before the run's start (zero when there is none), each count floored at 0, and the model of the newest `turn_context`; with no `token_count` after the start there is no usage yet. The model falls back to the run's requested model. `usage` is `{input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens}` or null; `cost_usd` is the FR74 cost of that usage at the cached price (FR71) as a 6-decimal string, or null when there is no usage, no provider session id, no usable cache or no price for the model. A start time, conversation record or session file that cannot be read leaves both null, and a request that cannot be read leaves the model without its fallback; either prints one warning on stderr and never fails the command. | P0 |
| FR80 | The band (FR69) shows each run's cost right after its elapsed time: `agentcli · <scenario> · <source> · <elapsed> · $X.XX · <newest step>`, from the newest progress read's `cost_usd` (FR79), formatted as FR78's complete case (`$X.XX` rounded half up, `<$0.01` above zero and below half a cent, `$0.00` at zero). A null `cost_usd` leaves the line without a cost. The line's last part, `<newest step>`, is the raw newest step with summaries off, and the run's newest label with summaries active (FR91); with summaries active and no label yet, the line ends after the cost (after the elapsed time when the cost is null). | P0 |

#### R. Progress summaries and Claude spend

| ID | Requirement | Priority |
|----|-------------|----------|
| FR81 | Model call record. A model call is one request the mod makes to a model outside any agentcli run: a progress summary (FR87), or a completion another plugin asks of the mod's `agentcli.complete` service (FR97). It is a telemetry record of kind `model_call`, appended like every record (FR30). Fields, in on-disk order: `v` (1), `kind` (`model_call`), `call_id` (the caller's own id given with `--call-id`, or else `m-<UTC yyyymmddThhmmssZ>-<8 lowercase hex>`, minted by `usage add` with the generator of FR9 and the prefix `m`), `ts` (RFC 3339 UTC to the second: the time of the call given with `--ts`, or else the time `usage add` records it), `session_id`, `provider`, `model`, `source`, `run_id` (the run the call was about, or null), `usage` (the shape of a run's usage, FR15; `input_tokens` is the total input, including the cached and cache-write tokens) and `attrs` (string values the caller gave with `--attr`; the key is absent when there are none, and a reader that does not know it passes over it). **Strategy:** model calls are telemetry records, not mod-side counters, for one source of truth: the same file and fold as runs, priced exactly in Go from the same cache, kept when the mod reloads or the conversation resumes (they carry the session id), and visible in `stats`. Fold (FR33): a `model_call` line needs a non-empty `call_id`, `ts`, `provider` and `model`, otherwise it counts as `unparseable`; a repeated `call_id` keeps the first. Model calls are kept apart from runs. They take the same time window as runs, by `ts`, and the same session filter, by `session_id` equality. They never appear in `runs`, `status`, `total`, `by_provider` (the run counts), reliability, outcomes, durations, findings or `by_source`; `stats` counts them per source in their own key, `model_calls_by_source` (FR35). A binary that predates the kind counts these lines as `skipped.unknown_kind`. | P0 |
| FR82 | `agentcli usage add --session-id <id> --provider anthropic --model <model> --source <source> [--run-id <run_id>] [--call-id <id>] [--attr <key>=<value>]... [--ts <RFC 3339>] [--json]` records one model call (FR81). `--call-id` is the caller's own id for the call, matching `^[A-Za-z0-9][A-Za-z0-9:._-]{0,199}$`, so that a call recorded twice under one id counts once (FR33). `--attr` is repeatable: the key matches `^[a-z_]{1,32}$`, the value has at most 200 characters, a key given twice or more than 32 attrs exit 2. `--ts` is when the call was made, an RFC 3339 time truncated to the second in UTC; without it the record takes the time it is written, and the record goes to the month file of its `ts`. **Strategy:** the mod hands over the provider's usage object unchanged and Go does the arithmetic, so the conversion exists once. Stdin is one JSON object holding Anthropic's usage exactly as the mod's `model.complete` returns it: `input_tokens`, `output_tokens`, `cache_read_input_tokens` and `cache_creation_input_tokens`. Any other key is ignored, since the reply's usage object may gain fields. A missing key reads as 0. A value that is not a non-negative integer, or stdin that is not one JSON object, exits 2 before anything is written. `--provider` must be a model-call provider; `anthropic` is the only one, and anything else exits 2. `--session-id`, `--model` and `--source` are required and non-empty (exit 2). `--run-id` is recorded as given. An unknown flag or a positional argument exits 2, like every other command. The record's usage is: `input_tokens` = input + cache read + cache creation, `cached_input_tokens` = cache read, `cache_write_input_tokens` = cache creation, `output_tokens` = output and `reasoning_output_tokens` = 0. When all four counts are 0, nothing is written and the command exits 0 with `recorded: false`. With `--json`, stdout is one object `{sdk_status: "ok", exit_code: 0, recorded: true, call_id: "m-…"}` (`recorded: false, call_id: null` when nothing was written); without it, stdout is the call id on one line, or nothing. A telemetry lock or write failure exits 70, as `annotate` does: the model call is the command's whole job, unlike a run's record (FR29). | P0 |
| FR83 | Provider-gated implicit-cache estimate. FR74's estimated cache write applies only to a provider that declares implicit caching. **Strategy:** the estimate exists because Codex under a ChatGPT sign-in always reports zero cache writes while implicit caching writes every uncached input token. Anthropic reports zero writes when it wrote none, so the same estimate would bill all its uncached input at the cache-write rate and overstate the cost. The cost function takes the declaration as a flag on its usage input (`ImplicitCacheWrites`); when it is false the usage is priced exactly as reported. The declaration is an optional provider capability (FRAME): the Codex adapter declares true, and a provider that is not registered (`anthropic`) or does not implement it is false. Both callers set the flag from the record's provider: `stats` (runs and model calls) and the live cost of `progress` (FR79). Every number for Codex runs stays as it is. A unit test pins that Anthropic-shaped usage with no cache is priced at exactly the input rate. | P0 |
| FR84 | Prices for model calls. **Strategy:** a model-call provider has a fixed price namespace, `anthropic` → `anthropic`, and no catalog, since agentcli never runs it and so has no list of its models to read. `prices refresh` wants, per provider, the models of that provider's model calls in the whole telemetry in addition to the rest of FR72 step 4, with empty and `unknown` excluded; a model is therefore priced by the refresh that follows its first recorded call, and until then its cost is missing (FR75) and the Claude part of the status line is absent. | P0 |
| FR85 | `stats` reports spend per provider. **Strategy:** a public field must not change meaning, so `usage_totals`, the `tokens:` text lines and the `cost:` text line stay runs-only and Claude spend gets its own key. `usage_by_provider` is an object keyed by provider; each value has exactly the shape of `usage_totals` (`input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `runs_with_usage`, `cost_usd`, `cost_complete`), and for a model-call provider `runs_with_usage` counts its model calls with usage. A provider appears when it has at least one run or model call with usage in the window and session filter. Per provider, `cost_usd` is null exactly when none of its records with usage has a price, and `cost_complete` follows the rule of `usage_totals`. `usage_by_model`, `unpriced_models` and `missing_prices` include the models of model calls, so the per-model `cost <model>:` text lines do too (FR75). Model calls reach no other key (FR81). | P0 |
| FR86 | When the mod summarizes. **Strategy:** the plugin manifest declares a `userConfig` field `summaries` (boolean, default true) whose title and description say that it summarizes progress with `claude-haiku-5-5` and sends progress text to the Anthropic API; `register(on, options)` reads `options.summaries`. Summaries are active when `options.summaries !== false` and `(await $.session.surfaces()).length > 0`, read at each summary. Otherwise the mod behaves as before group R: raw entries in the row, the raw newest step in the band, no model call. The setting is on by default so the feature works without setup, and the surfaces check keeps a session with no screen, where nobody reads a label, from spending anything. | P0 |
| FR87 | The summary call. A summary is one `$.model.complete` call: model `claude-haiku-5-5`, effort `low`, at most 40 output tokens and a 15-second timeout, inside try/catch because a rejection counts as a failure (FR89). **Strategy:** the system prompt (FRAME) asks for a label of 3–5 words in the present tense that names the file, command or function, as one line with no quotes, markdown or final period, because a row truncates around 40 characters. The prompt lists the window, oldest first: the run's progress entries (FR66) since the previous summary request, at most the 10 newest, each formatted as the band formats a step and cut to 300 characters. The previous label, when there is one, is sent with an instruction to say something new. `claude-haiku-5-5` met the target more often and costs less per call than `claude-haiku-4-5` in the benchmark (Appendix A). | P0 |
| FR88 | Summary cadence and limits. **Strategy:** a request is made when the run's first entry arrives, then each time 3 new entries have arrived since the last request, so a run that works through many small steps is labelled a third as often as it moves. At most one call is in flight per run and at most 2 across the session, which bounds both spend and concurrency. An `api-error` answer with status 429 pauses all summary requests for 60 seconds. A request that is due while the in-flight caps are full or the pause is on is deferred, never dropped: it is made once a slot frees or the pause ends, with the window as it stands then (the entries since the last request, at most the 10 newest). Until then the display keeps what it shows: the band keeps its last label (no step part before the first label), and the row streams nothing new, the 30-second elapsed-time line covering liveness (FR91). | P0 |
| FR89 | Label rules and failures. The reply is cleaned in this order: it is trimmed; it is rejected, as a failure, if it then holds `\n` or `\r`; the other control and ANSI characters are stripped; and it is rejected, as a failure, when it is then empty, starts with a markdown marker (`#`, a backtick, `*`, `-` or `>`) or is over 100 characters. `First\nSecond` is therefore rejected. **Strategy:** a failure is a rejected call, any answer that is not an answered reply (`isAnswered: false`, a 429 included) and a rejected label. On a failure the display keeps the previous label (FR91): the row streams nothing new and the band keeps its line. A raw entry is often a long command or a paragraph of message text (Section 2), so while summaries are active none reaches the row or the band, and the 30-second elapsed-time line shows that the run is alive. The failure goes to the debug log once per run. | P0 |
| FR90 | Recording the calls. After every completion whose usage has any non-zero count, an answered reply and an `empty-reply` alike, the mod runs `agentcli usage add --session-id <session id> --provider anthropic --model claude-haiku-5-5 --source mod-summary --run-id <run id> --json` with the usage object as stdin (FR82). **Strategy:** a billed call is recorded whether or not its label is used, so a reply rejected for its label is recorded too, and a completion with no usage is not. Recording goes through the command rather than the mod writing the file, so one writer owns the format. The display never awaits it, so a held telemetry lock cannot slow a row. An exit 0 increments a module counter, `usageAdds` (FR92). A recording that fails (a non-zero exit or a throw) is kept, with its usage and run id, in a module-level pending list, oldest first, at most 100 entries. Each poll, before `showStatus` reads the costs, retries the pending recordings in order and stops at the first that fails again, which stays first; a retry that exits 0 increments `usageAdds` like a first recording and sends the same usage JSON as stdin. A new failure while the list is full drops the oldest pending entry, a permanent loss: it is logged to the debug log, and the mod shows one toast per load, under its name, `some summary costs could not be recorded; the Claude cost is a lower bound`. A failure goes to the debug log once, until one recording succeeds. | P0 |
| FR91 | Display with summaries active. **Strategy:** **agent row** (FR67): raw entries are not streamed; each label streams as thinking `» <label>`. The row never awaits a summary inside its slice loop: it starts the call and yields the label on the first slice after the call settles, so the 5-second slice cadence never waits on the model; the 30-second elapsed-time line stays. **Band** (FR69, FR80): the line's last part is the run's newest label instead of its newest step; until the first label exists the line ends after the cost, with no step part. **Failure** (FR89): the row streams nothing new and the band keeps its last label, or no step part before the first; a label that settles after a failure streams as `» <label>`. Neither the row nor the band shows a raw entry while summaries are active. The agent row's first line `agentcli · <type>`, the elapsed-time line and the final answer are unchanged. | P0 |
| FR92 | Status line costs and re-read. **Strategy:** `showStatus` takes the Codex cost from `usage_by_provider.codex` and the Claude cost from `usage_by_provider.anthropic`; when `usage_by_provider` is absent from the answer, the Codex cost comes from `usage_totals`, as before group R. Each cost is formatted by FR78 and a null cost is omitted. The line is `💸 <jobs> · Codex $5.82 | Claude $0.03`: ` | ` only between costs, ` · ` between the jobs text and the costs, and `💸 Claude $0.03` alone is valid. `showStatus` reads `stats` again when the module counter `usageAdds` (FR90) changed since the last successful read, in addition to the triggers of FR70; the counter's value is captured before the read, as the price-change flag is (FR77), so an add that lands during the read triggers another read. "Claude $" is an API list-price estimate, like the Codex cost: it can differ from what Bedrock, Vertex or a subscription bills, and a call cut by its timeout may have been billed without being recorded, so the value is a lower bound. | P0 |

#### S. Spawn notice, ETA and spinner

| ID | Requirement | Priority |
|----|-------------|----------|
| FR94 | `agentcli eta --scenario <name> [--cwd <dir>] [--json]` estimates how long a run of the scenario started in a directory takes, from the whole telemetry history (no `--days` or `--all`). `--scenario` is required: without it, as with an unknown flag or a positional argument, the command exits 2. `--cwd` defaults to the working directory, and a relative one is made absolute, because telemetry records absolute directories. **Strategy:** the samples are the run records (FR31) of that scenario with a non-empty `cwd` and a `duration_ms` that reads as a number (a JSON number, or a string holding an integer), whatever their outcome; model calls (FR81) never count. A directory's symbolic links are resolved first (a directory that is gone stays as written), so two spellings of one directory share a key; its repo key is then the trimmed stdout of `git -C <dir> rev-parse --path-format=absolute --git-common-dir`, which a work tree, its subdirectories and its linked worktrees share; when that fails for any reason (the directory is gone, it is not a repository, git is missing, a non-zero exit) or prints nothing, the key is the resolved directory itself. Each distinct directory is resolved once per invocation. The target key is the key of `--cwd`. When samples have the target key, `basis` is `repo`: the estimate is their mean `duration_ms`, `samples` their count and `repos` 1. Otherwise, when there are samples, `basis` is `global`: the estimate is the mean of the per-repository means, so every repository weighs the same and the busiest one does not decide the result; `samples` counts all samples and `repos` the repositories. With no samples, `basis` is `none`, the estimate is null and `samples` and `repos` are 0, with exit 0. The estimate is rounded to the nearest millisecond, half away from zero. With `--json`, stdout is one object `{sdk_status, exit_code, scenario, repo, basis, eta_ms, samples, repos}`, `repo` being the target key whatever the basis. Without it, stdout is one line, `ETA ~2m 36s (repo average, 12 runs)`, `ETA ~2m 23s (global average, 8 repos)` or `ETA unknown (no history)`, with `1 run` and `1 repo` in the singular; the duration is `<s>s` under a minute, otherwise `<m>m <s>s`, the seconds floored, as the mod formats elapsed time. | P0 |
| FR95 | The spinner of an agentcli agent says what its run is doing. **Strategy:** a `ui.render` hook on the `Spinner` component. When the spinner's request id is the agent id of an agentcli subagent whose turn is running in this load of the mod, the hook draws it with its message set to the run's newest label while summaries are active (FR86) and one exists, otherwise `agentcli · <type>`; the message is the bare label, without the row's `» ` mark. A message the engine already set on that spinner (a compaction's, say) stays: the label or the type goes in only when there is none. Every other spinner, and that agent's once its turn has ended, is drawn untouched. The mod keeps, for the load, the run and the type each running turn's agent follows, from the admission of the turn's job to the turn's end. A label that settles for a run such an agent follows asks for a redraw (`$.ui.invalidate('ui.render')`). The hook writes `Spinner raised for <request id> with the word <word>` to the debug log for every spinner, once per request id until the mod's code is loaded again, since a render hook can run often (Open Questions). | P0 |
| FR96 | Spawn notices. Each run of the session is announced once, with a toast and a transcript line: `agentcli · spawned <scenario> · trigger <source> · <tail>`, where `<scenario>` and `<source>` are the run's own values. For a run that is going, the tail is its ETA, which the mod writes from `eta --json` (FR94) in the wording of the command's text form: `ETA ~2m 36s (repo average, 12 runs)`, `ETA ~2m 23s (global average, 8 repos)` or `ETA unknown (no history)`; when the ETA cannot be read (`eta` exits non-zero, fails to run, gives no answer within 10 seconds, or prints output that is not such an object, including a `samples` or `repos` count that is not a non-negative integer) it is `ETA unavailable`. **Strategy:** a run is recorded as announced before its notice is shown, in the session state key `spawnNotified` (the newest 500 run ids), so it is announced at most once, across the admission path, the listings and reloads. That holds while the record can be written: when the write fails, the run is still announced once for that load, and a later load may announce it again. The runs the mod admits itself, an agent type's turn and the `ask` and `send` tools, are announced by work a timer starts right after admission, so the tool result and the turn never wait for the notice. An agent type's run has its type name as scenario and `agent` as source, which is what the type passes; for `ask` and `send` (source `mod`) the mod reads the run's scenario and source with `status <run_id> --json`, since `send` reuses the conversation's first-turn scenario, which the tool does not know at admission; when that read fails, the next listing announces the run. Every other run (a hook's, or one started from the CLI with the session id) is found by reading `status --json --session-id <id>` every 5 seconds, the band's refresh period. That listing holds the session's 20 newest runs (FR7), so when more than 20 runs start between two reads, the oldest of them that the mod did not announce at admission get no notice. That read is its own, apart from the 15-second poll (FR47), which keeps its job: a hook run can end before a 15-second poll sees it, and `status` reads only the session's index (FR62). A listing that exits non-zero, fails to run or cannot be parsed is a failed scan. A scan records every run of its listing as announced before it shows any notice, so a notice that fails cannot leave a run to a later scan; a listed run without a scenario, a source or a state is skipped, neither recorded nor announced, until a listing gives it all three. The first listing of a load (each session start) that succeeds is a baseline: its runs that are not terminal are announced, and its terminal runs are announced as finished only when their `ended_at` falls in or after the second the load started in (run timestamps carry no fractions); the others stay silent, so a load or a reload does not announce runs that ended before it. A failed scan sets no baseline. After the baseline, a run not announced before is announced: with its ETA when it is not terminal, and with the tail `finished in <d>` and no ETA lookup when it is already terminal. `<d>` is the time from `started_at` (else `admitted_at`) to `ended_at` in the format of FR94's text form; the tail is `finished` alone when a timestamp is missing or unreadable. The ETA lookup runs `[bin, 'eta', '--scenario', <scenario>, '--cwd', <session cwd>, '--json']`, the session cwd being the `cwd` of the `session.start` event: the run listing carries no cwd, so a run that works in another repository gets the estimate of the session's repository. Five failures go to the debug log once: a failed scan, until a scan succeeds; and, once per load, a failed `status <run_id>` read after an `ask` or `send` admission, a failed ETA lookup, a notice that cannot be shown and a skipped listed run. A failed write of `spawnNotified` is logged every time it happens. | P0 |
| FR97 | The mod serves completions to other plugins as `$.agentcli.complete({ request, meta })`, so their model calls are recorded like the mod's own (FR81). **Strategy:** the `engine.create` hook adds the noun `agentcli` as a fixed object of methods, `{ complete }`, since a noun holding anything but functions makes the engine unload the plugin; a hook on `agentcli.complete` answers it. `request` is a `$.model.complete` request passed through unchanged (cache marks and timeout included), and the answer is that call's reply, unchanged. `meta` is `{ contract, callId, provider?, attrs? }`: `contract` must be 1; `callId` matches the pattern of FR82's `--call-id`; `provider`, when given, must be `anthropic`; `attrs` is an object of strings, of which the service keeps at most 16 whose key matches `^[a-z_]{1,32}$` and whose value has at most 200 characters, dropping the rest without refusing the call. The model is called once per call: no retry, and a `callId` already being served is refused. A refusal is one of two forms, `agentcli:pre-model:<why>` (the model was not called and can no longer be: `contract`, `call-id`, `unsupported-provider`, `request`, `duplicate`, `cancelled`, `abandoned-<kind>`, `re-entry`) or `agentcli:after-model:<why>` (the model may have been called: `throw`, `timeout`, `refused`), with `<why>` matching `[a-z0-9-]+`; a call the service holds no state for is refused with `agentcli:unknown-state:<kind>`, which a caller reads as ambiguous. After a billed reply the call is recorded without being awaited, as a best-effort `usage add` (FR82) with `--source mod:<calling plugin>` (the caller as the host names it), `--call-id` the caller's `callId`, `--ts` the start of the call, the session read at the start, the caller's attrs plus `model_requested` and `model_inferred` (`env` when `ANTHROPIC_DEFAULT_HAIKU_MODEL` names the model, `table` when the mod's own alias table does). `make verify-plugin` loads the plugin in a model-free `claude -p` and fails when the noun is not mounted; it runs before every push that changes `plugin/`. | P0 |

### Non-Functional Requirements
- **Performance:** wrapper overhead within the success-metric target. Startup does not read the full telemetry history (only `runs`, `stats`, `eta` and a `prices refresh` past its `--max-age` gate do; the gate itself reads only the price cache). `progress` reads a provider session file from its end and stops at the run's start, so a long session file does not slow the band. Telemetry writes are one append. The display never waits for a summary or for its `usage add` (FR90, FR91). *Assumption: overhead target needs validation by benchmark.*
- **Security:** never `danger-full-access`. Reserved native flags are blocked (FR14). Prompts and outputs stay local under the home directory with user-only permissions (directories 0700, files 0600). Telemetry never records the prompt text, nor the text sent to or returned by a model call (FR81). The downloaded price list is untrusted input: its size is capped and its numbers are parsed strictly (FR72).
- **Portability:** linux and darwin on amd64 and arm64. Static binaries with no cgo. POSIX `sh` for shims and the launcher.
- **Reliability:** state transitions are atomic replaces. Telemetry survives crashes mid-write (FR30). A crashed worker is detected as `lost` (FR23). No path can leave a conversation permanently busy.
- **Privacy:** FR44 for the public repository. No telemetry or artifact leaves the machine through the binary: its only network request is the unauthenticated GET of the public price list by `prices refresh`, which carries no user data. With progress summaries on (FR86), the mod sends each run's progress text to the Anthropic API to be labelled: at most the 10 newest entries since the last request, each cut to 300 characters (FR87). That text can hold commands, file names and message fragments of the run. The `summaries` setting turns it off, and a session with no screen sends nothing. The recorded model call holds usage only (FR81).
- **Accessibility:** not applicable. The product is a CLI, a mod that draws a status line, toasts, transcript lines, the band above the prompt and the message of an agentcli agent's spinner and otherwise uses Claude Code's own agent rows, and a skill. No visual UI beyond Claude Code's own.
- **Compatibility:** codex-cli 0.159.3 behaviours are the reference (Appendix A). Claude Code with mods and plugin `bin/` support (installed build 2.1.289).

---

## 5. User Experience

### User Flow
1. Install: `claude plugin marketplace add <owner>/agentcli`, then `claude plugin install agentcli@agentcli`. The next session start writes the launcher.
2. Claude asks another agent: it calls the `ask` tool. The tool returns ids at once. Claude keeps working. When the job ends, a toast appears and a new turn brings the outcome and output.
3. Claude follows up: `send` on the same conversation. The answer arrives the same way as turn 2.
4. A hook reviews a commit: it runs `agentcli exec` in the foreground, gets the output path, decides what to surface, and annotates findings.
5. The owner inspects: the status line for the session's costs (Codex runs and Claude summaries), `agentcli prices` for the cached prices, `agentcli stats` for the standing report, `agentcli eta` for how long a scenario's runs usually take, `agentcli runs --json | jq …` for any new question, `agentcli conversations` and `agentcli status` for live work.

### Design Considerations
- One verb per intent (exec, review, send) and one id per thing (run/job id, conversation id).
- Output paths are always absolute. A foreground run's (and `wait`'s) last stdout line is the output path, and a job admission's last stdout line is the job id, so shell callers stay one-liners.
- Every machine-facing command has `--json`.
- Failure is always explicit: no zero exit with empty output is reported as success (outcome `empty`).

---

## 6. Technical Considerations (High-Level)

### Architecture Impact
New public repository plus migration of the owner's private configuration. Deep modules, each behind a small interface:
- **Runner:** plan → spawn → stream-parse → finalize, for foreground and worker modes alike.
- **Provider adapter:** capabilities, plan building, event parsing, and optionally a model catalog, live usage and a declaration of implicit caching (FR83). Adding a provider means adding one adapter.
- **Store:** runs, conversations, state files and their locks.
- **Telemetry:** append, fold, `runs` export, `stats`, the model calls recorded by `usage add` (FR81, FR82), and the ETA estimate of `eta` (FR94).
- **Prices:** the price cache, the price-list parser and the cost of a run or model call (FR71–FR75, FR83, FR84), behind a small interface that `stats`, `prices` and `progress` call.
- **Launcher/link:** install resolution and the launcher file.

The mod and the skill sit outside the binary and contain no dispatch logic.

### Testing Decisions
Test external behaviour, not internals.
- **Primary seam:** the CLI process. Run the built binary against a fake provider placed first on PATH that replays recorded event streams and exit codes. Assert stdout, exit code, run-directory files, state transitions and telemetry lines.
- **Adapter seam:** `--dry-run` plans assert the exact Codex argv/stdin/cwd for every command shape. The event parser is fed the recorded, sanitized Codex streams (exec, exec review, resume, model-not-supported error).
- **Telemetry fold seam:** fold rules (ordering, month boundary, null values, unknown kinds, torn lines) tested as a module.
- **Lifecycle:** job admission, cancel (queued and running), timeout with descendant processes, worker crash → lost, concurrent `send`. Runs on Linux in Docker and on macOS in CI.
- **Mod:** Claude Code's plugin test runner.
- **Prices and cost:** the CLI process against a loopback test server named by `AGENTCLI_PRICES_URL` and a fake provider that answers `debug models`. The cost rule, the decimal handling and the rounding are tested as a module against exact oracles. The mod's cost text and refresh triggers run in the plugin test runner.
- **Model calls:** `usage add` through the CLI process (stdin, normalization, exit codes, the telemetry line) and the fold of `model_call` lines through `stats`. The implicit-cache gate is tested as a module against exact oracles, including Anthropic-shaped usage priced at exactly the input rate.
- **Summaries:** the summarizer runs in the plugin test runner, whose kit answers `model.complete` and `session.surfaces` beneath the plugin (FRAME); `session.surfaces` defaults to empty, so a test is headless unless it opts in.
- **ETA:** `eta` through the CLI process, over telemetry written into the sandbox home and real temporary git repositories, a linked worktree and plain directories; the estimate as a module, with the repo-key function passed in so a test names which directories share a repository.
- **Spawn notice and spinner:** the plugin test runner, whose kit records the debug log and the redraw requests and draws the engine's spinner from its message, else its word (FRAME).
- **Consumers:** the existing hook test suites, updated to stub `AGENTCLI_BIN`.
- **Real smokes:** FR57–FR59 on the owner's machine.
- **Prior art:** the PATH-stub pattern used by the current hook suites and by the current wrapper's telemetry test.
- Confidence comes from black-box tests on every exit code and state in FR5/FR22, plus real-provider smokes. Unit tests stay below that seam only where edge logic needs it (fold rules, id validation, flag reservation).

### Dependencies
- codex-cli (reference 0.159.3) on the user's PATH.
- Claude Code with plugin `bin/`, settings hooks and mods (installed 2.1.289).
- GitHub (public repository, Actions for CI on Linux and macOS runners).
- Docker on the developer machine (golang image) for build and test. No language toolchain is installed on the host.
- The LiteLLM price list on GitHub (public, no account), read by `prices refresh`.
- For progress summaries only: the mods API's `model.complete` and `session.surfaces`, and through them the Anthropic API. Without them, or with `summaries` off, the mod shows raw entries and no model call is made.
- For the spawn notice and the spinner: the mods API's `ui.render` hook on the `Spinner` component, `ui.invalidate`, `ui.toast`, `ui.log` and the session state (`$.state`, key `spawnNotified`).
- `git` for `eta`'s repo key (FR94). It is optional: without it, each directory is its own key.

### Risks & Mitigations
| Risk | Impact | Mitigation |
|------|--------|------------|
| Codex changes its JSONL event format | High | Parser ignores unknown events; recorded fixtures pin known shapes; provider_version in every record makes drift visible |
| Mods API changes between Claude Code builds | Med | Mod holds no logic; FR49 type check before implementation; CLI fully usable without the mod |
| Plugin `bin/` not on settings-hook PATH | High | Launcher (FR43) and `AGENTCLI_BIN` override; verified by FR59 |
| `dist` force-push publishes a stale build | Med | Only a release publishes, one at a time, and `dist` never moves back past a newer release (FR40, FR93); `build_seq` prevents launcher downgrade (FR43) |
| A fix reaches no install until a release | Low | By design: a push to `main` publishes nothing, and a release is cut only when the owner asks (FR93). `AGENTS.md` sends the agent to the release guide at <https://raw.githubusercontent.com/JustAzul/agent-docs/main/plugin-releasing-dist.md> before any release or version change |
| Prompts stored on disk contain sensitive content | Med | User-only permissions; same machine and trust as the provider's own session store; never in telemetry |
| Process-group semantics differ on macOS | Med | Lifecycle tests on a macOS CI runner |
| Hook exceeds its 600-second budget | High | FR26 bounded shutdown; FR30 bounded lock wait |
| The price list changes its schema or renames models | Med | A refresh that prices no wanted model keeps the previous cache and exits 7 (FR72); `prices` shows what is cached and from where |
| The price list carries a wrong price | Med | `prices` shows the source and every cached price; `AGENTCLI_PRICES_URL` can point at a corrected copy |
| Estimated cache writes exceed real ones (a prefix too short to be cached) | Low | The rule is documented in FR74; a provider that reports writes is priced from what it reports, and the estimate applies only to providers that declare implicit caching (FR83) |
| Summary labels are vague, too long or in the wrong tense | Low | In the benchmark of 54 calls per model, `claude-haiku-5-5` gave 42 labels of 3–5 words and 48 of at most 40 characters (Appendix A). Label rules reject the malformed ones (FR89), the previous label is sent so the next says something new (FR87), and any failure keeps the previous label (FR89) |
| "Claude $" is an estimate, not an invoice | Med | It is the API list price of the recorded calls, documented as such (FR92, Out of Scope); it can differ from Bedrock, Vertex or subscription billing. A call cut by its timeout may have been billed without being recorded, so the figure is a lower bound |
| Progress text leaves the machine for the Anthropic API | Med | On by default, off with the `summaries` setting; a session with no screen sends nothing; each entry is cut to 300 characters; the privacy requirement and the README say so (FR86, NFR Privacy) |
| Telemetry grows with one line per summary | Low | Estimated from the benchmark at about 2,000 model-call lines over today's 2,644 records; lines are small and append-only. A binary that predates the kind reports them as `skipped.unknown_kind` (FR81) |
| The Anthropic API rate-limits the summaries | Low | At most 1 call in flight per run and 2 per session, one request per 3 entries, and a 60-second pause after a 429 (FR88); a failure keeps the previous label, and the elapsed-time line shows the run is alive (FR89) |
| The network blocks the price list's host | Low | `AGENTCLI_PRICES_URL` names a mirror or `off`; the last cache keeps working |

---

## 7. Open Questions
One open question: whether Claude Code raises the `Spinner` component for the rows of agents a plugin answers (FR95), and under which request id. The debug line `Spinner raised for <id> with the word <word>` answers it in a real session.

Deferred execution items, not decisions:
1. ~~Mods API shape on the installed build~~. Deferred to FR49 because it can only be confirmed against the build's type definitions at implementation time; a mismatch is a reported blocker.
2. ~~Mod poll interval~~. Set to 15 seconds (FR47); recalibrating it is a one-value change.

---

## 8. Appendix

### A. Measured facts (2026-10-04, codex-cli 0.159.3, Claude Code 2.1.289)
- Parent flags `-C`, `-s`, `-m`, `-c`, `--json`, `-o` before `review <target>` and before `resume <id> -` work from any cwd.
- `review` with a target rejects a prompt (exit 2) and reports all-zero usage.
- `thread.started.thread_id` from turn 1 is accepted by `resume`; turn 2 recalled turn-1 content.
- With `--json`, a model-not-supported failure arrives as stdout `error` and `turn.failed` events; stderr held only an informational line.
- `turn.completed.usage` carries input, cached input, cache-write input, output and reasoning output tokens.
- Plugin `bin/` is on the Bash tool's PATH; a settings-hook probe found it absent and `CLAUDE_PLUGIN_ROOT` unset.
- The installed-plugins record (version 2) maps `name@marketplace` to entries with scope, install path and version.
- Marketplace plugin sources accept `github` with `ref` and optional `sha`.
- Telemetry baseline: 1,000 records over 24 days, about 300 bytes each; median duration 24.4 s, max 815.9 s.

Measured for session cost (2026-10-08, codex-cli 0.159.3):
- Codex's `input_tokens` includes the cached and cache-write tokens, and its `output_tokens` includes the reasoning tokens: a session file's `total_tokens` equals input plus output, and Codex derives non-cached input as input minus cached. OpenAI's prompt-caching guide states the same partition: each input token is billed at the uncached, cached or cache-write rate, never twice.
- OpenAI prices cache writes at 1.25× the uncached input price and cache reads at 0.1× (0.05× for gpt-6.1-sol) for GPT-5.6 and later; GPT-5.5 and earlier charge nothing extra for writes. In implicit caching mode, the guide's worked example writes every uncached input token of a request to the cache.
- OpenAI list prices for gpt-6.1-sol per million tokens: $2 input, $0.10 cached input, $2.50 cache write, $10 output.
- Under ChatGPT sign-in, every recorded run reports `cache_write_input_tokens: 0` (703 runs with usage), while cached input is 84.7% of all input.
- `codex debug models` prints the model catalog as JSON in under 0.1 s. Of its 10 slugs, two (`gpt-reserve`, `codex-auto-review`) are not in the price list.
- The LiteLLM price list (3.1 MB, about 154 KB gzipped) keys models by name with a `litellm_provider` field, gives prices per token as JSON numbers (for example `1e-07`), and is served with an ETag. Its prices for gpt-6-astra, gpt-6.1-sol and gpt-6-luna match OpenAI's list prices. Its gpt-5.5 entry has no cache-write price.

Measured for progress summaries (2026-10-08), a benchmark of 54 `model.complete` calls per model over 6 recorded runs, with the system prompt of FRAME:

| | `claude-haiku-5-5` | `claude-haiku-4-5` |
|---|---|---|
| Labels of 3–5 words | 42 of 54 | 33 of 54 |
| Labels of at most 40 characters | 48 of 54 | 29 of 54 |
| Cost per call | $0.000061 | $0.00044 |
| Median latency | 784 ms | 686 ms |

- The price list gives `claude-haiku-5-5`, under `anthropic`, per million tokens: $0.10 input, $0.50 output, $0.01 cache read and $0.125 cache write.

### B. Glossary
- **run:** one provider invocation; the unit of artifacts, state and telemetry.
- **job:** a run executed in the background; its id is its run id.
- **conversation:** the ordered runs sharing one provider session.
- **turn:** a run's position within its conversation.
- **provider:** an agent CLI (codex now; claude next). A model-call provider is the exception (see **model-call provider**).
- **adapter:** a provider's implementation inside agentcli.
- **provider session:** the provider's own session/thread id.
- **session id:** the id of the Claude Code session that dispatched a run (distinct from the provider session).
- **scenario:** the caller's label for a run's purpose.
- **profile:** a scenario's default settings.
- **source:** who dispatched the run.
- **outcome:** the classification of a finished run.
- **annotation:** attributes appended to a run after the fact.
- **attrs:** a run's free-form attribute map.
- **price cache:** the local copy of the prices of the wanted models (FR71).
- **wanted models:** the models a provider offers plus those its runs used and those its model calls used (FR72, FR84).
- **unpriced model:** a wanted model the price list gives no usable price, or the model of a run or model call with no cached price.
- **missing price:** a model used by a run or model call that the price cache lists neither as priced nor as unpriced.
- **estimated cache write:** uncached input counted as written to the prompt cache when a provider that declares implicit caching reports no writes (FR74, FR83).
- **session cost:** the FR74 cost of a Claude Code session's runs at API list prices, and, per provider, of its recorded model calls (FR85).
- **model call:** one request the mod makes to a model outside any run, recorded as a `model_call` telemetry record (FR81).
- **model-call provider:** a provider that is only called, never run, such as `anthropic`; it has a fixed price namespace and no catalog (FR84).
- **implicit caching:** a provider's habit of writing every uncached input token to its prompt cache while reporting no writes (FR83).
- **summary label:** the three-to-five-word line the mod asks a model for to say what a run is doing (FR87).
- **repo key:** the absolute common git directory of a directory, shared by a work tree, its subdirectories and its linked worktrees, or the directory itself when git cannot resolve it (FR94).
- **ETA:** the estimated duration of a run of a scenario: the mean duration of that scenario's runs in the same repository, else the mean of the per-repository means (FR94).
- **spawn notice:** the toast and transcript line that announces a run of the session once, with its ETA (FR96).

### C. Prior art consulted
- JamesPrial/go-plugin-release: orphan release branch for Go plugin binaries.
- cexll/myclaude codeagent-wrapper: Go multi-backend wrapper.
- xunzhimeng/one-code-cli: per-run artifacts, session persisted before launch.
- alexeygrigorev/heru: unified event envelope.
- yelban/codex-orchestrator: detached exec runner.
- nothintoulouse/agentcli: exact-argv tests, Codex format drift.
- openai/codex-plugin-cc: job commands.

### D. Independent review dispositions
Two independent reviews (spec v2 and v3) were applied. Adopted:
- adapter-owned output file;
- reserved passthrough flags;
- launcher in place of a version symlink;
- caller-supplied run ids;
- fold ordering;
- torn-line guard;
- import ids;
- conversation reservation;
- durable prompt handoff;
- lock-based liveness;
- cancellation ownership;
- terminal-state ordering;
- process-group timeouts;
- full exit contract;
- admission JSON;
- forward-only launcher;
- serialized publishing of releases;
- an environment-aware plan for the next provider;
- PII scan of dist and ids;
- macOS lifecycle tests.

Cut:
- provider-version caching;
- the profile override file;
- the launcher's cache-glob fallback.

Narrowed: the public contract to the CLI/JSON surface.
