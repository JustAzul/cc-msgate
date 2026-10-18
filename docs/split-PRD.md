# Product Requirements Document

**Project / Feature**: msgate split — the agentcli plugin becomes the engine `msgate` (repository `cc-msgate`) and the agents plugin `msgate-agents`
**Date**: 2026-10-16

---

## 1. Executive Summary

### Problem Statement
agentcli ships as one Claude Code plugin that holds two products: an engine that runs agent CLIs (Codex today), records every run with its cost and serves other plugins, and a set of ready-made agents (second opinion, code review and others). Another plugin cannot use the engine without taking the agents along, the engine knows the agents' scenarios and profiles, and the job notices that the engine's own tools promise can be lost when more than 20 runs start between two polls.

### Proposed Solution
Split agentcli into two plugins. The engine, `msgate`, lives in a new public repository, `cc-msgate`. It runs and resumes runs, records telemetry and cost, answers every agent row that a run backs, and exposes all of it to other plugins only through its interface `$.msgate`, without knowing who uses it. The agents plugin, `msgate-agents`, is the current repository renamed: it registers the ready-made agent types, hands their profiles to the engine as data, and ships the dispatch skill. The engine ships 1.0.0 with only the new names, the owner's consumers move to them in the same cutover, and a later phase gives hook runs their own agent rows.

### Business Impact
- Other plugins (the `followups` plugin today) use the engine's runs, cost recording and model calls without installing agents they do not use.
- The engine stops changing when an agent's model, effort or sandbox changes: profiles move to the agents plugin and reach the engine as data.
- No job result started by the engine's tools is lost, however many runs a session starts.
- Hook results reach Claude for every outcome, not only for findings, so a clean review and a failed review are visible in the conversation.

### Success Metrics
- **Telemetry records readable after the cutover:** 100% of the records written before it. Baseline Observed 2026-10-16: 9,414 records (943 in `2026-09.jsonl`, 8,471 in `2026-10.jsonl`). Verified by FR60: `msgate stats --all` and `msgate runs --all` against the same outputs of agentcli saved just before the cutover.
- **Consumers that still invoke the old names after the cutover:** 0. Baseline Observed: 30 files under the owner's configuration that name agentcli, 2 hook entry scripts that load its hook cores without naming it, and the `followups` plugin. Verified by the text search of FR61.
- **Silent-break paths left open:** 0 of the 8 listed in Appendix C, each verified by its own test case.
- **Tool-started jobs whose finish notice is lost while their session is alive:** 0. Baseline Observed (agentcli FR47 and agentcli FR7): notices come from a listing of the session's 20 newest runs, so a job that falls out of that window before a poll sees it terminal gets no notice. Verified by a test with 25 jobs ending between two polls (FR1–FR3).
- **Hook outcomes that reach Claude:** 100% of hook runs (clean, findings, failure once per state). Baseline Observed: only findings and the reflection hook's write reach Claude, from the post-commit review hook and the end-of-turn review hook; clean results and failures end silently. Verified by the hook test suites and the end-to-end check of FR63.
- **Engine plugin tests and Go tests on every push of `cc-msgate`:** 100% green before release, as today for agentcli. *Assumption: the plugin test runner (`claude plugin test`) is run locally before every push that changes the plugin (the `AGENTS.md` rule), not in CI — CI has no Claude Code; needs validation against the CI image if it is moved there.*

---

## 2. Problem Definition

### Target Users
- **Primary:** the owner's Claude Code sessions, where Claude asks other agents for second opinions, reviews, cross-checks, expert personas and delegated work.
- **Secondary:** the owner's automation — the post-commit review hook, the end-of-turn review hook, the end-of-turn reflection hook, the code-review skill's parallel mode and its eval harness — and the owner's `followups` plugin, which records its model calls through the engine.
- **Tertiary:** public users and plugin authors who install the engine from GitHub, with or without the agents plugin.

### Problem Statement
Observed in the current repository and the owner's configuration on 2026-10-16:
- One plugin, `agentcli@agentcli`, carries the Go binary, the mod and the dispatch skill. The mod both answers the agent types and serves `$.agentcli.complete` to `followups`.
- The binary holds the scenario profiles (model, effort, sandbox per scenario) and defaults `--scenario` to `adhoc`, an agent type's name. The engine cannot change without the agents changing, and the reverse.
- Job finish notices come from a 15-second listing of the session's 20 newest runs; a `{ drop }` from the host counts as delivered.
- Hook runs show only in a band above the prompt; the hook decides what reaches Claude, and today only findings do.

### User Pain Points
- A plugin that wants model-call cost recording must install agent types it never uses.
- Moving an agent to a new model means releasing the engine.
- A busy session can lose a job's result: Claude never hears that the job ended.
- A clean review or a failed review is invisible to Claude; only findings arrive.
- Hook runs look different from the agents Claude starts, though they are the same kind of work.

### Business Case
The owner's workflow starts about 186 runs a day (Observed: 2,983 run records from 2026-10-01 to 2026-10-16), and the mod records about 305 model calls a day for progress labels (4,881 model-call records in the same days). The engine is the piece other plugins already depend on (`followups`), and the ready-made agents are the piece that changes most often. Splitting them lets each change at its own pace, makes the engine reusable on its own, and closes the delivery gaps that hide results from Claude.

---

## 3. Solution Scope

### In Scope
- **Phase 1 — Reliable job delivery** (released from the current repository as agentcli 0.6.3, a fix): a persisted per-session delivery queue for the jobs the mod's tools start.
- **Phase 2 — The split and the cutover:**
  - The `cc-msgate` repository, created from the full history of `agentcli`, with the engine plugin `msgate` 1.0.0: the binary, the provider interface with the Codex adapter, runs, conversations and jobs, telemetry, `stats`, `eta`, retention, prices and cost, the completion service, the job tools `run` and `jobs`, finish and spawn notices, the launcher, progress summaries, the band, the spinner of agent rows, and the answering of every run-backed agent row.
  - The engine interface `$.msgate`: `complete`, `backType`, `version`, `run`, `jobs`.
  - The repository `agentcli` renamed to `msgate-agents`, with the agents plugin `msgate-agents` 0.7.0: the six agent types with their profiles, the dispatch skill.
  - New names everywhere, with no aliases: binary, environment variables, state directory, interface, refusal prefixes, command, visible text; `--scenario` becomes `--purpose`.
  - Profiles leave the binary; a run started from the CLI without `--sandbox` runs read-only.
  - Hook delivery for every outcome, and the migration of every consumer in the owner's configuration and of `followups`, in the same cutover.
- **Phase 3 — Hidden rows for flagged runs:** the agents plugin opens an agent row for each flagged run of the session, which the engine answers as appearance only; in the auto permission mode the engine refuses the row's spawn and the run stays in the band; gated on a measured proof in the default, bypass and auto permission modes.

### Out of Scope
- Implementing the Claude Code provider (native plugin interface from the mod; the Agent SDK's stdio protocol or `claude -p` from hooks) and the OpenRouter provider (HTTP). The provider contract keeps room for both; no adapter ships.
- Changing what the reviews and the reflection do (their prompts and models).
- The Reminder Rule Engine of the Claude Harness project.
- Aliases or compatibility shims for the old `agentcli` names.
- A user-editable profile file.
- Running the plugin test runner in CI.
- Windows.

### MVP Definition
Phases 1 and 2: every P0 row in groups A–J and L. The MVP is done when the delivery queue ships in agentcli 0.6.3; `cc-msgate` 1.0.0 and `msgate-agents` 0.7.0 are released; the cutover (FR54) has run with every step verified; every consumer uses the new names (FR61); and the validation rows of group L pass. Phase 3 (group K) starts only after the MVP is done and the proof of FR59 is recorded.

---

## 4. User Stories & Requirements

### User Stories

```
As the owner's Claude Code session
I want a job I start with the engine's run tool to tell me when it ends
So that I act on its result even when the session starts many runs

Acceptance Criteria:
- [ ] A job started with the run tool produces exactly one finish notice while its session is alive, or a notice marked as a possible repeat after a crash
- [ ] 25 jobs ending between two polls produce 25 notices
- [ ] A notice the host refuses ({ drop } or a throw) is retried on the next poll
```

```
As a plugin author
I want to use the engine's runs and model-call recording without the agents plugin
So that my plugin depends on one engine and nothing else

Acceptance Criteria:
- [ ] With only msgate installed, $.msgate.complete records a model call with source mod:<my plugin>
- [ ] $.msgate.run admits a job and $.msgate.jobs returns its result to my plugin
- [ ] $.msgate.version() returns the engine's release version
```

```
As the owner's Claude Code session
I want the ready-made agents to keep working after the split
So that second opinions, reviews and delegation behave as before

Acceptance Criteria:
- [ ] Agent(subagent_type: "msgate-agents:second-opinion") runs a Codex job with gpt-6.1-sol, high effort, read-only, and its report reaches Claude in the auto and default permission modes
- [ ] A SendMessage to that agent continues the same conversation
- [ ] Without the msgate engine installed, the agent types are not offered and the session shows one notice that the engine is missing
```

```
As the owner
I want every hook result delivered to Claude
So that clean reviews and failed reviews are as visible as findings

Acceptance Criteria:
- [ ] A clean post-commit review wakes Claude with a report that says it is clean
- [ ] A failed review is delivered once for its state and not again until the state changes
- [ ] The report names the external model, the run id and the outcome, and escapes harness markup in the output
```

```
As the owner
I want the cutover to keep my history and break nothing silently
So that stats, ETA estimates and every hook keep working under the new names

Acceptance Criteria:
- [ ] Every telemetry record written before the cutover is read by msgate stats and msgate runs
- [ ] No file in the consumer tree invokes agentcli after the cutover
- [ ] Each of the 8 silent-break paths of Appendix C has a passing check
```

```
As the owner's Claude Code session (phase 3)
I want a hook's flagged run to show as an agent row
So that hook work looks like the agents Claude starts

Acceptance Criteria:
- [ ] A flagged run of the session gets one row, opened by the agents plugin and answered by the engine, that streams its progress
- [ ] The run leaves the band once its row is answered
- [ ] Stopping the row hides it and does not cancel the run
- [ ] The row never hands a result back to Claude; the hook delivers it
```

### Functional Requirements

References of the form "agentcli FRn" point at the current agentcli PRD (`docs/PRD.md` of the agentcli repository at 0.6.2). A requirement that names one supersedes or amends it; everything in that document not superseded here carries over to the engine with the renames of group C (Appendix A maps each one).

#### A. Reliable job delivery (phase 1, agentcli 0.6.3)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR1 | Every job the mod's own tools admit (today `ask` and `send`; after the split, `run`) gets an entry in a **delivery queue** before the tool returns: `{run_id, conversation_id, purpose, admitted_at, attempted_at: null}`, `purpose` holding the run's scenario label until the rename of group D. **Strategy:** the queue is persisted in the plugin store under one key per session id (`delivery:<session id>`), so two Claude Code processes never write the same key; the key is removed when its queue is empty. An entry whose write fails at admission is held in memory, written again at every tick and delivered from memory meanwhile; the first such failure of a load shows a toast. At session start the mod removes the keys of other sessions whose newest entry was admitted more than 30 days ago (the retention default of agentcli FR60, after which their runs may be pruned anyway) and logs how many it removed. Amends agentcli FR47. | P0 |
| FR2 | Delivery. **Strategy:** a 15-second timer, skipping a tick while the previous one runs, reads each pending entry's run by id (`status <run_id> --json`), not the session listing, so no 20-run window applies. For a terminal run it writes `attempted_at` to the entry, then submits the finish notice through `$.prompt.submit`; the entry is removed only when the submit is accepted. A `{ drop }` or a throw resets `attempted_at` to null and leaves the entry, which is retried on the next tick (the load knows that submit was not accepted). A run whose id is not found (exit 4: pruned) is removed and logged once to the debug log. A status read that fails leaves the entry and is logged once per load. | P0 |
| FR3 | At-least-once delivery. **Strategy:** an entry that a load finds with `attempted_at` already set (the previous load submitted it and may have stopped before removing it) is delivered again with the notice marked as a possible repeat: ` (possibly repeated)` after the outcome. The notice text is otherwise agentcli FR47's, renamed in phase 2: `msgate job <run_id> (<purpose>, conversation <conversation_id>) finished: <outcome>`, followed by the output inline when it is at most 8 KiB, otherwise its first 8 KiB and the output path. The persistent "notified" set of agentcli FR47 is retired. | P0 |

#### B. Repositories, distribution and releases (phase 2)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR4 | `JustAzul/cc-msgate` is created public as a copy of the full history and tags of `JustAzul/agentcli`; its first commit after the copy removes the agents plugin's parts (agent types, dispatch skill, their tests). Go module path `github.com/JustAzul/cc-msgate`. **Strategy:** a full copy keeps `build_seq` (the commit count) growing past every launcher already written, keeps `release-notes.sh` working from the previous tag, and publishes nothing that is not already public. | P0 |
| FR5 | `JustAzul/agentcli` is renamed to `JustAzul/msgate-agents`; its first commit after the rename removes the engine's parts: the Go module, `cmd/`, `internal/`, `tools/`, `testdata/` except the PII allowlist that FR9's scan reads, `test/e2e/`, the binary build and `plugin/bin/`, and every mod module, type file and plugin test; the agents plugin's mod is a new module holding FR39–FR42 (and FR57 in phase 3), with its own tests. | P0 |
| FR6 | `cc-msgate` publishes marketplace `cc-msgate` with one plugin, `msgate`, sourced from its `dist` branch (agentcli FR39–FR40 and FR93 otherwise unchanged); install id `msgate@cc-msgate`; first release `v1.0.0`. | P0 |
| FR7 | `msgate-agents` publishes marketplace `msgate-agents` with one plugin, `msgate-agents`, sourced from its own `dist` branch built by copying `plugin/` without tests, type files or `tsconfig.json`, plus `LICENSE` and `README.md`; install id `msgate-agents@msgate-agents`; first release `v0.7.0`, with the same version-bump rule as `AGENTS.md`. | P0 |
| FR8 | Neither plugin manifest declares `dependencies` (the existing manifest test keeps refusing the field). The agents plugin finds the engine at use (FR41). | P0 |
| FR9 | Both repositories run the PII scan on tracked files and on the built `dist` tree (agentcli FR44). `msgate-agents` runs the engine's tool at a pinned version: `go run github.com/JustAzul/cc-msgate/tools/piiscan@<version>`; the pin is bumped when the tool changes. Both carry the MIT license and the git identity rule of agentcli FR45. | P0 |

#### C. Names (phase 2, engine)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR10 | The binary is `msgate`: plugin shim `bin/msgate`, platform binaries `libexec/msgate-<os>-<arch>`, hidden worker `msgate _worker <run_id>`, version text `msgate <version> (commit …)`, stderr prefix `msgate: `, usage text naming `msgate`. No `agentcli` binary, shim or launcher is written. Amends agentcli FR10, FR21, FR41. | P0 |
| FR11 | `link` writes the launcher `~/.local/bin/msgate` with the marker line `# msgate-launcher`, resolving the install record of `msgate@cc-msgate`; its lock and temporary files are `.msgate.lock` and `.msgate.tmp-*` beside it. A missing install prints that the msgate plugin is not installed and exits 127. Amends agentcli FR42–FR43. | P0 |
| FR12 | Environment variables: `MSGATE_HOME`, `MSGATE_AGENT_FEEDBACK`, `MSGATE_NO_LINK`, `MSGATE_NO_PRUNE`, `MSGATE_RETENTION_DAYS`, `MSGATE_PRICES_URL`, and the test knobs `MSGATE_TEST_*` (the eleven `AGENTCLI_TEST_*` renamed). No `AGENTCLI_*` variable is read. | P0 |
| FR13 | Home directory: `MSGATE_HOME`, else `$XDG_STATE_HOME/msgate`, else `~/.local/state/msgate`; the layout under it is unchanged (agentcli FR27 and Appendix B). Amends agentcli FR27. | P0 |
| FR14 | Home migration. **Strategy:** every command resolves the home first. When `MSGATE_HOME` is unset, the resolved home does not exist, and the old home of the same tier exists (`$XDG_STATE_HOME/agentcli` when `XDG_STATE_HOME` is set, else `~/.local/state/agentcli`), the command takes a lock file `.msgate-migrate.lock` in the parent directory, checks both paths again, reconciles every non-terminal run of the old home as `status` does (a run whose worker is gone becomes `lost`, agentcli FR23), refuses with exit 1 and a message naming the run when a run is still not terminal after that (so no live worker writes into a moved directory), and otherwise renames the old directory to the new path (one `rename`, same parent). No link is left at the old path. A failed rename exits 1 naming both paths and changes nothing. A command that finds the new home exists never looks at the old one. Every reader locates a run's files from the current home and the run id, never from the absolute paths a run's records hold, so a moved run's output stays readable (FR16). | P0 |
| FR15 | Visible text: finish notice `msgate job …` (FR3); answer trailer `[msgate run <run_id> · conversation <conversation_id> · <outcome>]`; hand-off failure line `msgate: the hand-off to the agent failed, so this is not its answer.`; band and spinner prefix `msgate · `; spawn notice `msgate · spawned <purpose> · trigger <source> · <tail>`; the jobs command `/msgate-jobs`. | P0 |
| FR16 | The plugin's Claude Code state starts empty under the new name: the `summaries` setting, the session state (`hookRuns`, `spawnNotified`) and the store (`agent-conversations`, the delivery queue). This is documented in both READMEs: `summaries` returns to its default (on), an agent row of the old plugin cannot be continued, and finish notices the old plugin had not delivered are not carried over (their results stay readable with `msgate result`). | P0 |

#### D. Purpose and profiles (phase 2, engine)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR17 | `--scenario` is replaced by `--purpose <label>` on every command that took it (`exec`, `review`, `send`, `eta`); default `adhoc`, a neutral label with no meaning to the engine; `--scenario` is an unknown flag (exit 2). On `eta`, `--purpose` is required. Amends agentcli FR4, FR94. | P0 |
| FR18 | Records and outputs use the field `purpose`: `request.json`, a conversation's `defaults`, the telemetry run record, `status --json`, `eta --json`, the `runs` export. **Strategy:** the telemetry fold reads a record's `scenario` as its `purpose` when `purpose` is absent, so `stats`, `eta`, `runs` and every other reader see `purpose` only; `send` and `status` read an old conversation's `defaults.scenario` and an old `request.json`'s `scenario` the same way. Amends agentcli FR18, FR31, FR33, FR34. | P0 |
| FR19 | Profiles leave the binary: the purpose never sets a model, effort or sandbox. Effective settings: a flag's value, else the provider's default; the sandbox, when not passed, is `read-only`. A setting that writes needs `--sandbox workspace-write`. Supersedes agentcli FR38 and amends agentcli FR8 and agentcli FR13. | P0 |
| FR20 | Setting sources. **Strategy:** `exec`, `review` and `send` take `--settings-source <flag\|profile>` (default `flag`); with `profile`, the model, effort and sandbox passed as flags are recorded with source `profile`. `--effort-source <flag\|profile>` sets the source of the effort alone, over `--settings-source`; without `--effort` it changes nothing, and any other value is a flag error (exit 2). A setting not passed is recorded `provider-default`, except the sandbox, recorded `default` when the engine's read-only default applies. `send` keeps reusing a conversation's defaults and their sources (agentcli FR20, FR63); an old conversation whose sandbox is null continues read-only with source `default`. Amends agentcli FR31, FR63. | P0 |
| FR65 | `stats` reports runs by purpose and origin, in place of agentcli FR35's sections that select hook runs and code-review runs. **Strategy:** a run's origin is `plugin` when its source is one the engine's mod writes (`mod`, `agent`, or one starting with `plugin:`), and `cli` for every other source, the default `cli`, hooks and skills included; old records are classified by the same rule. `stats --json` drops `reliability`, `outcomes`, `findings` and `review_findings_proxy` and adds `by_purpose`: an object keyed by purpose in name order, each holding a `cli` and a `plugin` object, present only when that origin has runs of the purpose: `{ runs, outcomes, findings }`. `outcomes` counts each outcome, `no-outcome` when a run has none. `findings` sums, over the runs carrying a `review.findings` annotation, the keys `total`, `critical`, `high`, `medium`, `low` and `violations`, and adds `annotated` (those runs), `with_findings` (those whose `total` is above 0) and `clean` (those whose `total` is 0); it is `{ annotated: 0 }` when no run carries the annotation. The text form shows the same counts per purpose and origin. Every other key of agentcli FR35 is unchanged. Amends agentcli FR35. | P0 |

#### E. Engine tools and notices (phase 2)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR21 | The engine's mod registers two tools, `run` and `jobs` (`mcp__msgate__run`, `mcp__msgate__jobs`), both executing the plugin's own `bin/msgate` with `--source mod` and the session id. `run` takes `prompt` (required), `conversation_id`, `purpose`, `provider`, `model`, `effort`, `sandbox`, `cwd`; without `conversation_id` it runs `exec`, with it `send`; it always admits a job and returns `{conversation_id, run_id, continued}`, `continued` true when it continued the given conversation. `jobs` keeps agentcli FR46's `action` (`list`, `status`, `result`, `cancel`) and `run_id`, with `result` cut at 64 KiB. Supersedes agentcli FR46. | P0 |
| FR22 | Finish notices of `run` jobs go through the delivery queue (FR1–FR3). The status line (running jobs started by the mod, and the session's costs, agentcli FR48, FR78, FR92) and `/msgate-jobs` are otherwise unchanged. Amends agentcli FR47–FR48. | P0 |
| FR23 | Spawn notices keep agentcli FR96 with the renames: `<scenario>` is `<purpose>`, the runs the mod admits itself are a row's turn and the `run` tool, and the ETA lookup is `[bin, 'eta', '--purpose', <purpose>, '--cwd', <session cwd>, '--json']`. Amends agentcli FR96. | P0 |
| FR24 | The band keeps agentcli FR69 and FR80 with the renames, showing the session's flagged runs whose source is not `agent` (the rows of backed types show their own runs). Its rationale ("only another plugin's timer can spawn a row the mod answers") becomes the design of group K. Amends agentcli FR69. | P0 |
| FR25 | Everything else the engine does carries over unchanged except for the names of group C, the field of group D and the `stats` sections of FR65: runs, conversations, jobs, lifecycle and outcomes (agentcli FR1–FR37), retention (agentcli FR60–FR63), progress (agentcli FR66–FR67), agent feedback (agentcli FR68), model used (agentcli FR70), prices and cost (agentcli FR71–FR80), model calls (agentcli FR81–FR85), summaries (agentcli FR86–FR92), `eta` (agentcli FR94) and the spinner (agentcli FR95, now drawing `msgate · <type>`). | P0 |

#### F. Engine interface `$.msgate` (phase 2)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR26 | The engine adds the interface `msgate` through `engine.create` as a fixed object of functions, `{ complete, backType, version, run, jobs }`; each is answered by a hook on `msgate.<method>`, which returns `{ value }` or `{ deny }`. The caller is the plugin the host stamps on the call (`next.origin.plugin`). A refusal reaches the caller as an error whose message starts `msgate:<method>:` (FR27 keeps its own forms). Supersedes agentcli FR97's noun. | P0 |
| FR27 | `complete({ request, meta })` keeps agentcli FR97 with the refusal forms `msgate:pre-model:<why>`, `msgate:after-model:<why>` and `msgate:unknown-state:<kind>`; the body that answers when the hook is not reached throws `msgate:unserved`. Recording source `mod:<calling plugin>`. | P0 |
| FR28 | `version()` resolves to the engine's release version string (for example `1.0.0`). **Strategy:** the interface is versioned by the engine's own SemVer: a change that breaks any method's contract bumps the engine's major version; a new method bumps the minor. | P0 |
| FR29 | `backType({ type, command, promptMode, model, effort, sandbox })` marks the caller's agent type `<caller>:<type>` as backed, so the engine answers its rows (group G). **Strategy:** `type` matches `^[a-z0-9][a-z0-9-]{0,63}$`; `command` is `exec` or `review`; `promptMode` is `free` or `review-target`, and `review` requires `review-target` and the reverse; `model`, `effort` and `sandbox` are optional strings, `sandbox` one of `read-only` and `workspace-write`. Anything else is refused with `msgate:backType:<field>`. The backed types live in the engine's session state, keyed by full type, so an engine reload keeps them; a later call for the same type replaces its data. A plugin can back only its own types, since the full type takes the caller's name. Resolves `{ type: '<caller>:<type>' }`. | P0 |
| FR30 | `run({ prompt, conversation_id?, purpose?, provider?, model?, effort?, sandbox?, cwd? })` admits a job like the `run` tool (FR21) with source `plugin:<caller>` and resolves `{ conversation_id, run_id, continued }`. The engine does not deliver its result to Claude and adds no delivery-queue entry: the caller reads it with `jobs`. | P0 |
| FR31 | `jobs({ action, run_id? })` takes the actions of FR21. **Strategy:** `list` returns the session's runs whose source is `plugin:<caller>`; `status`, `result` and `cancel` refuse a run whose source is not `plugin:<caller>` with `msgate:jobs:not-yours`, so a plugin cannot read or cancel another plugin's or Claude's runs. | P0 |
| FR32 | `make verify-plugin` loads the engine plugin in a model-free `claude -p` and fails when the `msgate` interface is not mounted with its five methods; it runs before every push that changes the engine's plugin. Amends agentcli FR97. | P0 |

#### G. Rows the engine answers (phase 2)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR33 | The engine answers every model request of an agent whose type is backed in the session (FR29), whoever spawned the row (usually Claude, which leaves no spawner). **Strategy:** a `turn.step` hook looks up the agent's type with `$.agent.list()` (cached per agent id per load) and answers only backed types; every other step passes through. The engine never calls `$.msgate` or any other plugin's interface inside a row's step. Supersedes the answering part of agentcli FR64. | P0 |
| FR34 | Prompt of a turn. **Strategy:** the newest user message of the agent's conversation, system reminders removed; when there is none, the text the turn's `turn.start` carried for that agent and the same turn id, reminders removed. A start text heard for an earlier turn is never used. Both empty → the hand-off failure `the prompt is empty`, and no process is started. (The order is the one agentcli 0.6.2 shipped: on a turn a SendMessage woke, the start text wraps the message in Claude Code's framing.) | P0 |
| FR35 | First step of a turn. **Strategy:** with no conversation recorded for the agent, `command: exec` admits `exec --background --json --purpose <type> --source agent --session-id <session> --settings-source profile` with the backed model, effort and sandbox and the prompt on stdin, except that an explicit `effort` in the Agent call that spawned the agent replaces the backed effort in its place, followed by `--effort-source flag` (the Agent call's `tool_use_id` ties it to the spawn; the session's own effort never applies); `command: review` reads the prompt as exactly one target (`uncommitted`, `base <branch>`, `commit <sha>`, the revision matching `^[A-Za-z0-9_][A-Za-z0-9._/~^-]{0,254}$`) and admits `review` with it, refusing anything else with a hand-off failure naming the three forms. With a conversation recorded for the agent in the plugin store (the 500 most recent), it admits `send <conversation_id>` instead, with or without a saved transcript; with saved messages holding an earlier answer and no recorded conversation, it answers that the conversation cannot be resumed and starts nothing. `<type>` is the type's own name without the plugin prefix. Supersedes agentcli FR64–FR65's admission. | P0 |
| FR36 | Following the run keeps agentcli FR67 (progress in 5-second slices as thinking, the elapsed-time line, summaries) and agentcli FR95 (the spinner); interrupting the row (Esc, TaskStop) cancels its run. | P0 |
| FR37 | Answer and delivery. **Strategy:** the answer is the output cut at 64 KiB, led by how the run ended when it did not deliver, closed by the trailer of FR15. It is handed back through `SubagentHandback({ message })`, which the tool guard lets through while refusing every other tool; where the tool does not exist, the next step of the same turn answers with the same text as plain text. A later step of a turn never starts, continues or waits on a run: it answers with the turn's text. Every failure answers with the hand-off failure line. (agentcli 0.6.1–0.6.2 behaviour, renamed.) | P0 |
| FR38 | Step limit. **Strategy:** the engine counts the steps of each turn of a row; from the ninth step on (limit 8) it no longer answers and passes the step down with `next(e)`, so the model the agent type registered as its fallback ends the turn whatever the plugin's own answer did; it logs that once per turn to the debug log. | P0 |

#### H. Agents plugin (phase 2)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR39 | `msgate-agents` registers six background agent types with `$.agent.register`: `second-opinion`, `code-review`, `cross-check`, `expert-persona`, `delegation`, `adhoc`, each with its description and the note that it is used only when the user asks for another agent; the registered fallback (model `haiku`, the one tool `Read`, the project's CLAUDE.md omitted, a prompt that makes it answer the hand-off failure line) answers only when the engine does not. Carries over the type part of agentcli FR64. | P0 |
| FR40 | Profiles, passed to `$.msgate.backType` for each type at every session start: second-opinion — exec, free, gpt-6.1-sol, high, read-only; code-review — review, review-target, gpt-6.1-sol, high, read-only; cross-check — exec, free, gpt-6.1-sol, high, read-only; expert-persona — exec, free, gpt-6.1-sol, xhigh, read-only; delegation — exec, free, gpt-6.1-sol, medium, workspace-write; adhoc — exec, free, no model or effort (provider defaults), read-only. Supersedes agentcli FR38's table. | P0 |
| FR41 | Engine presence. **Strategy:** the agents plugin calls `$.msgate.version()` at session start; when the interface is missing (the call throws because `$.msgate` is undefined), refuses, or answers a version whose major is not 1, it shows one toast per session — `msgate-agents needs the msgate engine plugin 1.x` followed by the version found, if any — logs the error once, makes no `backType` call, and its `agent.offer` hook hides all six types for the session. A `backType` refusal hides that type and is logged. | P0 |
| FR42 | The types are offered to the model only once the dispatch skill has loaded in the session (agentcli FR64's gate, in the agents plugin's own session state) and only when FR41 found the engine. | P0 |
| FR43 | The dispatch skill (`msgate-agents:dispatch`) covers the six types; the engine's tools `run` and `jobs`; the CLI with explicit `--model`, `--effort` and `--sandbox` for each purpose, since the CLI applies no profile (FR19); continuing conversations; reading results; quoting another agent's findings with attribution and disagreeing openly; never applying its patches except for delegation; cost awareness. Its trigger evals carry over with renamed targets. Amends agentcli FR50. | P0 |

#### I. Hook delivery and consumer migration (phase 2, owner's configuration and `followups`)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR44 | `msgate result <run_id \| output path> --report` prints a delivery report for a terminal run. **Strategy:** line 1 `## msgate report · <purpose> · <model> · <outcome>`, `<model>` being the run's `model_used`, else its requested `model`, else the word `default`; then, when the run did not deliver, `ended <outcome>: <error_excerpt>`; then the output, in which every `<` that opens a tag-shaped token (`</?[A-Za-z][A-Za-z0-9_-]*`) is written `&lt;`, so harness markup in the output cannot act as markup; then the trailer of FR15 and `Full output: <path>`. A path argument resolves to its run by the run directory it names. A run that is not terminal exits 5; an unknown run or path exits 4. | P0 |
| FR45 | The three hooks (post-commit review, end-of-turn review, reflection) call `${MSGATE_BIN:-$HOME/.local/bin/msgate} exec` with `--purpose` in place of `--scenario`, explicit `--model`, `--effort`, `--sandbox read-only` and `--timeout` as today (agentcli FR52), always `--session-id`, and `MSGATE_AGENT_FEEDBACK=1`. Their availability check, tests and stub use `msgate` and `MSGATE_*`. Amends agentcli FR52. | P0 |
| FR46 | Hook delivery for every outcome. **Strategy:** a hook ends with `exit 2` and the output of `msgate result <run> --report` on stderr for every outcome — clean, findings, and failure — instead of only for findings; the reflection hook keeps reporting whether it wrote its file. The hooks keep `asyncRewake: true`. A failure (a non-zero exit, a timeout, an empty output) is delivered once per state and not again while the state is unchanged: the post-commit hook's state is the reviewed HEAD range (`last_reviewed_sha` to HEAD); the end-of-turn review's state is the session plus the hash of the working-tree changes (`last_seen_dirty_hash`, which the hook now checks); the reflection hook's state is the session (`claude_md_by_session`, its existing dedup record). When the binary is not available (the hook's availability check fails), the hook delivers one line, `msgate is not available at <path>`, by `exit 2` once per state, instead of ending silently. When `exec` exits non-zero and its last stdout line names no run directory (no run was admitted: a refused flag, a home that cannot be resolved or migrated), the hook delivers `msgate exec failed (exit <code>): <last line of its stderr>` by `exit 2` once per state; to read it, the hooks keep `exec`'s stderr in a temporary file instead of discarding it. Otherwise the run id for the report comes from the run directory named by the last stdout line. | P0 |
| FR47 | The code-review skill's parallel mode runs `MSGATE_AGENT_FEEDBACK=1 msgate review --background --json --purpose code-review --source cr-parallel --model gpt-6.1-sol --effort high --sandbox read-only` with the resolved target and harvests with `msgate wait`; its exit-code table is unchanged. Its eval harness reads `msgate runs --all` and runs `msgate exec --purpose cross-check` with its explicit flags. Amends agentcli FR53–FR54. | P0 |
| FR48 | The stats command becomes `/msgate-stats` running `msgate stats`. Its review summary reads `by_purpose` (FR65) — the `code-review` purpose's `cli` findings and outcomes — in place of `findings` and `review_findings_proxy`; the other keys it reads are unchanged. Amends agentcli FR54. | P0 |
| FR49 | Every mention of `agentcli:dispatch` in the owner's skills, rule and hook documentation becomes `msgate-agents:dispatch`; the skill-gate registry binds `msgate-agents:dispatch` to Bash commands whose first token, after the wrapper and env stripping, matches an optional path prefix and `msgate` or `codex`. Amends agentcli FR55. | P0 |
| FR50 | The owner's settings enable `msgate@cc-msgate` and `msgate-agents@msgate-agents`, know the marketplaces `cc-msgate` (`JustAzul/cc-msgate`) and `msgate-agents` (`JustAzul/msgate-agents`), and no longer name `agentcli@agentcli` or the `agentcli` marketplace. The hook wiring (PostToolUse on `Bash`, Stop, `asyncRewake: true`) is unchanged. | P0 |
| FR51 | The `followups` plugin calls `$.msgate.complete`; its refusal pattern matches `msgate:(pre-model\|after-model):<why>`; its missing-service check matches the error of an undefined `$.msgate.complete`; its labels say "msgate service". Its tests are updated and it is released in the same cutover. | P0 |
| FR52 | The documentation that points at agentcli's code (the mods notes in the owner's configuration) points at the `cc-msgate` paths. | P1 |

#### J. Cutover (phase 2)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR53 | Preconditions, each verified before step 1 of FR54: agentcli 0.6.3 is installed and its delivery queue has shipped; `cc-msgate` 1.0.0 and `msgate-agents` 0.7.0 are released with their `dist` branches built; the consumer changes of group I are written and their tests pass against the new binary; no run in the old home is non-terminal; `agentcli stats --all --json` and `agentcli runs --all` are saved for FR60's comparison. | P0 |
| FR54 | Cutover order. **Strategy:** (1) uninstall `agentcli@agentcli` and remove its marketplace; (2) delete `~/.local/bin/agentcli` and the old plugin's cache directories under the Claude Code plugin cache; (3) add the marketplaces and install `msgate@cc-msgate` and `msgate-agents@msgate-agents`; (4) start a session, which runs `link` (the launcher) and, through the first `msgate` command, the home migration of FR14; (5) apply the consumer changes of group I and release `followups`; (6) restart Claude Code and run the checks of group L. Each step that deletes or overwrites something is confirmed when it runs and verified before the next starts. Supersedes agentcli FR51. | P0 |
| FR55 | Rollback, documented before step 1 of FR54. **Strategy:** (1) uninstall `msgate@cc-msgate` and `msgate-agents@msgate-agents`; (2) with no run of the new home non-terminal, rename `~/.local/state/msgate` back to `~/.local/state/agentcli`; (3) add a local marketplace (a directory holding a marketplace manifest named `agentcli` whose plugin `agentcli` is sourced from `JustAzul/msgate-agents` at ref `v0.6.3`, the tag that points at that version's `dist` commit) and install `agentcli@agentcli` from it; (4) revert the consumer changes in the owner's configuration and in `followups` from their version control. The `dist` branch cannot serve a rollback: after the split it holds the agents plugin. | P0 |

#### K. Hidden rows for flagged runs (phase 3)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR56 | `$.msgate.flaggedRuns()` resolves the session's runs with `agent_feedback: true`, not terminal, whose source is not `agent`, and that have no row the engine has answered: `[{ run_id, purpose, source, started_at }]`. A minor version of the engine (FR28). | P1 |
| FR57 | `backType` accepts `command: 'follow'` with `promptMode: 'run-id'` (a minor version, FR28). The agents plugin registers a hidden type `run` (offered to no model) and backs it so. Its own timer, every 5 seconds, calls `flaggedRuns()` and spawns one `msgate-agents:run` row per new run with the prompt `msgate run <run_id>` and the description `<purpose> · <source>`. A spawn that fails (no Agent tool, a refusal) is logged once per run and the run stays in the band. | P1 |
| FR58 | The engine answers a hidden row only when its spawner is the plugin that backed its type and the prompt names a flagged run of the same session that the engine knows. The spawner is the origin plugin of the spawn, which the engine reads in its `agent.spawn` hook; the agent listing does not carry it. In the auto permission mode the engine refuses the spawn of a hidden row (`{ deny }`), so no row opens and the run stays in the band: that mode does not let a row end with plain text, and a hand-back would deliver the row's text to Claude. In the other modes, **Strategy:** it follows the run like FR36 without admitting one, and ends the turn with plain text (`msgate run <run_id> ended <outcome>`), never with a hand-back: the hook that started the run delivers its result (FR46). The band drops the run once its row has been answered; stopping the row hides it and does not cancel the run, and the band does not show that run again. | P1 |
| FR59 | Phase 3 ships only after a recorded proof, on the installed Claude Code build, in the default, the bypass and the auto permission modes: in default and bypass, a hidden row ends its turn once, delivers nothing to Claude, and the hook's own delivery arrives once; in auto, the spawn is refused, no row opens, nothing reaches Claude, and the hook's own delivery arrives once. | P1 |

#### L. Validation

| ID | Requirement | Priority |
|----|-------------|----------|
| FR60 | History check: after step 4 of FR54, `msgate stats --all --json` equals the saved `agentcli stats --all --json` for every key except the four that FR65 removes, its `by_purpose` equals FR65's rule recomputed over the saved `agentcli runs --all` (each run's `scenario` read as its purpose), and `msgate runs --all` prints the same folded runs as the saved `agentcli runs --all`, line for line, except that each run's `scenario` key reads `purpose`. (`runs` prints folded runs: annotations merge into their run and model calls are not listed, so its line count is not the telemetry line count of 9,414 records on 2026-10-16.) | P0 |
| FR61 | Name check: a text search of the consumer tree (hooks, skills, rules, commands, settings, the `followups` plugin) finds no command, variable, interface or plugin id of the old names; prose that tells the history of the rename is allowed. | P0 |
| FR62 | Real smokes on the owner's machine, through the Agent tool: one per agent type at its profile, a two-turn conversation through SendMessage, and one `run` tool job with its finish notice; in the auto and default permission modes, and once in a session started with `--no-session-persistence`. Each ends with an outcome other than `error` or `lost` and a telemetry record whose `purpose` is the type's name and whose `model_source` is `profile` (except `adhoc`). Amends agentcli FR57. | P0 |
| FR63 | End-to-end: a real commit in a live session makes the post-commit hook deliver its report to Claude by `exit 2` for a clean review and for findings; a forced failure is delivered once and not on the next commit-free Stop. Amends agentcli FR58. | P0 |
| FR64 | Install check: after installing from the GitHub marketplaces, `msgate` resolves from Claude's Bash tool, and after a session start the launcher resolves from a settings hook. With only `msgate` installed, `followups` records a model call through `$.msgate.complete`. Amends agentcli FR59. | P0 |

### Non-Functional Requirements
- **Isolation:** the engine names no agent type, profile, plugin or hook; no rule of the engine selects by a purpose, and the only source values its rules read are the labels it writes itself — `mod` (its tools), `agent` (rows it answers, FR24, FR35, FR56) and `plugin:<caller>` (interface runs, FR30–FR31, where the caller is the name the host stamps) — which also decide a run's origin in `stats` (FR65). The agents plugin reaches the engine only through `$.msgate`; it never runs the binary.
- **Performance:** agentcli's performance requirement carries over. The delivery queue reads one status per pending entry per tick; a session with more than 50 pending entries reads at most 50 per tick, oldest first, each tick starting after the last entry the previous tick read, so no entry waits behind unfinished ones. *Assumption: 50 per tick — needs validation against the owner's busiest session (no queue exists today to measure).*
- **Security:** never `danger-full-access`; a run writes only with `--sandbox workspace-write`, which only the delegation profile and an explicit flag ask for. Interface callers cannot read or cancel other callers' runs (FR31). The report escapes harness markup (FR44).
- **Reliability:** the home migration is one rename under a lock and refuses while a run is not terminal (FR14). Delivery is at least once (FR3). A row never loops: the step limit hands the turn to the fallback model (FR38).
- **Privacy:** agentcli's privacy requirement and FR9 for both public repositories.
- **Portability:** linux and darwin on amd64 and arm64 for the engine; the agents plugin is JavaScript and a skill, with no binary.
- **Accessibility:** not applicable; no visual UI beyond Claude Code's own rows, status line, toasts and band.
- **Compatibility:** Claude Code 2.1.300 (the build the probes and agentcli 0.6.2 ran on) and codex-cli 0.159.3 behaviours as the reference.

---

## 5. User Experience

### User Flow
1. Install: add the marketplaces `JustAzul/cc-msgate` and `JustAzul/msgate-agents`, then install `msgate@cc-msgate` and, for the ready-made agents, `msgate-agents@msgate-agents`. The next session start writes the `msgate` launcher.
2. Claude asks another agent: after the dispatch skill loads, Claude calls the Agent tool with `msgate-agents:second-opinion`. The row streams what Codex does; the report comes back through hand-back (auto mode) or as the agent's final text (other modes).
3. Claude follows up: SendMessage to the same agent continues the conversation.
4. Claude starts a job directly: the `run` tool returns ids at once; a finish notice brings the output when the job ends.
5. A hook reviews a commit: it runs `msgate exec`, then delivers the report for every outcome by `exit 2`; Claude wakes with it.
6. Another plugin records its model calls through `$.msgate.complete`, or starts and reads runs through `$.msgate.run` and `$.msgate.jobs`.
7. The owner inspects with `msgate stats`, `msgate eta --purpose <p>`, `msgate runs`, `msgate prices`, the status line and `/msgate-jobs`.

### Design Considerations
- One engine, many consumers: the agents plugin is one consumer among others and gets no private channel.
- Profiles are data: the agents plugin owns them; the engine records where each setting came from.
- No silent paths: every hook outcome is delivered, every tool job is announced, and every refusal names its reason.
- One verb per intent and `--json` on every machine-facing command, as in agentcli.

---

## 6. Technical Considerations (High-Level)

### Architecture Impact
Two repositories and two plugins. Deep modules, each behind a small interface:
- **Engine — row answering:** the per-turn state machine of a backed row (prompt source, admission per backed data, follow, answer, hand-back or text, replay, step limit). Interface: the `turn.step` and `turn.start` hooks plus the backed-type registry.
- **Engine — delivery queue:** enqueue at admission, poll by id, submit, remove on acceptance, repeat marking. Interface: `enqueue(entry)` and the tick.
- **Engine — interface service:** the `msgate` methods and their validation, caller identity from the host. Interface: the five methods.
- **Engine — Go CLI:** unchanged modules (runner, provider adapter, store, telemetry, prices, launcher/link) plus the home migration, `--purpose`, `--settings-source`, the sandbox default and `result --report`.
- **Agents plugin:** type registration and backing, the engine presence check, the offer gate, the dispatch skill; in phase 3 the hidden-row timer.

### Testing Decisions
Test external behaviour, not internals.
- **Engine mod:** the plugin test runner with the existing kit (`world`, `step`, `startTurn`), extended with a second plugin's identity for interface calls (`next.origin`) and a fake `$.prompt.submit` that can accept, drop or throw. Prior art: the 0.6.1–0.6.2 subagent tests.
- **Engine CLI:** the existing CLI-process tests against the fake provider, for the renames, `--purpose`, the field fold, `--settings-source`, the sandbox default, the home migration (with a non-terminal run present and absent) and `result --report`. Prior art: `test/e2e`.
- **Agents plugin:** its own plugin test runner suite, with a fake `$.msgate` (present, missing, refusing) beneath it.
- **Hooks:** the owner's hook test suites with the renamed stub, which also answers `result --report`.
- **End to end:** a real-session harness running both plugins from their `dist` builds in an isolated home, in the auto and default modes, with and without session persistence (prior art: the 0.6.2 harness).
- Confidence comes from the black-box suites at each seam plus the real smokes of group L.

### Dependencies
- Claude Code with mods, plugin `bin/`, settings hooks with `asyncRewake`, `SubagentHandback` in auto mode (build 2.1.300).
- codex-cli on the user's PATH (reference 0.159.3).
- GitHub (two public repositories, Actions on Linux and macOS for the engine; Linux for the agents plugin), the owner's GitHub account for repository creation and rename.
- Docker with the golang image for the engine's build and tests.
- The `followups` plugin repository (released in the same cutover).

### Risks & Mitigations
| Risk | Impact | Mitigation |
|------|--------|------------|
| A consumer is missed and breaks silently under the new names | High | The 8 silent-break paths of Appendix C each get a check; FR61's text search; FR63's end-to-end |
| The home migration runs while an old worker still writes | High | Refuse while any run is not terminal (FR14); precondition in FR53 |
| Two Claude Code processes write the delivery queue at once | Med | One store key per session (FR1); only that session's process writes it |
| A crash after a notice was accepted repeats it | Low | Marked `(possibly repeated)` with the same run id (FR3) |
| Every hook outcome wakes Claude, adding a model turn per commit and per Stop | Med | Accepted trade-off of the brainstorm; failures once per state (FR46) |
| In auto mode, the plugin's hand-back arrives flagged "classifier unavailable" | Low | Accepted trade-off of the brainstorm; delivery still works (0.6.1) |
| The old plugin's cached copies keep resolving the old skill | Med | The cutover deletes the old plugin's cache directories (FR54 step 2) |
| Rollback after the cutover | Med | FR55, documented before step 1 |
| Claude Code changes the mods API or the nesting rule hidden rows depend on | Med | Phase 3 gated on a recorded proof on the installed build (FR59); FR33's rows do not depend on it |
| Plugin state reset by the rename surprises the owner | Low | Documented in both READMEs (FR16) |

---

## 7. Open Questions

None open. Every decision is locked in the planning record (Appendix D).

Deferred execution items, not decisions:
1. ~~Whether the plugin test runner can run in CI.~~ Out of scope; the `AGENTS.md` rule keeps it a local gate.
2. ~~The 50-per-tick read cap of the delivery queue.~~ A one-value change if the first busy session shows a backlog (NFR Performance).

---

## 8. Appendix

### A. agentcli requirements → their home after the split
Every number in this appendix is an agentcli FR (the agentcli PRD at 0.6.2), not a requirement of this document.
- **Engine, with the renames of group C and D only:** agentcli FR1–FR37, FR60–FR63, FR66–FR67, FR70–FR92, FR94–FR95.
- **Engine, amended here:** agentcli FR4, FR8, FR10, FR13, FR18, FR20, FR21, FR27, FR31, FR33, FR34, FR35 (the `stats` sections, FR65 here), FR38 (precedence only), FR41–FR43, FR46–FR48, FR64–FR65 (answering, admission and delivery), FR68 (flag and field), FR69, FR80, FR96, FR97.
- **Agents plugin:** the types and the gate of agentcli FR64, the target forms of agentcli FR65 (the engine parses them as `promptMode: review-target` data), agentcli FR38's table (FR40 here), agentcli FR50 (FR43 here).
- **Both repositories:** agentcli FR39, FR40, FR44, FR45, FR49, FR93.
- **Superseded:** agentcli FR51 (by FR53–FR55 here), agentcli FR52–FR59 (by groups I and L here).

### B. Contracts preserved without an agentcli FR (Observed in the code)
- Mod timeouts: agent-type admission 30 s; each 5-second `wait` slice 60 s; `prices refresh` 60 s.
- Cuts: the `jobs` tool result 64 KiB; band entries 200 characters; unexpected admission output quoted to 500 characters.
- Id patterns: tool ids `^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`; review revisions as in FR35.
- Run files: `worker.log` (last 64 KiB), `cancel.request`, `progress.jsonl`; prune renames a run to `.prune-<id>` before deleting it.
- Locks and waits: run lock 500 ms polling every 10 ms; `wait` polls every 25 ms and waits up to 10 s for a final state; provider version capture 5 s.
- Session index keys: `_` for an empty session id, `~<hex>` for an unsafe one; `index/lock`.
- Launcher: directory mode 0755, launcher at most 64 KiB, a target with a line break refused.
- Dist build: removes `tests/`, type files and `tsconfig.json`; `SHA256SUMS` covers the binaries.

### C. Consumers and the silent-break paths
Consumers (Observed 2026-10-16): the hook library and its three cores, entry scripts and tests; the code-review skill's parallel mode and eval harness; the stats command; the skill-gate registry; the code-review, goal-generate, orchestrator-mode, brainstorm and spec-audit skills and the decision-harvesting rule, which name the dispatch skill; the mods notes; `settings.json` (enabled plugins, marketplaces); the `followups` plugin (interface call, refusal pattern, missing-service check, labels, tests).

Silent-break paths, each checked in the test cases:
1. The binary missing at the hooks' default path disables all three hooks with no output.
2. A flag the CLI rejects exits 2, which the hooks treat as an ordinary failure.
3. An extra stdout line after the output path breaks the path the hooks read.
4. The skill reminder resolving a cached copy of the old plugin's skill.
5. `followups` meeting a `msgate:` refusal its pattern does not match, which trips its 10-minute breaker with no cost recording.
6. The band losing hook runs when the feedback variable is not renamed in the hooks.
7. History not migrated, which makes `stats` and `runs --all` undercount.
8. The old and new plugins active together.

### D. Planning record
- The planning decisions (nine questions and a table of ratified defaults) are kept outside this repository; the requirements above state their outcomes.
- `stats` by purpose and origin (FR65): replaces the hook and code-review sections, so the engine selects by no consumer's labels.
- Probes ran on Claude Code 2.1.300; their files are not part of this repository.
- Prompt-source finding of agentcli 0.6.2 (saved message first; the start text wraps a woken turn): release https://github.com/JustAzul/agentcli/releases/tag/v0.6.2 and agentcli FR64 at that version.
- The engine answers every run-backed row (FR33) and draws the spinner of those rows (FR25).
