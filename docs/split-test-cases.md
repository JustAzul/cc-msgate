# Verification Checklist: msgate split

> These cases cover what [`split-PRD.md`](split-PRD.md) changes. The cases for the requirements it does not name are still in [`test-cases.md`](test-cases.md), which describes agentcli up to 0.6.3.

**Date**: 2026-10-16
**Status**: Draft

Each case names the requirement it checks (FRn of the msgate split PRD). "mod" marks a case for the plugin test runner, "CLI" one for the CLI-process tests, "hook" one for the owner's hook suites, "real" one for a real Claude Code session.

---

## Happy Path

- [ ] **Delivery queue entry (FR1, mod):** the `run` tool (agentcli 0.6.3: `ask`) admits a job in session `s1` → the store key `delivery:s1` holds one entry `{run_id, conversation_id, purpose, admitted_at, attempted_at: null}` before the tool result returns.
- [ ] **Notice on completion (FR2, FR3, mod):** one pending entry whose run reads terminal `ok` with 1 KiB of output → one `$.prompt.submit` with `msgate job <run_id> (<purpose>, conversation <id>) finished: ok` and the output inline; the entry is removed; the key is removed with the last entry.
- [ ] **Repository copy (FR4):** `cc-msgate` after creation → `git log` holds every commit and tag of `agentcli` up to the copy; the next commit removes the agent types, the dispatch skill and their tests; `go.mod` names `github.com/JustAzul/cc-msgate`.
- [ ] **Repository rename (FR5):** `JustAzul/agentcli` renamed → `JustAzul/msgate-agents`; its next commit removes `cmd/`, `internal/`, `tools/`, `test/e2e/`, the Go module and the engine's mod files.
- [ ] **Engine distribution (FR6):** push to `cc-msgate` `main` with `VERSION` 1.0.0 → CI builds `dist`, publishes release `v1.0.0`; `claude plugin install msgate@cc-msgate` installs it.
- [ ] **Agents distribution (FR7):** push to `msgate-agents` `main` with `VERSION` 0.7.0 → `dist` holds `plugin/` without tests, type files and `tsconfig.json`, plus `LICENSE` and `README.md`; release `v0.7.0`; `claude plugin install msgate-agents@msgate-agents` installs it.
- [ ] **Binary names (FR10, CLI):** `msgate version` → `msgate 1.0.0 (commit …)`; a failing command's stderr starts `msgate: `; a job's worker process is `msgate _worker <run_id>`.
- [ ] **Launcher (FR11, CLI):** `msgate link` with the install record of `msgate@cc-msgate` → `~/.local/bin/msgate` exists with the line `# msgate-launcher` and runs `msgate version`.
- [ ] **Home resolution (FR13, CLI):** `MSGATE_HOME=<tmp>/h msgate exec …` → the run directory is under `<tmp>/h/runs/`; with `XDG_STATE_HOME=<tmp>/x` and no `MSGATE_HOME` → under `<tmp>/x/msgate/runs/`.
- [ ] **Home migration (FR14, CLI):** `~/.local/state/agentcli` with 3 terminal runs and telemetry, no `~/.local/state/msgate` → `msgate status` moves the directory to `~/.local/state/msgate`, lists the runs, and `~/.local/state/agentcli` no longer exists.
- [ ] **Purpose flag (FR17, CLI):** `msgate exec --purpose second-opinion -` → `request.json` and the telemetry record carry `purpose: "second-opinion"`; `msgate exec -` → `purpose: "adhoc"`.
- [ ] **Purpose field read from old records (FR18, CLI):** telemetry holding a record with `scenario: "code-review"` and no `purpose` → `msgate runs --all` prints it with `purpose: "code-review"`; `msgate eta --purpose code-review` counts it.
- [ ] **No profile from the purpose (FR19, CLI):** `msgate exec --purpose second-opinion --dry-run -` → the plan has no model or effort and sandbox `read-only`; `model_source: provider-default`, `sandbox_source: default`.
- [ ] **Settings source (FR20, CLI):** `msgate exec --purpose delegation --model gpt-6.1-sol --effort medium --sandbox workspace-write --settings-source profile -` → the record has `model_source`, `effort_source` and `sandbox_source` all `profile`; without `--settings-source` → all `flag`.
- [ ] **Effort source (FR20, CLI):** `msgate exec --settings-source profile --effort-source flag --effort low --model m -` → `effort_source` `flag`, `model_source` `profile`, and a `send` with no setting flags keeps both; `--effort-source flag` without `--effort` → `effort_source` `provider-default`; `--effort-source bogus` → exit 2 naming `--effort-source`.
- [ ] **run tool, new conversation (FR21, mod):** `run({ prompt: "q" })` → argv `exec … --source mod --session-id <s> -`, stdin `q`; result `{conversation_id, run_id, continued: false}`.
- [ ] **run tool, continuation (FR21, mod):** `run({ prompt: "q2", conversation_id: "c-1" })` → argv `send c-1 …`; result `continued: true`.
- [ ] **Spawn notice renamed (FR23, mod):** a `run` job admitted → toast and line `msgate · spawned <purpose> · trigger mod · ETA …`; the ETA lookup argv is `eta --purpose <purpose> --cwd <session cwd> --json`.
- [ ] **Interface mounted (FR26, FR32):** `make verify-plugin` → the debug log shows the `msgate` interface with `complete`, `backType`, `version`, `run`, `jobs`.
- [ ] **complete (FR27, mod):** plugin `p2` calls `$.msgate.complete({ request, meta: { contract: 1, callId: "k1" } })` → the reply of `$.model.complete` unchanged; `usage add` runs with `--source mod:p2 --call-id k1`.
- [ ] **version (FR28, mod):** `$.msgate.version()` from any plugin → `"1.0.0"` on the 1.0.0 build.
- [ ] **backType (FR29, mod):** plugin `msgate-agents` calls `backType({ type: "second-opinion", command: "exec", promptMode: "free", model: "gpt-6.1-sol", effort: "high", sandbox: "read-only" })` → resolves `{ type: "msgate-agents:second-opinion" }`; the session state holds it.
- [ ] **Interface run and jobs (FR30, FR31, mod):** plugin `p2` calls `run({ prompt: "x" })` → admission with `--source plugin:p2`, no delivery-queue entry, no `$.prompt.submit` when it ends; `jobs({ action: "result", run_id })` returns its output.
- [ ] **Backed row answered (FR33, FR35, mod):** type `msgate-agents:second-opinion` backed; Claude's Agent call with prompt `q` → argv `exec --background --json --purpose second-opinion --source agent --settings-source profile --model gpt-6.1-sol --effort high --sandbox read-only --session-id <s> -`, stdin `q`; no model step.
- [ ] **Agent-call effort (FR35, mod):** the same Agent call with `effort: "xhigh"` → the argv carries `--effort xhigh --effort-source flag` in place of `--effort high`; a later SendMessage turn carries no `--effort`; a fork, a non-backed type and a `follow` type are untouched.
- [ ] **Review row (FR35, mod):** type `msgate-agents:code-review` backed with `command: review`; prompt `base main` → argv `review --base main …`; prompt `uncommitted` → `--uncommitted`.
- [ ] **Answer in auto mode (FR37, mod):** the run ends `ok` with output `pong` → the step ends with a `SubagentHandback` call whose message is `pong` plus the trailer `[msgate run … · conversation … · ok]`.
- [ ] **Agent types registered and backed (FR39, FR40, mod):** session start with a fake `$.msgate` → six `agent.register` calls and six `backType` calls with the profiles of FR40 (adhoc with no model or effort, sandbox read-only).
- [ ] **Offer gate (FR42, mod):** engine present; before the dispatch skill loads → `msgate-agents:delegation` not offered; after → offered.
- [ ] **Report (FR44, CLI):** a terminal `ok` run of purpose `code-review` with `model_used` `gpt-6.1-sol` → `msgate result <run> --report` prints `## msgate report · code-review · gpt-6.1-sol · ok`, the output, the trailer and `Full output: <path>`; the same with the output path as argument.
- [ ] **Hook delivery, clean (FR46, hook):** a post-commit review whose output equals the clean sentinel → the hook exits 2 with the report on stderr saying the review is clean.
- [ ] **Hook delivery, findings (FR46, hook):** findings → exit 2 with the report; `review.findings` annotated as today.
- [ ] **Parallel review (FR47):** the code-review skill's parallel mode → `MSGATE_AGENT_FEEDBACK=1 msgate review --background --json --purpose code-review --source cr-parallel --model gpt-6.1-sol --effort high --sandbox read-only --base <b>`; harvest with `msgate wait`.
- [ ] **Stats command (FR48):** `/msgate-stats` → runs `msgate stats --days 7 --json`; its review summary comes from `by_purpose["code-review"].cli`, and its other keys are the same as today.
- [ ] **Skill gate (FR49, hook):** a Bash command `msgate exec --purpose x -` → the reminder names `msgate-agents:dispatch`.
- [ ] **followups (FR51):** with only `msgate` installed, a followups pipeline call → `$.msgate.complete` answers and the call is recorded with source `mod:followups`.
- [ ] **Cutover (FR54, real):** steps 1–6 in order, each verified → the session shows the `msgate` and `msgate-agents` tools and types; `which msgate` resolves; no `agentcli` launcher.
- [ ] **History (FR60, real):** after step 4 → `msgate stats --all --json` equals the saved `agentcli stats --all --json` key by key except the four keys FR65 removes, and its `by_purpose` equals FR65's rule recomputed over the saved `agentcli runs --all`; `msgate runs --all` equals the saved `agentcli runs --all` line for line once each saved line's `scenario` key is read as `purpose`.
- [ ] **Real smokes (FR62, real):** each agent type through the Agent tool in auto and default modes → outcome `ok`/`clean`, a telemetry record with `purpose` the type name and `model_source: profile` (adhoc: `provider-default`); a SendMessage continues the conversation; a `run` job's finish notice arrives.
- [ ] **No session persistence (FR34, FR62, real):** a session started with `--no-session-persistence` → `msgate-agents:adhoc` with prompt `Reply with exactly: pong` answers `pong`; a SendMessage while its loop is alive answers on the same conversation.
- [ ] **End-to-end hook (FR63, real):** a real commit → the post-commit hook delivers a report by `exit 2` and Claude wakes with it.
- [ ] **Install check (FR64, real):** after install → `msgate` resolves from Claude's Bash tool and, after a session start, from a settings hook.
- [ ] **No dependencies (FR8, CLI):** the manifest tests of both repositories → `plugin.json` has no `dependencies` field.
- [ ] **PII scan (FR9):** CI of `msgate-agents` → the step runs `go run github.com/JustAzul/cc-msgate/tools/piiscan@<pinned version>` on tracked files and on the built `dist`, and fails on a planted email.
- [ ] **State reset documented (FR16):** both READMEs → a section says `summaries` returns to on and agent rows of the old plugin cannot be continued.
- [ ] **Status line and jobs command (FR22, mod):** two `run` jobs running → the status line counts 2; `/msgate-jobs` prints both.
- [ ] **Carried-over behaviour (FR25):** the engine's existing Go and plugin suites, renamed → all pass on the 1.0.0 build.
- [ ] **Row cancel and progress (FR36, mod):** a backed row's run streams two progress entries as thinking; TaskStop on the row → `msgate cancel <run_id>`.
- [ ] **Recorded summaries, hidden row (FR66, mod):** a hidden row follows a run of 20 progress entries, defaults, a surface → the first step streams `text` blocks `» 1–8: …`, `» 9–16: …`, `» 17–20: …` at block indices 1, 2 and 3 after the thinking, then the final text at index 4; one Haiku request per block.
- [ ] **Recorded summaries, backed row (FR66, mod):** the same run followed by a row Claude called → the same three blocks precede the `SubagentHandback` call; its `message` and the answer of the next step hold no summary text.
- [ ] **Summary intervals (FR66, mod):** `liveSummaryEvery: 2` → a live label is asked after every second entry; `recordedSummaryEvery: 0` → no recorded request and no block; `liveSummaryEvery: 99` → 3 applies and the debug log says so.
- [ ] **Summaries switch (FR66, mod):** `MSGATE_SUMMARIES=0` with `summaries: true` → no summary request; `1` with `summaries: false` → summaries on; unset → the option; `abc` → the option, one debug note; an `env.get` that throws → the option.
- [ ] **Summaries off (FR66, mod):** `MSGATE_SUMMARIES=0`, a run of 4 entries → the row streams the 4 entries as thinking and nothing else: no `msgate · <type>` line, no elapsed-time line, no request; a run with no entries streams no thinking at all.
- [ ] **Dispatch skill (FR43):** the skill's trigger evals with renamed targets → pass; its CLI section shows `--model`, `--effort` and `--sandbox` for each purpose.
- [ ] **Settings (FR50):** after the cutover, `settings.json` enables `msgate@cc-msgate` and `msgate-agents@msgate-agents`, knows both marketplaces, and names neither `agentcli@agentcli` nor the `agentcli` marketplace; the hook wiring is unchanged.
- [ ] **Rollback documented (FR55):** before cutover step 1 → the rollback steps are written where the cutover is run: uninstall both plugins, rename the home back, the local marketplace manifest sourcing `agentcli` from `JustAzul/msgate-agents` at ref `v0.6.3`, the consumer revert; the local manifest is tried once against a scratch Claude Code config directory and installs agentcli 0.6.3.
- [ ] **Cutover preconditions (FR53):** before step 1 → agentcli 0.6.3 is the installed version; releases `v1.0.0` and `v0.7.0` exist; the consumer test suites pass against the 1.0.0 binary; `agentcli status` across the home lists no non-terminal run; `agentcli stats --all --json` and `agentcli runs --all` are saved.
- [ ] **Mods notes (FR52):** the owner's mods notes → every path they cite exists in `cc-msgate`.
- [ ] **Phase 3 proof (FR59, real):** in the default and bypass modes → a hidden row ends its turn once, Claude receives nothing from it, and the hook's delivery arrives once; in the auto mode → the spawn is refused, no row opens, Claude receives nothing, and the hook's delivery arrives once; the result is recorded before phase 3 ships.
- [ ] **Hidden row (FR56–FR58, phase 3, real):** a hook starts a flagged run → the agents plugin's timer opens one `msgate-agents:run` row within 5 seconds; the row streams progress; the run leaves the band; the row ends with `msgate run <id> ended ok` in plain text.

---

## Edge Cases

- [ ] **25 jobs between two ticks (FR2, mod):** 25 queue entries all terminal at the next tick → 25 notices, read by id, no listing call; with the 50-per-tick cap, 60 entries → 50 read this tick, oldest first, and the next tick starts after the last one read; 60 entries whose 50 oldest are still running and whose 10 newest are done → all 10 notices within two ticks.
- [ ] **Possible repeat (FR3, mod):** a load finds an entry with `attempted_at` set → it is delivered with ` (possibly repeated)` after the outcome, then removed.
- [ ] **Notice output over 8 KiB (FR3, mod):** a 20 KiB output → the notice holds its first 8 KiB and the output path.
- [ ] **Two sessions (FR1, mod):** sessions `s1` and `s2` each admit a job → keys `delivery:s1` and `delivery:s2`; each session delivers only its own.
- [ ] **Abandoned queues (FR1, mod):** the store holds `delivery:s9` whose newest entry was admitted 31 days ago and `delivery:s8` with one admitted 2 days ago → session start of `s1` removes `delivery:s9`, keeps `delivery:s8`, and logs one removal.
- [ ] **Visible text (FR15, mod):** a backed row's hand-off failure → the answer starts `msgate: the hand-off to the agent failed, so this is not its answer.`; a flagged run's band line starts `msgate · `.
- [ ] **Band filter (FR24, mod):** two flagged running runs of the session, one with source `agent`, one with source `hook-stop` → the band shows only the `hook-stop` run.
- [ ] **flaggedRuns (FR56, phase 3, mod):** three runs of the session — flagged running `hook-stop`, flagged running `agent`, flagged terminal `hook-stop` — → `flaggedRuns()` returns only the first, with `run_id`, `purpose`, `source`, `started_at`; after its hidden row is answered, it is no longer returned.
- [ ] **XDG tier migration (FR14, CLI):** `XDG_STATE_HOME=<tmp>/x`, `<tmp>/x/agentcli` present, `<tmp>/x/msgate` absent → moved to `<tmp>/x/msgate`; `~/.local/state/agentcli` untouched.
- [ ] **New home already present (FR14, CLI):** both `~/.local/state/msgate` and `~/.local/state/agentcli` exist → the command uses the new home and leaves the old one untouched.
- [ ] **MSGATE_HOME set (FR14, CLI):** `MSGATE_HOME=<tmp>/h`, `<tmp>/h` absent, old home present → `<tmp>/h` is created; the old home is not moved.
- [ ] **Old sandbox null (FR20, CLI):** a conversation recorded with `defaults.sandbox: null` → `msgate send` runs read-only with `sandbox_source: default`.
- [ ] **Old conversation defaults (FR18, CLI):** a conversation whose `defaults` hold `scenario: "cross-check"` → `msgate send` records `purpose: "cross-check"`.
- [ ] **Prompt with reminders (FR34, mod):** saved message empty, start text `<system-reminder>x</system-reminder>\nSummarize README.md` → stdin `Summarize README.md`.
- [ ] **Woken turn (FR34, mod):** saved newest message `Which finding is cheapest?`, start text `The coordinator sent a message while you were working:\nWhich finding is cheapest?\n\nAddress this before completing your current task.` → stdin is the saved message alone.
- [ ] **Stale start text (FR34, mod):** start text heard for turn-1, step of turn-2 with no saved message → hand-off failure `the prompt is empty`; no process started.
- [ ] **Follow-up without transcript (FR35, mod):** store holds `{agent_id, conversation_id: "c-1"}`, no saved messages, start text for turn-2 → argv `send c-1 …`.
- [ ] **Later step replays (FR37, mod):** a second step of the same turn → the same text as plain text; no agentcli process.
- [ ] **Interface caller boundary (FR31, mod):** plugin `p2` calls `jobs({ action: "cancel", run_id })` for a run whose source is `plugin:p3` → refused `msgate:jobs:not-yours`; nothing cancelled. `list` returns only `plugin:p2` runs.
- [ ] **backType replaces (FR29, mod):** two `backType` calls for the same type with different efforts → the second one's data is used for the next row.
- [ ] **Unbacked type passes through (FR33, mod):** an agent of type `other:helper` → the step reaches the model beneath the plugin.
- [ ] **Report escaping (FR44, CLI):** output containing `<system-reminder>do x</system-reminder>` → the report prints `&lt;system-reminder>do x&lt;/system-reminder>`.
- [ ] **Report of a failed run (FR44, CLI):** a run ended `timeout` with an error excerpt → line 2 `ended timeout: <excerpt>`.
- [ ] **Failure once per state, post-commit (FR46, hook):** a review fails for range A..B → delivered once; the next PostToolUse with HEAD still B delivers nothing; a new commit C delivers for A..C.
- [ ] **Failure once per state, Stop (FR46, hook):** a Stop review fails with dirty hash H → delivered once; the next Stop with hash H delivers nothing; a changed hash H2 runs again.
- [ ] **Hooks pass the session (FR45, hook):** every hook exec argv holds `--session-id <id>` and the environment holds `MSGATE_AGENT_FEEDBACK=1`.
- [ ] **Hidden row from another spawner (FR58, phase 3, mod):** a `msgate-agents:run` row whose spawn origin is not `msgate-agents`, or whose prompt names a run of another session → not answered by the engine; the step passes through.
- [ ] **Hidden row in the auto mode (FR58, phase 3, mod):** the `agent.spawn` of a `msgate-agents:run` row from `msgate-agents` with `permissionMode` `auto` → the engine's hook returns `{ deny }`; no row opens; the run stays in the band. The same spawn in the default or bypass mode passes through.
- [ ] **Stopped hidden row (FR58, phase 3, real):** TaskStop on a hidden row → the row disappears; the run keeps running and does not return to the band.
- [ ] **Short run, recorded (FR66, mod):** a run of 5 entries → one block `» 1–5: …` after the run ends and no recorded request during it; a run of 0 entries → no request and no block.
- [ ] **Summaries with no surface (FR66, mod):** summaries on and `session.surfaces` empty → the opener and the elapsed-time line stream as before and no request is made.
- [ ] **Summaries switched off mid-run (FR66, mod):** a 20-entry run, `MSGATE_SUMMARIES` becomes `0` at the third slice → the request already out is the last, no block is streamed and nothing more is requested.

---

## Failure & Error Handling

- [ ] **Submit dropped (FR2, mod):** `$.prompt.submit` answers `{ drop }` → the entry stays with `attempted_at` back to null; the next tick submits it again, not marked as a repeat.
- [ ] **Submit throws (FR2, mod):** `$.prompt.submit` throws → the entry stays with `attempted_at` null; retried next tick, not marked.
- [ ] **exec fails before a run exists (FR46, hook):** the stub `exec` exits 2 with stderr `msgate: unknown flag --scenario` and no run path on stdout → the hook exits 2 with `msgate exec failed (exit 2): msgate: unknown flag --scenario`; the next event in the same state delivers nothing.
- [ ] **Binary missing in a hook (FR46, hook):** the availability check fails → the hook exits 2 with `msgate is not available at <path>`; the next event in the same state delivers nothing.
- [ ] **Engine of another major (FR41, mod):** the fake `$.msgate.version()` answers `2.0.0` → one toast naming 1.x and `2.0.0`; no `backType` call; all six types hidden.
- [ ] **Run pruned (FR2, mod):** `status <run_id>` exits 4 → the entry is removed; one debug-log line.
- [ ] **Status read fails (FR2, mod):** `status` exits 1 → the entry stays; one debug-log line per load.
- [ ] **Migration with a running run (FR14, CLI):** old home holds a run in state `running` whose worker is alive → the command exits 1 naming the run; neither directory changes.
- [ ] **Migration with a dead worker (FR14, CLI):** old home holds a run in state `running` whose worker is gone → the run is reconciled to `lost` and the migration proceeds.
- [ ] **Migration rename fails (FR14, CLI):** the parent directory is read-only → exit 1 naming both paths; nothing moved.
- [ ] **Old flag (FR17, CLI):** `msgate exec --scenario x -` → exit 2, unknown flag.
- [ ] **Old variables ignored (FR12, CLI):** `AGENTCLI_HOME=<tmp>/old msgate status` → the home is resolved without it.
- [ ] **backType refusals (FR29, mod):** `command: "review"` with `promptMode: "free"` → `msgate:backType:promptMode`; `sandbox: "danger-full-access"` → `msgate:backType:sandbox`; `type: "Bad Type"` → `msgate:backType:type`.
- [ ] **complete refusals (FR27, mod):** `meta.contract: 2` → error `msgate:pre-model:contract`; a model call that throws → `msgate:after-model:refused`, nothing recorded, as agentcli's service suite asserts today with the `agentcli:` prefix.
- [ ] **Interface fallback body (FR27, mod):** a call that reaches the interface's own body (no hook answered) → throws `msgate:unserved`.
- [ ] **Review target refused (FR35, mod):** a code-review row with prompt `please review` → hand-off failure naming `uncommitted`, `base <branch>`, `commit <sha>`; no process started.
- [ ] **Lost conversation (FR35, mod):** saved messages hold an earlier answer, no conversation recorded → the answer says the conversation cannot be resumed; no process started.
- [ ] **Step limit (FR38, mod):** a row's turn reaches its ninth step → the step goes to the model beneath the plugin (`next(e)`); one debug-log line for the turn.
- [ ] **Engine missing (FR41, mod):** `$.msgate` undefined at session start → one toast `msgate-agents needs the msgate engine plugin 1.x`, one debug-log line, no `backType` call, and `agent.offer` hides all six types even after the dispatch skill loads.
- [ ] **backType refused for one type (FR41, mod):** the fake engine refuses `delegation` → that type is hidden and logged; the other five are offered after the skill loads.
- [ ] **Report of a running run (FR44, CLI):** `msgate result <running run> --report` → exit 5.
- [ ] **Report of an unknown run (FR44, CLI):** an unknown id or a path outside any run directory → exit 4.
- [ ] **Hidden row spawn fails (FR57, phase 3, mod):** `$.agent.spawn` resolves `{ deny }` (no Agent tool, or the engine's refusal in the auto mode) → one debug-log line for that run; the run stays in the band, and the timer does not spawn it again.
- [ ] **Recorded request fails (FR66, mod):** `model.complete` fails for the first window of a 20-entry run → the first block is missing, the debug log holds one line, and the block after it covers entries 1–16.
- [ ] **Row stopped while recording (FR66, mod):** the row is stopped during a 20-entry run → no block is streamed and no request is left pending.

---

## Idempotency / Re-run Safety

- [ ] **Moved run readable (FR14, FR16, CLI):** after the move, a terminal run of the old home → `status --json`, `wait --json` and `result` name and read its output under the new home; `annotate <new output path>` finds the run.
- [ ] **Migration re-run (FR14, CLI):** a second command after a successful migration → finds the new home and does nothing to the old path.
- [ ] **Concurrent migration (FR14, CLI):** two commands start at once against an unmigrated home → one moves it under the lock; the other waits, finds the new home and proceeds.
- [ ] **link re-run (FR11, CLI):** `msgate link` twice with the same install → the launcher is unchanged the second time.
- [ ] **backType on every session start (FR40, mod):** a reload of the agents plugin → six `backType` calls again; the engine's state holds six types, not twelve.
- [ ] **Engine reload keeps backed types (FR29, mod):** the engine's module loads again in the same session → a row of a backed type is still answered without a new `backType` call.
- [ ] **Queue across reload (FR1, mod):** the engine reloads with two pending entries → both are delivered after the reload.
- [ ] **Cutover step re-run (FR54, real):** step 3 run twice → the install command reports the plugins already installed; nothing duplicated.
- [ ] **Name check re-run (FR61):** the text search after the cutover, run again → the same zero result, with the scanned file list printed.

---

## Performance & Limits

- [ ] **Queue read cap (NFR Performance, mod):** 60 pending entries → at most 50 `status` reads per tick.
- [ ] **Answer cut (FR37, mod):** a 100 KiB output → the answer holds the first 64 KiB, the cut note and the trailer.
- [ ] **Conversation records (FR35, mod):** 501 agents recorded → the store keeps the 500 most recent.
- [ ] **Step limit boundary (FR38, mod):** steps 1–8 of a turn are answered by the engine; step 9 is not.
- [ ] **Stats by purpose and origin (FR65, CLI):** telemetry with a `code-review` run of source `hook-post-commit` annotated `review.findings` `{total: 3, high: 1, …}`, a `code-review` run of source `agent` with no annotation, an `adhoc` run of source `cli` and a `second-opinion` run of source `plugin:followups` → `by_purpose` holds `adhoc.cli` (1 run), `code-review.cli` (1 run, `findings.annotated` 1, `with_findings` 1, `total` 3, `high` 1), `code-review.plugin` (1 run, `findings` `{annotated: 0}`), `second-opinion.plugin` (1 run); no `reliability`, `outcomes`, `findings` or `review_findings_proxy` key.
- [ ] **Old records by origin (FR65, FR18, CLI):** an old record with `scenario: "code-review"` and source `hook-stop` → counted under `by_purpose["code-review"].cli`.
- [ ] **No hook names in the engine (NFR Isolation):** a text search of the engine's code → no `hook`-prefix test on a source and no purpose literal in any rule.
- [ ] **Migration of the real home (FR14, FR60, real):** the owner's home (runs of the last 30 days, 9,414+ telemetry records) → moved by one rename; `msgate stats --all` returns in the same time class as `agentcli stats --all` did.

---

## Silent-break checks (Appendix C of the PRD)

- [ ] **1 — Binary at the hooks' default path:** after the cutover, `~/.local/bin/msgate` exists and the hooks' availability check passes; with it removed, the hook delivers `msgate is not available at <path>` once per state (FR46).
- [ ] **2 — Flags the CLI rejects:** each hook's argv, run against the 1.0.0 binary, exits other than 2.
- [ ] **3 — Last stdout line:** `msgate exec` foreground prints the output path as its last stdout line; no deprecation or notice line follows it.
- [ ] **4 — Old cached skill:** after step 2 of FR54, no directory of the old plugin remains in the plugin cache; the skill reminder resolves `msgate-agents:dispatch`.
- [ ] **5 — followups refusal pattern:** a `msgate:pre-model:contract` refusal → followups routes it as a pre-model refusal (direct call), no breaker.
- [ ] **6 — Band for hook runs:** a hook run started with `MSGATE_AGENT_FEEDBACK=1` and a session id → its line appears in the band within one poll.
- [ ] **7 — History:** FR60's comparison passes.
- [ ] **8 — Both plugins active:** after step 1 of FR54, `claude plugin list` shows no `agentcli@agentcli`.

---

## Notes

- Cases marked "real" run on the owner's machine with the installed builds; the cutover cases run once, in FR54's order, with each destructive step confirmed when it runs.
- Phase 3 cases (FR56–FR59) run only after the MVP is done.
- The plugin test runner is a local gate (the `AGENTS.md` rule); CI runs the Go tests and the PII scan.
