# Verification Checklist: agentcli (`agentcli`)

> These test cases describe agentcli up to 0.6.3. [`split-PRD.md`](split-PRD.md) amends and supersedes the requirements they check for msgate, as its Appendix A maps; the cases for the changes are in [`split-test-cases.md`](split-test-cases.md).

**Date**: 2026-10-04 (updated 2026-10-08: session cost; progress summaries and Claude spend, group R; spawn notice, ETA and spinner, group S)
**Status**: Draft

Unless stated otherwise:
- `AGENTCLI_HOME` points at a fresh temporary directory.
- A fake `codex` is first on PATH. It replays a named recorded event stream (sanitized fixtures: `exec-ok`, `review-ok`, `resume-ok`, `error-400`), writes the `-o` file, and exits with a scripted code.
- `AGENTCLI_PRICES_URL=off`, so no case reaches the network. Price cases point it at a loopback test server that serves a fixture price list.
- "Real" cases use the installed codex-cli and are the FR57–FR59 smokes.
- Mod cases run in the plugin test runner. Its kit answers `model.complete` and `session.surfaces` beneath the plugin; `session.surfaces` answers `[]` unless a case says it answers a surface, so a case is headless by default.

---

## Happy Path

- [ ] **exec foreground (FR1, FR5, FR13, FR15, FR31):** fake `exec-ok` → `agentcli exec --scenario second-opinion "q"`.
  - Exit 0; the last stdout line is the absolute path of `runs/<run_id>/output.md`, which holds the fixture's final message.
  - `state.json` is `done`.
  - One `run` record with `command: exec`, `turn: 1`, `outcome: ok`, `usage.output_tokens` equal to the fixture value, `model: gpt-6.1-sol`, `model_source: profile`, `provider_session_id` equal to the fixture's thread id.
- [ ] **exec from stdin (FR1):** `printf 'q' | agentcli exec -` → `prompt.md` equals `q` and the fake received `q` on stdin.
- [ ] **exec from file (FR1):** `agentcli exec --prompt-file p.md` → `prompt.md` equals the file content.
- [ ] **dry-run plan (FR8, FR13):** `agentcli exec --dry-run --cwd /work/repo --model m --effort high --sandbox read-only "q"` → the JSON plan's argv is exactly `codex exec -C /work/repo -s read-only -m m -c model_reasoning_effort=high --json -o <path> -`, the stdin source is the prompt, and no run directory, conversation or telemetry exists afterwards.
- [ ] **review plan (FR2, FR13):** `agentcli review --base main --dry-run` → argv ends with `review --base main`, stdin is empty, and there is no `-` tail.
- [ ] **send resumes (FR3, FR13, FR18, US2):** after an `exec` whose fixture thread id is T, `agentcli send <conversation_id> "follow"`.
  - argv ends with `resume T -`.
  - The record has `turn: 2` and the same `conversation_id`.
  - The conversation lists both run ids in order.
- [ ] **send by run id (FR3):** `agentcli send <turn-1 run_id> "x"` → resolves to the same conversation, turn 2.
- [ ] **job admission and wait (FR6, FR7, FR21, US4):** `agentcli exec --background --json "q"`.
  - Exit 0 within 10 s with `{conversation_id, run_id, state: running, run_dir, output_path}`.
  - `agentcli wait <run_id>` from another shell exits 0 and its last stdout line equals `output_path`.
  - The record has `background: true`.
- [ ] **job last line (FR6):** `agentcli exec --background "q"` (no `--json`) → the last stdout line equals the run id.
- [ ] **status listing (FR7, FR62):** two runs under `--session-id S` and one under `S2` → `agentcli status --session-id S --json` lists exactly the two `S` runs, newest first.
- [ ] **prune summary (FR60):** two runs, one finished 40 days ago and one fresh → `agentcli prune --json` exits 0 with `removed: 1`, `kept: 1`, `skipped: 0`, `older_than_days: 30` and `bytes_freed` greater than 0. The old run directory is gone, the fresh one stays, and the text form prints `removed 1, kept 1, skipped 0, freed <n> bytes`.
- [ ] **prune retention window (FR60):** `--older-than 60` keeps the 40-day-old run, `--older-than 10` removes it, `AGENTCLI_RETENTION_DAYS=60` keeps it, and the flag wins over the variable. `--older-than 0` removes every finished run.
- [ ] **prune dry run (FR60):** `prune --dry-run` prints `would remove 1, …` (JSON: `dry_run: true`, `removed: 1`), removes nothing and creates no lock file in any run directory.
- [ ] **prune leaves history alone (FR60):** after a prune, the telemetry records and the conversation files are byte-for-byte unchanged.
- [ ] **index on admission (FR62):** `exec`, `review`, `send` and the two `--background` forms with `--session-id S` → `index/sessions/S` lists their run ids in admission order, one per line. A run with no session id is listed in `index/sessions/_`, and `status` with no session id lists it.
- [ ] **session status reads one index (FR62):** a run that no index lists is not shown by `status --session-id X`, even when its request says session `X`. A session with no index lists no runs.
- [ ] **conversation sources (FR63):** `exec --scenario second-opinion --model m1` → the conversation's `defaults` hold `model_source: flag`, `effort_source: profile`, `sandbox_source: profile`.
- [ ] **send after the first turn is pruned (FR60, FR63):** prune removes turn 1 of a conversation started with `--model m1` → `send <conversation_id>` runs turn 2 with `model: m1`, `model_source: flag`, `effort_source: profile`. `send <turn-1 run id>` exits 4.
- [ ] **auto prune (FR61):** `prune --auto` on a home with an old run → exit 0, nothing on stdout or stderr, the old run is gone, and `prune.stamp` holds the time of the pass.
- [ ] **hook command (FR61):** the plugin's SessionStart hook command is `"${CLAUDE_PLUGIN_ROOT}"/bin/agentcli link --quiet; "${CLAUDE_PLUGIN_ROOT}"/bin/agentcli prune --auto` with timeout 5.
- [ ] **result (FR7):** `agentcli result <run_id>` → prints the content of `output.md`.
- [ ] **conversations (FR7):** after exec + send → `agentcli conversations --json` shows the conversation with provider `codex`, 2 turns, status `idle` and `resumable: true`.
- [ ] **attrs at dispatch (FR4, FR31, US6):** `--attr ticket=2056 --attr-json review.findings='{"total":1}'` → the record's `attrs` equals `{"ticket":"2056","review.findings":{"total":1}}`.
- [ ] **annotate after the fact (FR32, FR33, US6):** `agentcli annotate <run_id> --attr acted=yes` → `agentcli runs --json` shows the run with `attrs.acted == "yes"` alongside the dispatch attrs.
- [ ] **annotate by output path (FR32):** `agentcli annotate <output_path> --attr k=v` → annotation recorded for that run id.
- [ ] **clean outcome (FR28, US3):** the fixture output is `No material findings.` with `--clean-sentinel 'No material findings.'` → outcome `clean`.
- [ ] **material label (FR28):** the fixture output is a findings list with `--material-label findings` → outcome `findings`.
- [ ] **profiles (FR38):** `--scenario delegation` → plan has `-s workspace-write`, `-c model_reasoning_effort=medium`, `-m gpt-6.1-sol`. `--scenario delegation --effort high` → effort high with `effort_source: flag`.
- [ ] **unknown scenario (FR38):** `--scenario claude-md-update` → no profile applied, record `scenario: claude-md-update`, sources `provider-default` unless flags are given.
- [ ] **stats (FR35, US7):** with mixed records → `agentcli stats --all --json` has every key `total, empty, span, by_source, by_source_status, instrumentation, reliability, outcomes, duration_ms, findings, review_findings_proxy, window_days`, plus `by_provider`, `usage_totals`, `usage_by_model`, `usage_by_provider`, `skipped`, `unpriced_models`, `missing_prices` and `prices_checked_at`, and no other key.
- [ ] **version (FR10):** `agentcli version --json` → has `version`, `source_commit`, `build_seq` (integer > 0 for CI builds) and `platform`.
- [ ] **link fresh (FR43, US5):** no launcher present → `agentcli link`.
  - It creates `~/.local/bin/agentcli` (directory created if missing), which executes the resolved install's `bin/agentcli` and carries its `build_seq`.
  - `~/.local/bin/agentcli version` matches.
- [ ] **shim platform selection (FR41):** on linux/amd64 → `bin/agentcli version --json` reports `platform: linux/amd64`. The same holds on the macOS CI runner for darwin.
- [ ] **mod ask (FR46, FR47, US1):** in a session with the mod, Claude calls `ask {prompt}`.
  - It returns `{conversation_id, run_id}` at once.
  - Within one poll interval after the job ends, a toast appears and a turn is submitted whose text starts `agentcli job <run_id>` and contains the outcome and the output inline (≤ 8 KiB).
- [ ] **mod send (FR46):** `send {conversation_id, prompt}` → a job for turn 2, with a notice on completion.
- [ ] **mod jobs (FR46):** `jobs {action: "list"}`, `{action: "status", run_id}`, `{action: "result", run_id}`, `{action: "cancel", run_id}` each return the CLI's JSON or content.
- [ ] **mod status line and command (FR48):** two running jobs → the status line shows 2. `/agentcli-jobs` prints the session's recent jobs while Claude is mid-turn.
- [ ] **provider version (FR17):** fake `codex --version` prints `codex-cli 9.9.9` → the record's `provider_version` is `codex-cli 9.9.9`. A fake whose version flag exits 1 → `provider_version: null`, and the run is otherwise unaffected.
- [ ] **terminal ordering (FR22):** a test observer polls a job; at the first poll where `state.json` is terminal, `output.md` already has its final bytes and the conversation's marker is already cleared. The telemetry record appears after the terminal state, never before.
- [ ] **job survives its launcher (FR25):** start a job from a shell, then kill that shell's whole process group → the job still reaches `done`, and `wait` from a new shell exits 0.
- [ ] **namespaced attrs (FR36):** `--attr-json review.findings='{"total":2}'` → stored under the literal key `review.findings`, not nested as `review → findings`.
- [ ] **marketplace manifest (FR39):** `claude plugin validate` on the repository root passes. The manifest lists one plugin `agentcli` with source `github`, `ref: dist` and no `sha`.
- [ ] **dist content (FR40, FR42):** the published `dist` tree contains the plugin manifest without `version`, `bin/agentcli`, four binaries, `SHA256SUMS` (all four checksums verify), the skill, the mod, commands, and a hooks file whose SessionStart runs `agentcli link --quiet; agentcli prune --auto`.
- [ ] **release of a new version (FR93):** a temp repository whose `VERSION` went 0.1.0 → 0.2.0 → 0.3.0, a fake `gh` that has no release `v0.3.0` → `scripts/publish-release.sh <dist sha> <source sha>` runs `gh release create v0.3.0 --target <dist sha> --title v0.3.0 --notes-file <file>`. The notes start with `Build of <source sha>.` and list, oldest first under Features, Fixes and Other, the subjects of the commits after the 0.2.0 commit up to the source commit, each with its short sha, without `chore: release 0.3.0`.
- [ ] **pre-release version (FR93):** `VERSION` is `0.5.0-rc.1` or `1.0.0-alpha+exp.sha.5114f85` → `gh release create` gets `--prerelease`. `1.0.0+build-1` and `1.2.3` → no `--prerelease`.
- [ ] **release notes of the first version (FR93):** a temp repository whose `VERSION` was never changed after the first commit → the notes list every commit up to the source commit.
- [ ] **repository identity (FR45):** `git log --format='%an <%ae>'` on the public repo shows only the owner's global git identity. LICENSE is MIT with the owner's name.
- [ ] **mods API check (FR49):** before mod code is written, the installed build's mod type definitions are read, and the registration, tool, timer, toast, prompt-submit, status and store calls used by FR46–FR48 exist with the expected shapes. Any mismatch is reported as a blocker.
- [ ] **dispatch skill (FR50):** the skill text covers scenarios/profiles, tools vs CLI, foreground vs job, continuing conversations, reading results, attribution and open disagreement, no patch application outside delegation, and cost. The carried-over trigger evals pass with the renamed skill.
- [ ] **real per-scenario smokes (FR57):** installed binary, real codex → one run each of second-opinion, code-review (`review --uncommitted` on a scratch repo), cross-check, expert-persona, delegation (scratch repo) at profile defaults. Each outcome is not `error`/`lost`, each has a telemetry record with non-null `usage` (except review), and `error_excerpt` is null.
- [ ] **real conversation and job (FR57):** real exec, then real `send` → turn 2 references turn-1 content. A real `--background` job, then `wait`, exits 0.
- [ ] **post-commit E2E (FR52, FR58):** live session, a real commit in a scratch repo → the migrated hook surfaces a review or stays silent on clean, and `agentcli runs --json` shows a record with `source: hook-post-commit`, `scenario: code-review`, `model: gpt-6.1-sol`.
- [ ] **cutover order (FR51):** the migration log shows the steps in order (install → skill → hooks → parallel review → analyzer/command/harness → legacy import + archive → deletion), each with its verification evidence recorded before the next starts.
- [ ] **parallel review (FR53, US4):** the code-review skill in parallel mode on a scratch branch → one job with `source: cr-parallel`, `scenario: code-review`; harvest through `wait` returns the provider exit and output path. A files-only target starts no job.
- [ ] **stats command and harness (FR54, US7):** the stats slash command prints `agentcli stats` output. The eval harness counts as dispatches exactly the runs whose `ts` is inside the attempt window and whose `cwd` is the attempt's repository; a run from another cwd in the same window is not counted.
- [ ] **skill gate (FR55):** in a session without the dispatch skill loaded, each of `agentcli exec "q"`, `nohup agentcli exec "q"`, `FOO=1 agentcli exec "q"`, `/abs/path/agentcli exec "q"` and `codex exec "q"` triggers the precondition naming `agentcli:dispatch`. `echo agentcli` does not.
- [ ] **old names gone (FR55, FR56):** a text search for the old skill name across the listed skills, rules, hook docs and eval files returns no stale reference. A text search for `codex exec`/`codex review` invocations across hook scripts, hook libraries, scripts, commands and skill scripts returns zero (success metric 1).
- [ ] **install check (FR59, US5):** after the marketplace install, a Bash tool `command -v agentcli` resolves inside the plugin. After a session start, a settings-hook probe resolves `~/.local/bin/agentcli`.

---

## Agent types, progress and agent feedback

- [ ] **agent type answers (FR64, FR67):** in a session with the plugin and no summaries (the `summaries` setting off, or a session with no screen, FR86), after the dispatch skill loads, Claude calls `Agent(subagent_type: "agentcli:second-opinion", prompt: "q")`.
  - A background agent row appears and streams the run's raw progress entries as thinking.
  - The completion notification carries the output and the `[agentcli run … · conversation … · ok]` trailer.
  - No Claude model request is made for the subagent; telemetry has one run with source `agent`.
- [ ] **gate (FR64):** before the dispatch skill loads, `Agent(subagent_type: "agentcli:adhoc")` is refused as an unknown type; after it loads, the call is accepted. Loading it in another session does not close this one.
- [ ] **follow-up (FR65):** a SendMessage to a finished agentcli agent runs `send` on its conversation; the provider answers with the earlier turn's context.
- [ ] **cancel (FR65):** TaskStop on a running agentcli agent → its run ends `cancelled` (exit 130).
- [ ] **hand-off failure (FR64):** an agentcli failure (exit 3, busy) → the answer starts with `agentcli: the hand-off to the agent failed` and no model request is made.
- [ ] **hand-back in the auto permission mode (FR65):** in a session in the auto permission mode, Claude calls `Agent(subagent_type: "agentcli:adhoc", prompt: "Reply with exactly: pong")` → the agent calls `SubagentHandback`, and Claude receives the report with the `[agentcli run … · conversation … · ok]` trailer.
- [ ] **plain text in the default permission mode (FR65):** the same call in the default permission mode → the report reaches Claude as the agent's final text, and telemetry has exactly one run with source `agent` for it.
- [ ] **later step of a turn (FR65, mod):** a second step of the same turn of an agentcli agent → it answers with the turn's text as plain text and runs no agentcli command.
- [ ] **hand-back allowed (FR64, mod):** inside an agentcli agent, a `SubagentHandback` call passes the tool guard; a `Bash` call is still refused.
- [ ] **session with no saved transcript (FR64, FR65):** in a session started with `--no-session-persistence`, Claude calls `Agent(subagent_type: "agentcli:adhoc", prompt: "Reply with exactly: pong")` → the run's prompt is that text and the report reaches Claude; a SendMessage to the same agent while its loop is still alive runs `send` on its conversation, and both trailers name the same `conversation_id`. Claude Code itself refuses to resume an agent that has finished in such a session ("No transcript found for agent ID"), so a later SendMessage never reaches the plugin.
- [ ] **prompt source (FR64, mod):** the prompt is the newest user message of the agent's conversation, system reminders removed; with none, the text the turn's `turn.start` carried; with neither, the answer is the hand-off failure ending in `the prompt is empty` and no agentcli command runs. A SendMessage turn whose start text wraps the message (`The coordinator sent a message while you were working: …`) sends the saved message alone. A start text heard for an earlier turn is never used for a later one.
- [ ] **progress (FR66):** fake `review-ok` → `agentcli progress <run_id> --json` lists the two commands and the final message in order, `next: 3`; `--from 3` lists none; an unknown run exits 4.
- [ ] **agent feedback flag (FR68):** `--agent-feedback --session-id s` → `agent_feedback: true` in `request.json` and `status --json`; `AGENTCLI_AGENT_FEEDBACK=1` likewise; with no session id the run exits 0, `agent_feedback: false`, and stderr says it was ignored.
- [ ] **flagged run shown (FR69):** with no summaries (the `summaries` setting off, or a session with no screen), a hook starts a foreground `exec --agent-feedback` in the session → within one poll a line `agentcli · <scenario> · <source> · <elapsed> · <newest step>` appears above the prompt and follows what the run does; within 5 seconds of the run ending the line is gone. A run started by the agent types, or without the flag, gets no line. No agent row appears, no Claude model request is made, and the run's text never enters the conversation with Claude. With summaries active, the line's last part is the label instead (FR91, below).
- [ ] **model used (FR70):** a run on the provider's default model records `model: null` and `model_used` equal to the model in the provider's session file; `stats --session-id s --json` counts only that session and lists its tokens under `usage_by_model`.

---

## Session cost and prices

Fixtures for this section:
- **Price list:** `testdata/prices/` serves a LiteLLM-shaped file with fictitious models. All have `litellm_provider: openai` except the last two.
  - `m-sol`: input `2e-06`, cache read `1e-07`, cache creation `2.5e-06`, output `1e-05`.
  - `m-old`: input `5e-06`, cache read `5e-07`, output `3e-05`; no cache creation.
  - `m-bare`: input `1e-06`, output `4e-06`; no cache fields.
  - `m-zero`: input `3e-06`, cache read `3e-07`, cache creation `0`, output `1.2e-05`.
  - `m-half`: input `5e-07`, output `5e-07`.
  - `m-text`: input as the string `"2e-06"`, output `1e-05`.
  - `chatgpt/m-sol` (`litellm_provider: chatgpt`, prices null) and `m-azure` (`litellm_provider: azure`).
- **Catalog:** `FAKECODEX_MODELS` lists `m-sol`, `m-old`, `m-bare` and `m-hidden`.
- **Server:** the test server answers `ETag: "e1"` and returns 304 when `If-None-Match: "e1"` arrives. It records every request it receives.

Expected cached prices (US dollars per million tokens):
- `m-sol`: `{input "2", cached_input "0.1", cache_write "2.5", output "10"}`.
- `m-old`: `{5, 0.5, 5, 30}`.
- `m-bare`: `{1, 1, 1, 4}`.
- `m-zero`: `{3, 0.3, 3, 12}`.

### Happy path

- [ ] **refresh writes the cache (FR71, FR72, FR76):** one telemetry run on `m-zero` → `agentcli prices refresh --json` exits 0.
  - The JSON has `ran: true`, `reason: updated` and `changed: true`.
  - `priced` is `[codex/m-bare, codex/m-old, codex/m-sol, codex/m-zero]` and `unpriced` is `[codex/m-hidden]`.
  - `prices.json` holds the expected prices above, `etag` `"e1"` and `source` equal to the server URL.
  - The file has mode 0600, and no `m-azure`, `chatgpt/m-sol` or `m-half` entry.
  - The server saw one GET with no query string, no `Authorization` header and no cookie.
  - The text form (no `--json`) prints exactly `prices: updated (4 priced, 1 unpriced)`.
- [ ] **prices shows the cache (FR73):** after the refresh, `agentcli prices --json` → `cached: true` plus the cache's fields. The text form lists each model with its four prices and the unpriced models.
- [ ] **run cost with estimated writes (FR74, FR75, FR83):** one codex run (a provider that declares implicit caching) on `m-sol` with usage `input 1,000,000, cached 800,000, cache_write 0, output 10,000`.
  - `stats --all --json` gives `usage_by_model.m-sol.cost_usd` `"0.680000"`: 200,000 estimated written tokens at $2.50, 800,000 cached at $0.10 and 10,000 output at $10.
  - `cost_complete` is `true`.
- [ ] **reported writes win (FR74):** the same run with `cache_write 50,000` → `"0.605000"`: 150,000 ordinary at $2, 800,000 cached, 50,000 written and the output.
- [ ] **omitted cache prices (FR71, FR74):**
  - `m-old` with `input 100,000, cached 60,000, output 1,000` → `"0.260000"`, the writes at the input price.
  - `m-bare` with `input 10,000, cached 4,000, output 500` → `"0.012000"`.
- [ ] **exact totals (FR74, FR75):** the `m-sol`, `m-old` and `m-bare` runs above together → `usage_totals.cost_usd` is `"0.952000"`. That equals the independent sum of the three oracles, with no rounding before the sum.
- [ ] **reasoning not double-counted (FR74):** the `m-sol` run with `reasoning_output_tokens 5,000` added → its cost is unchanged.
- [ ] **hook runs count (FR75, US8):** runs with `source` `hook-stop`, `hook-post-commit` and `cli`, all under session `S` → `stats --session-id S --all --json` prices all three.
- [ ] **stats text (FR75):** for the three runs above:
  - the `tokens:` lines carry no cost key;
  - the output has `cost: $0.95`, plus `cost m-sol: $0.68`, `cost m-old: $0.26` and `cost m-bare: $0.01`.
- [ ] **status line cost (FR78, FR92, US8):** stats answers `usage_by_provider.codex` with `cost_usd "0.680000"` and `cost_complete: true`, no `anthropic` key, and one job running → the status line is `💸 1 job running · Codex $0.68`. With no job running, it is `💸 Codex $0.68`; with one job running and `cost_usd` null, it is `💸 1 job running`.
- [ ] **status line rounding (FR78):**

  | `cost_usd` | complete? | Codex part of the status line |
  |---|---|---|
  | `"52.940726"` | yes | `Codex $52.94` |
  | `"1.005000"` | yes | `Codex $1.01` |
  | `"0.005000"` | yes | `Codex $0.01` |
  | `"0.004999"` | yes | `Codex <$0.01` |
  | `"0.000000"` | yes | `Codex $0.00` |
  | `"52.949999"` | no | `Codex ≥$52.94` |
  | `"0.004999"` | no | `Codex ≥$0.00` |
  | null | — | nothing for Codex |

  The same table holds for the Claude part, with the label `Claude` (FR78, FR92).

- [ ] **refresh on compaction (FR77, US8):**
  - A `session.compact` with trigger `manual`, then one with `auto`, both with no `agentId` → each hook resolves before its refresh process finishes, and each schedules one `prices refresh --json` with no `--max-age`.
  - The triggers `precompute` and `plugin`, and any compaction with an `agentId`, schedule none.
- [ ] **refresh on session start (FR77):** `session.start` → one `prices refresh --max-age 24h --json` runs from a timer callback, not inside the hook.
- [ ] **re-read after a change (FR70, FR77):** a refresh answering `ran: true, changed: true` → the next poll runs `stats` again, even though no run ended.
- [ ] **live cost of a running run (FR79):** a background run with provider session `T`, kept running by the fake, and the price cache holding `m-sol`. `CODEX_HOME/sessions/…/rollout-…-T.jsonl` has a `turn_context` with model `m-sol`, then `token_count` lines stamped after the run's start; the newest totals are `input 1,000,000, cached 800,000, cache_write 0, output 10,000, reasoning 2,000`. → `progress <run_id> --json` has `usage` with those counts and `cost_usd` `"0.680000"`.
- [ ] **resumed conversation subtracts what came before (FR79):** the same file also holds a `token_count` stamped before the run's start with totals `input 400,000, cached 300,000, output 5,000`, and the newest totals are `input 1,400,000, cached 1,100,000, output 15,000` → `usage` is `input 1,000,000, cached 800,000, output 10,000` and `cost_usd` is `"0.680000"`.
- [ ] **band shows the cost (FR80):** progress answers `cost_usd "0.123456"` for a flagged run that has run 95 s and whose newest step is `Reading the diff` → the band line is `agentcli · code-review · hook-stop · 1m 35s · $0.12 · Reading the diff`. With `"0.004000"` it shows `<$0.01`, and with null there is no cost part. This is the line with summaries off; with summaries active the last part is the label, and before the first label the line ends after the cost (FR91, below).

### Edge cases

- [ ] **unpriced and missing (FR75):**
  - Runs on `m-sol`, `m-hidden`, `m-new` (in no cache list) and one with no model → `cost_complete: false`.
  - `unpriced_models` is `[m-hidden, m-new, unknown]` and `missing_prices` is `[m-new]`.
  - `usage_totals.cost_usd` covers the `m-sol` run only.
- [ ] **nothing priced (FR75, FR78):** with no cache, or only unpriced runs → `usage_totals.cost_usd` is null and the status line shows nothing for Codex. With no cache, `prices_checked_at` is null and `missing_prices` lists every model used except `unknown`.
- [ ] **no usage (FR75):** a window whose runs all have null usage → `cost_usd` null and `cost_complete: true`.
- [ ] **exact literals (FR72):** `m-sol`'s `1e-07` cache read is stored as `"0.1"`, not as a binary-float approximation.
- [ ] **half to even (FR74):** with `m-half` wanted, one run with `input 1` → `"0.000000"`; one with `input 3` → `"0.000002"`.
- [ ] **non-number price (FR72):** `m-text` wanted → listed as unpriced.
- [ ] **wrong namespace ignored (FR72):** a wanted `m-azure` → unpriced, even though the list has an entry under that key. The `chatgpt/m-sol` key never matches `m-sol`.
- [ ] **conditional request (FR72):** a refresh, then another → the second request carries `If-None-Match: "e1"`.
  - It answers `ran: true, changed: false, reason: not_modified`.
  - `checked_at` advances and `fetched_at` and `models` are unchanged.
- [ ] **new model forces a full GET (FR72):** after a refresh, a telemetry run on `m-old2` (not in the cache) → the next request carries no `If-None-Match`, and `m-old2`, absent from the list, ends up in `unpriced`.
- [ ] **source change forces a full GET (FR72):** the cache's `source` differs from `AGENTCLI_PRICES_URL` → no `If-None-Match`, and the new `source` is written.
- [ ] **fresh cache skips everything (FR72):** with `checked_at` 1 hour old, `prices refresh --max-age 24h --json` → `ran: false, reason: fresh`. The server receives no request, and `FAKECODEX_RECORD` shows `codex` was not run.
- [ ] **stale cache refreshes (FR72):** with `checked_at` 25 hours old, `--max-age 24h` → it refreshes.
- [ ] **catalog fails (FR72, FR76):** `FAKECODEX_MODELS` unset → exit 0. Only the telemetry models are wanted, and `catalog_error` is non-empty.
- [ ] **nothing wanted (FR72):** `FAKECODEX_MODELS` unset and no telemetry → exit 0, `ran: false, reason: nothing_wanted`. The server receives no request and no `prices.json` is written.
- [ ] **ran false describes the cache (FR72):** `reason: fresh` on a cache that has 4 priced and 1 unpriced model → `priced` and `unpriced` list them, and `checked_at` equals the cache's. `reason: off` with no cache → empty lists and `checked_at: null`.
- [ ] **usage errors come first (FR72):** `AGENTCLI_PRICES_URL=off agentcli prices refresh --max-age soon` → exit 2, not `reason: off`.
- [ ] **corrupt cache (FR71):** `prices.json` holding `{` or `"v": 2` → `stats` reports null costs and `prices_checked_at: null`; `prices` reports `cached: false`; `refresh` makes a full GET and replaces the file.
- [ ] **usage errors (FR72):** `prices refresh --max-age soon`, `--max-age -1h`, an unknown flag, or a positional argument → exit 2 with `sdk_status: usage_error`.
- [ ] **no live usage yet (FR79):** no session file, a session file with no `token_count` after the run's start, or a conversation with no provider session id → `progress --json` exits 0 with `usage: null` and `cost_usd: null`.
- [ ] **the start second belongs to the previous turn (FR79):** the session file holds the previous turn's last `token_count` stamped 0.5 s after the run's `started_at` (totals `input 400,000, cached 300,000, output 5,000`) and the run's own one 3 s after (`input 1,400,000, cached 1,100,000, output 15,000`) → `usage` is `input 1,000,000, cached 800,000, output 10,000` and `cost_usd` is `"0.680000"`.
- [ ] **unreadable conversation record (FR79):** the run's conversation file holds `{` → `progress --json` exits 0 with `usage: null`, `cost_usd: null` and one `agentcli: warning:` line naming the run.
- [ ] **unreadable start time (FR79):** the running run's `started_at` is `not a time` → `progress --json` exits 0 with `usage: null`, `cost_usd: null` and one `agentcli: warning:` line naming the run.
- [ ] **unreadable request (FR79):** a session file with `token_count` lines but no `turn_context`, for a run whose `request.json` holds `{` → `usage` is set, `cost_usd` is null and one `agentcli: warning:` line names the run.
- [ ] **ended run reports no live usage (FR79):** a run that has ended (`done`) whose session file holds `token_count` lines, some of them from a later turn → `progress --json` has `usage: null` and `cost_usd: null`.
- [ ] **unreadable session file (FR79):** the session file path is a directory → `progress --json` exits 0 with `usage: null`, `cost_usd: null` and one `agentcli: warning:` line on stderr; with no session file at all, stderr stays empty.
- [ ] **live usage without a price (FR79):** the session file's model is `m-hidden`, or there is no usable price cache → `usage` is set and `cost_usd` is null.
- [ ] **model falls back to the request (FR79):** a session file with `token_count` lines but no `turn_context`, for a run requested with `--model m-sol` → `cost_usd` uses `m-sol`'s prices.
- [ ] **torn session file (FR79):** the session file's last line is cut mid-object → it is ignored, and `usage` comes from the newest complete `token_count`.
- [ ] **compaction during a refresh (FR77):** a compaction arrives while a `--max-age 24h` refresh is still running → no second process starts while it runs. When it ends (even with `reason: fresh`), exactly one queued `prices refresh --json` without `--max-age` starts.
- [ ] **requests coalesce (FR77):** two compactions and a missing-price request arrive while one refresh runs → exactly one queued refresh runs after it, not three.
- [ ] **missing price triggers once (FR77):** stats keeps answering `missing_prices: [m-new]` → exactly one refresh runs for `m-new` in this load.
- [ ] **queued request survives an unchanged refresh (FR77):** `missing_prices: [m-new]` arrives while a refresh that will answer `changed: false` is running → when it ends, the queued refresh runs. If that one answers `changed: true`, the next poll reads stats again, with no new run ending in between.

### Failure and error handling

- [ ] **list unavailable (FR5, FR72):** each of these cases → exit 7, `sdk_status: prices_unavailable`, `reason: unavailable`, a non-empty `error`, and `prices.json` byte-identical (or still absent). Without `--json`, stdout is empty and stderr has one `agentcli: …` line:
  - the server is closed (connection refused);
  - the server answers 500;
  - the body is invalid JSON;
  - the body has no wanted model;
  - the body exceeds `AGENTCLI_TEST_PRICES_MAX_BYTES`;
  - the server stalls past `AGENTCLI_TEST_PRICES_TIMEOUT_MS`.
- [ ] **refresh switched off (FR72):** `AGENTCLI_PRICES_URL=off` → exit 0, `ran: false, reason: off`. No request is made and no file is created.
- [ ] **lock held (FR72):** another process holds `prices.lock` → exit 0, `ran: false, reason: busy`, and the cache is unchanged.
- [ ] **failed refresh in the mod (FR77):** two refreshes in a row exit 7 → one debug log line, not two. After one succeeds, the next failure logs again.

### Idempotency

- [ ] **refresh twice (FR72):** two refreshes with no change upstream → the second has `changed: false`, and `prices.json` differs only in `checked_at`.
- [ ] **reload (FR77):** the mod reloads 3 times within an hour of a refresh → each `session.start` refresh answers `reason: fresh`, and the server receives no request.

### Performance and limits

- [ ] **refresh over a large history (FR72):** with 20,000 telemetry records, `prices refresh` completes. Record its wall time as a baseline.
- [ ] **long session file (FR79, NFR Performance):** a 50 MB session file whose run events sit in its last 20 KB → `progress --json` returns the right `usage`. Record its wall time as a baseline; the file is read from its end.
- [ ] **the age gate reads nothing else (FR72, NFR Performance):** with a fresh cache and an unreadable telemetry directory, `prices refresh --max-age 24h` exits 0 with `reason: fresh`.

## Progress summaries and Claude spend

Fixtures for this section:
- **Price list:** the fixture list of the section above also holds `m-haiku` with `litellm_provider: anthropic`: input `1e-07`, cache read `1e-08`, cache creation `1.25e-07`, output `5e-07`. Its expected cached prices are `{input "0.1", cached_input "0.01", cache_write "0.125", output "0.5"}`. `m-sol` stays an `openai` entry only, and `m-haiku` is in no `openai` entry.
- **Anthropic usage on stdin:** `U1` is `{"input_tokens":458,"output_tokens":18}`. `U2` is `{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":400,"cache_creation_input_tokens":200}`. `U3` is `{"input_tokens":1000000,"output_tokens":100000}`. `U4` is `{"input_tokens":100000,"output_tokens":0,"cache_read_input_tokens":800000,"cache_creation_input_tokens":100000}`.
- **Mixed home:** one codex run on `m-sol` (usage `input 1,000,000, cached 800,000, cache_write 0, output 10,000`, cost `"0.680000"`) and, recorded through `usage add` under the same session, model calls on `m-haiku` with `U3`, `U3` and `U4`.
- **Mod cases:** the kit answers one surface from `session.surfaces` unless the case says otherwise. `progress` answers scripted entries for the flagged run, and `model.complete` answers scripted replies and records its requests.

Expected costs (US dollars):
- `U1` on `m-haiku`: `"0.000055"`. `U2`: `"0.000044"`. `U3`: `"0.150000"`. `U4`: `"0.030500"`. The mixed home's model calls together: `"0.330500"`.

### Happy path

- [ ] **usage add records a model call (FR81, FR82):** `printf '%s' "$U1" | agentcli usage add --session-id S --provider anthropic --model m-haiku --source mod-summary --run-id r-x --json` → exit 0 and stdout `{"sdk_status":"ok","exit_code":0,"recorded":true,"call_id":"m-…"}`.
  - The month file gains one line whose keys are, in this order, `v, kind, call_id, ts, session_id, provider, model, source, run_id, usage`, with `v` 1 and `kind` `model_call`.
  - `call_id` matches `m-<UTC yyyymmddThhmmssZ>-<8 lowercase hex>` and `ts` is RFC 3339 UTC to the second.
  - `usage` is `{"input_tokens":458,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":18,"reasoning_output_tokens":0}`, keys in that order.
- [ ] **usage add text form and no run id (FR82):** the same call without `--json` and without `--run-id` → stdout is the call id alone on one line, and the record has `run_id: null`.
- [ ] **normalization (FR82):** `U2` → the record's usage is `input_tokens 700, cached_input_tokens 400, cache_write_input_tokens 200, output_tokens 10, reasoning_output_tokens 0`.
- [ ] **missing keys read 0 (FR82):** stdin `{"input_tokens":5}` → usage `5, 0, 0, 0, 0`. Stdin `{"output_tokens":3}` → usage `0, 0, 0, 3, 0`, recorded.
- [ ] **run id as given (FR82):** `--run-id` naming no run, or a string that FR9 would refuse (`'a b'`) → exit 0 and the record holds it unchanged.
- [ ] **model calls stay apart from runs (FR81, FR85):** the mixed home → `runs --json` prints the one run; `status --session-id S --json` lists the one run; `stats --all --json` has `total` 1, `by_provider` holding `codex` only, and no `mod-summary` in `by_source`, `by_source_status` or `instrumentation`. `reliability`, `outcomes`, `duration_ms` and `findings` are equal to those of the same home without the model calls.
- [ ] **usage_by_provider per provider (FR85):** the mixed home, prices refreshed → `stats --all --json`.
  - `usage_by_provider.codex` equals `usage_totals` key for key (the home holds codex runs only).
  - `usage_by_provider.anthropic` is `input_tokens 3,000,000, cached_input_tokens 800,000, cache_write_input_tokens 100,000, output_tokens 200,000, reasoning_output_tokens 0, runs_with_usage 3, cost_usd "0.330500", cost_complete true`.
- [ ] **usage_totals unchanged (FR85):** the mixed home and the same home without the model calls → `usage_totals` is byte-equal in both, and so are the text `tokens:` lines and the `cost:` line (`cost: $0.68`).
- [ ] **per-model lines include model calls (FR75, FR85):** the mixed home → `usage_by_model` has an `m-haiku` entry with `cost_usd` `"0.330500"`, and the text output has `cost m-haiku: $0.33` besides `cost m-sol: $0.68`.
- [ ] **Anthropic usage priced at exactly the input rate (FR83):** `U1` on `m-haiku` → `usage_by_model.m-haiku.cost_usd` is `"0.000055"`, not the `"0.000066"` that estimating 458 written tokens at the cache-write rate would give. A model call of `input_tokens 1,000,000` and no output → `"0.100000"`. This also runs as a module test of the cost function with `ImplicitCacheWrites` false.
- [ ] **Anthropic cache tokens priced as reported (FR83, FR82):** `U2` → `"0.000044"`: 100 ordinary at $0.10, 400 cached at $0.01, 200 written at $0.125 and 10 output at $0.50. `U4` → `"0.030500"`.
- [ ] **the gate changes only unflagged usage (FR83):** a module test of the cost function on `m-sol` prices with usage `input 1,000,000, cached 800,000, cache_write 0, output 10,000` → `"0.680000"` with `ImplicitCacheWrites` true and `"0.580000"` with it false (200,000 ordinary at $2, 800,000 cached, 10,000 output). With `cache_write 50,000` both give `"0.605000"`.
- [ ] **codex numbers unchanged (FR83):** every cost case of the section above (estimated writes `"0.680000"`, reported writes `"0.605000"`, omitted cache prices, exact totals, live cost of a running run) gives the same value as before group R, through `stats` and through `progress`.
- [ ] **wanted models include model calls (FR72, FR84):** telemetry holding only model calls on `m-haiku` and `unknown`, `FAKECODEX_MODELS` listing `m-sol` → `prices refresh --json` exits 0 with `ran: true`, `priced` `[anthropic/m-haiku, codex/m-sol]` and no `unknown` anywhere.
  - `prices.json` holds `models.anthropic.m-haiku` as `{input "0.1", cached_input "0.01", cache_write "0.125", output "0.5"}`.
  - The model calls' models count from the whole telemetry: a call 400 days old makes its model wanted.
- [ ] **first Claude cost needs a refresh (FR84, FR92):** a model call on `m-haiku` with `U1` and no cache entry for it → `stats` has `usage_by_provider.anthropic.cost_usd` null, `cost_complete` false and `missing_prices` `[m-haiku]`. After the refresh, a call with `U1` costs `"0.000055"` and `missing_prices` is empty.
- [ ] **stats keys (FR35, FR85):** a home with no records → `usage_by_provider` is `{}`, and the key list is the one of the stats case above.
- [ ] **first summary on the first entry (FR87, FR88):** a flagged run and a surface; the run's first progress entry arrives → exactly one `model.complete` request with `model: claude-haiku-5-5`, `effort: low`, `maxTokens: 40`, `timeoutMs: 15000`, the system prompt of FRAME and a prompt `Newest activity of the agent, oldest first:` followed by `- <entry>`, with no `Previous label` paragraph.
- [ ] **cadence: first entry, then every 3 (FR88):** entries 1 to 7 arrive one at a time, each reply settling before the next entry → requests happen at entries 1, 4 and 7 and at no other.
- [ ] **window and previous label (FR87):** the second request's prompt lists entries 2, 3 and 4 oldest first, one `- <line>` each, then a blank line and `Previous label: <first label> (say something NEW).`.
- [ ] **window cap and cut (FR87):** 14 new entries arrive between two reads → one request whose prompt lists the 10 newest, oldest first. An entry of 1,000 characters appears in the prompt as a line of 300 characters.
- [ ] **agent row streams labels (FR91, FR67):** an agentcli subagent with a surface, the reply `Reading workerlog.go` → the row streams thinking `» Reading workerlog.go` on the first slice after the call settles, and no raw entry is streamed. A slice whose call has not settled streams nothing.
- [ ] **row never waits on a summary (FR91):** the reply is held open for 20 s → the 5-second slices keep their cadence while it is pending, and the 30-second elapsed-time line still appears.
- [ ] **band shows the label (FR69, FR80, FR91):** a flagged run, 95 s old, cost `"0.123456"`, label `Reading workerlog.go` → the band line is `agentcli · code-review · hook-stop · 1m 35s · $0.12 · Reading workerlog.go`.
- [ ] **band before the first label (FR80, FR91, US9):** a flagged run of source `hook-post-commit`, 95 s old, cost `"0.123456"`, its first summary still pending → the band line is `agentcli · code-review · hook-post-commit · 1m 35s · $0.12`, with no step part and no raw entry.
- [ ] **usage recorded after a reply (FR90):** an answered reply with usage `{input_tokens: 458, output_tokens: 18, …}` → one process runs with argv `[bin, 'usage', 'add', '--session-id', <session id>, '--provider', 'anthropic', '--model', 'claude-haiku-5-5', '--source', 'mod-summary', '--run-id', <run id>, '--json']` and stdin equal to `JSON.stringify(usage)`.
- [ ] **only billed calls are recorded (FR90):** an `empty-reply` with non-zero usage and an answered reply whose label is rejected (FR89) → both run `usage add`. A completion whose usage is all zero, a rejection of the call, a timeout and a 429 answer → none runs it.
- [ ] **recording never delays the display (FR90, FR91):** the `usage add` process is held open → the label is shown at once.
- [ ] **status line with two costs (FR78, FR92, US9):** stats answers `usage_by_provider` with `codex.cost_usd "5.820000"` and `anthropic.cost_usd "0.030000"`, both complete, and one job running → `💸 1 job running · Codex $5.82 | Claude $0.03`. With no job: `💸 Codex $5.82 | Claude $0.03`.
- [ ] **status line with Claude alone (FR92):** no job, `codex.cost_usd` null, `anthropic.cost_usd "0.030000"` → `💸 Claude $0.03`. With one job running → `💸 1 job running · Claude $0.03`.
- [ ] **Claude after its first cost (FR92):** `usage_by_provider` holds no `anthropic` key, or `anthropic.cost_usd` null, one job running and Codex `"5.820000"` → `💸 1 job running · Codex $5.82`, with no `|` and no `Claude`.
- [ ] **older stats answer (FR92):** an answer with no `usage_by_provider` and `usage_totals.cost_usd "0.680000"` → `💸 Codex $0.68`, with no Claude part.
- [ ] **re-read after a recorded call (FR90, FR92):** a status read, then a `usage add` that exits 0, with no run ending and no price change in between → the next `showStatus` runs `stats` again and shows the new Claude cost. With no further add, it does not.
- [ ] **counter captured before the read (FR92):** a `usage add` exits 0 while a `stats` read is in flight → after that read, `showStatus` reads `stats` once more.

### Edge cases

- [ ] **all-zero usage records nothing (FR82):** stdin `{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`, and stdin `{}` → exit 0, `recorded: false, call_id: null` with `--json`, nothing on stdout without it, and the month file unchanged (or still absent).
- [ ] **repeated call id (FR81):** a month file holding two `model_call` lines with one `call_id` and different usage → `stats` counts the first only. Two `usage add` invocations with the same stdin → two records with different call ids, both counted.
- [ ] **window filter (FR81):** a model call with `ts` 10 days ago → `stats --days 7` leaves it out, `--days 30` and `--all` count it.
- [ ] **session filter (FR81):** model calls under sessions `S` and `S2` → `stats --session-id S --all --json` counts the `S` calls only. With a session that has none, `usage_by_provider` has no `anthropic` key.
- [ ] **provider appears with usage only (FR85):** a window whose runs all have null usage and that holds no model call → `usage_by_provider` is `{}`; a window with only model calls → it holds `anthropic` alone.
- [ ] **unpriced model calls (FR85, FR75):** model calls on `m-nopr` (in no list) only → `usage_by_provider.anthropic.cost_usd` null and `cost_complete` false; `unpriced_models` lists `m-nopr`. Before any refresh `missing_prices` lists it; after a refresh that finds no price for it, it moves to the cache's `unpriced` and leaves `missing_prices`.
- [ ] **partly priced provider (FR85):** calls on `m-haiku` (`U3`) and `m-nopr` → `cost_usd "0.150000"`, `cost_complete` false, and `unpriced_models` `[m-nopr]`.
- [ ] **namespace is fixed (FR84, FR72):** a model call of provider `anthropic` on `m-sol` (an `openai` entry only) → `unpriced` holds `anthropic/m-sol`. A codex run on `m-haiku` (an `anthropic` entry only) → `unpriced` holds `codex/m-haiku`.
- [ ] **nothing wanted still holds (FR72, FR84):** only model calls on `unknown`, no catalog → `ran: false, reason: nothing_wanted`, with no request and no write.
- [ ] **no catalog for Claude (FR84):** a refresh wanting `anthropic/m-haiku` runs `codex debug models` once and nothing else for Anthropic; `catalog_error` is empty when the codex catalog reads.
- [ ] **valid model calls are not skipped (FR33, FR81):** a file with five valid model calls, one unknown-kind, one unknown-version and one garbage line → `skipped` is `{unknown_kind: 1, unknown_version: 1, unparseable: 1}`.
- [ ] **model call missing a field (FR81):** four `model_call` lines, each without one of `call_id`, `ts`, `provider`, `model` (or with it empty) → `skipped.unparseable` is 4 and `usage_by_provider` holds none of them.
- [ ] **binary that predates the kind (FR81):** the previous release's binary over a file with model calls → it reports them as `skipped.unknown_kind` and its other output is unchanged. Run by hand with that binary.
- [ ] **summaries off in the setting (FR86):** `options.summaries` false, with a surface → no `model.complete` request, the row streams raw entries, the band shows the raw newest step, no `usage add` runs. `options.summaries` undefined → summaries are active.
- [ ] **headless session (FR86):** `session.surfaces` answers `[]` → no request, raw entries in the row and the band. The kit's default is this, so every case that does not opt in is headless.
- [ ] **surfaces read at each summary (FR86):** a surface present at the first entry and gone at the fourth → the first summary is made and the second is not.
- [ ] **manifest setting (FR86):** `plugin/.claude-plugin/plugin.json` has `userConfig.summaries` of type boolean, default true, with a title and a description that name `claude-haiku-5-5` and the Anthropic API.
- [ ] **one in flight per run (FR88):** the first reply held open and 6 more entries arriving → no second request starts while it is pending.
- [ ] **two in flight per session (FR88):** three flagged runs whose first replies are all held open → at no instant are more than 2 requests pending.
- [ ] **429 pause (FR88):** a reply that is an `api-error` with status 429 → no request is made for any run for 60 seconds (none at 59 s, even when entries arrive), and a request is made once 60 seconds have passed.
- [ ] **request deferred under the pause (FR88, FR91):** a label `Reading workerlog.go` is shown on the band, then a 429 answer arrives, then 3 more entries arrive during the pause → no request is made and the band keeps `Reading workerlog.go` (and the row streams nothing new). At 60 s exactly one request is made, whose prompt holds those entries (at most the 10 newest) and the previous label; it is not lost.
- [ ] **request deferred under the caps (FR88, FR91):** the session already has 2 requests pending and a third run's first entry arrives → the third run's request is not made and its band line ends after the cost, with no step part. When one pending request settles, the third request is made at once with the window as it stands then. For one run whose call is pending while 6 entries arrive, a single request follows its settling, holding the 6 entries (the entries since the last request, at most the 10 newest).
- [ ] **label cleaning order (FR89):** the reply `Fi\u0007rst` (an interior control character) → the label `First`. A reply that is only control characters or ANSI sequences → rejected as empty. `First\nSecond` is rejected, not joined into `FirstSecond`.
- [ ] **label cleaning (FR89):** the reply `\u001b[1mReading workerlog.go\u001b[0m` → the label `Reading workerlog.go`. The reply `  Running store tests  \n` → the label `Running store tests`.
- [ ] **label rejection (FR89):** each of an empty reply, a whitespace-only reply, `First\nSecond`, `First\r\nSecond`, `# Heading`, a reply starting with a backtick, `*bold*`, `- item`, `> quote`, and a reply of 101 characters → a failure: the row streams nothing after its first line `agentcli · <type>`, no raw entry is shown, the failure is logged once, and the call is still recorded (FR90). A reply of exactly 100 characters is accepted.
- [ ] **failure keeps the last label (FR89, FR91):** a rejection of the call, an answer with `isAnswered: false` (an `api-error` 500, an `empty-reply`) and a rejected label, each in turn, failing every window of a run → the row streams `agentcli · <type>` and nothing else, no raw entry, and the failure is logged once; the final answer still arrives. The band keeps its old label: `Reading auth.go` shown at 20 s, then three more entries whose request answers an `api-error` 500 → at 30 s the line is still `agentcli · code-review · hook-post-commit · 30s · Reading auth.go`. Before the first label, the line ends after the cost, or after the elapsed time with no cost (a failed first window of a flagged run 20 s old with no cost: `agentcli · code-review · hook-post-commit · 20s`).
- [ ] **label after a failure (FR89, FR91):** an `api-error` 500 on the first window, then the reply `Running store tests` → the row streams `agentcli · <type>`, then `» Running store tests`, with no raw entry between them.
- [ ] **failure logged once per run (FR89):** three failures on one run → one debug log line for that run; a first failure on another run → its own line.
- [ ] **failed recording (FR90):** `usage add` exits 1 twice → the debug log gets one line and `usageAdds` does not move. After one exit 0 it moves by one, and the next failure logs again.
- [ ] **failed recording is kept and retried on each poll (FR90, FR92):** `usage add` exits 70 → the next poll runs it again with the same argv and the same stdin, and every poll after that until it exits 0. The poll that sees the exit 0 increments `usageAdds` and runs `stats` again before the status line is drawn; later polls do not run `usage add` for it.
- [ ] **retries stop at the first failure (FR90):** three failed recordings, oldest first, and `usage add` still failing → a poll runs only the oldest. Once it exits 0, one poll records all three in their original order.
- [ ] **at most 100 pending, oldest dropped (FR90):** 100 recordings fail → no toast. The 101st failure drops the oldest (a debug log line) and shows the toast `some summary costs could not be recorded; the Claude cost is a lower bound` (drawn under the plugin name); a 102nd drops the next oldest, logs it, and shows no second toast. When `usage add` then exits 0, one poll records the remaining 100, oldest first, and the two dropped are never run again.

### Failure and error handling

- [ ] **bad stdin (FR82):** each of stdin that is not JSON, empty stdin, an array, a string and two objects → exit 2 with `sdk_status: usage_error` under `--json`, and nothing written.
- [ ] **bad values (FR82):** each of `-1`, `1.5`, `"5"`, `null` and `true` as a count → exit 2 and nothing written.
- [ ] **extra stdin keys ignored (FR82):** stdin `U1` plus `"service_tier":"standard"` and a nested object under another key → exit 0, and the record is identical to the one for `U1` alone.
- [ ] **ts is the recording time (FR81, FR82):** a `usage add` run between two timestamps taken around it → the record's `ts` lies between them (to the second, UTC), and `--run-id` naming an older run does not change it.
- [ ] **unknown flag or positional (FR82):** `usage add ... --bogus`, and `usage add ... extra` with valid flags and stdin → exit 2 (`sdk_status: usage_error` under `--json`) and nothing written.
- [ ] **bad flags (FR82):** `--provider openai` or `--provider codex`, and a missing or empty `--session-id`, `--model` or `--source`, each with valid stdin → exit 2 and nothing written.
- [ ] **telemetry lock held (FR82):** another process holds the telemetry lock for 10 s → `usage add` exits 70 within the 5-second lock wait and writes nothing. A read-only telemetry directory → exit 70 as well.

### Idempotency

- [ ] **replayed lines (FR81):** the same `model_call` line appended twice → `stats` is equal to a single application.
- [ ] **reload keeps spend (FR81, FR92):** the mod reloads after three recorded calls → the status line still shows the Claude cost from `stats`, since the calls live in telemetry, not in the mod.

### Performance and limits

- [ ] **telemetry volume with model calls (FR81, FR85):** a month file with 20,000 lines, half of them model calls → `stats --all` and `stats --session-id S --all` complete and report them. Record their wall time as a baseline.

## Spawn notice, ETA and spinner

Fixtures for this section:
- **ETA runs:** run records of the scenario `code-review`, outcome `ok` unless a case says otherwise, written into the current month file of the sandbox home, each with a `cwd` and a `duration_ms`. Module cases hand `EstimateETA` folded runs and a key function that keys a directory by itself unless the case names directories that share a repository.
- **Directories:** a git repository with one commit, a linked worktree of it in another temp directory, its subdirectory `pkg/deep`, a plain directory outside git, and a path whose directory no longer exists. The repository's key is the real path of the repository followed by `/.git`.
- **Mod cases:** the session starts with cwd `/work`, and the test clock starts at the Unix epoch. `status` and `eta` answer scripted replies, and the kit records toasts, transcript lines, debug log lines and redraw requests; the spawn tests also watch the writes of `spawnNotified` themselves, through a `state.set` hook. A listing's runs default to `admitted_at` `2026-01-01T00:00:00Z`. Spinner cases run a turn of agent `ag-1` of type `agentcli:second-opinion` beside an `Explore` agent `ag-2`, and draw the engine's `Spinner` with the word `Sauteing` and a request id.

### Happy path

- [ ] **repo basis and JSON (FR94):** six runs of 150000 ms and six of 162000 ms in a plain directory `D` → `agentcli eta --scenario code-review --cwd D --json` exits 0 with stdout exactly `{"sdk_status":"ok","exit_code":0,"scenario":"code-review","repo":"D","basis":"repo","eta_ms":156000,"samples":12,"repos":1}` and a newline, keys in that order.
- [ ] **worktree and subdirectory share the key (FR94):** a run of 100000 ms in the repository and one of 200000 ms in its worktree → asked from the repository, the worktree and the subdirectory alike, `repo` is the repository's key, `basis: repo`, `eta_ms` 150000, `samples` 2, `repos` 1.
- [ ] **repo basis ignores other repositories (FR94):** module case, `/work/a` and `/work/a-sub` sharing the key `/work/a/.git`: runs of 100000 ms in `/work/a`, 200000 in `/work/a-sub` and 900000 in `/work/b`, asked from `/work/a` → `basis: repo`, `eta_ms` 150000, `samples` 2, `repos` 1, `repo` `/work/a/.git`.
- [ ] **global basis is the mean of the repo means (FR94):** module case, the same shared key: runs of 100000 and 200000 ms in `/work/a`, 300000 in `/work/a-sub` and 1000000 in `/work/b`, asked from `/work/c` → `basis: global`, `eta_ms` 600000 (the pooled mean would be 400000), `samples` 4, `repos` 2, `repo` `/work/c`.
- [ ] **text lines (FR94):** asked from a directory `A`, without `--json`, each exits 0 with one line on stdout:
  - the twelve runs of the first case in `A` → `ETA ~2m 36s (repo average, 12 runs)`;
  - eight runs in eight other directories, alternating 140000 and 146000 ms → `ETA ~2m 23s (global average, 8 repos)`;
  - one run of 59999 ms in `A` → `ETA ~59s (repo average, 1 run)`;
  - one run of 60000 ms in one other directory → `ETA ~1m 0s (global average, 1 repo)`;
  - no run → `ETA unknown (no history)`.
- [ ] **the whole history counts (FR94):** in `D`, a run of 40000 ms recorded 120 days ago and one of 20000 ms an hour ago, in their own month files → `basis: repo`, `eta_ms` 30000, `samples` 2.
- [ ] **cwd defaults to the working directory (FR94):** a run of 30000 ms in `H` and one of 90000 ms in `O`, then `eta --scenario code-review --json` run from `H` with no `--cwd` → `repo` `H`, `basis: repo`, `eta_ms` 30000.
- [ ] **spinner says the type (FR95, US10):** a surface, the summary still pending → `ag-1`'s spinner is drawn as `agentcli · second-opinion`, at the turn's first wait and again after its first raw entry is read.
- [ ] **spinner shows the label (FR95):** the label `Reading auth.go` settles → `ag-1`'s spinner is drawn as `Reading auth.go`, with no `» `.
- [ ] **redraw on a settled label (FR95):** no redraw is requested before the label settles; after it, the mod has asked once to redraw `ui.render`.
- [ ] **hook run seen running (FR96, US10):** the first listing holds a running run of scenario `code-review` and source `hook-post-commit`, and `eta` answers `basis: repo, eta_ms 156000, samples 12` → one toast and one transcript line, both `agentcli · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)`. One `eta` runs, through the plugin's shim, with the arguments `eta --scenario code-review --cwd /work --json`.
- [ ] **the other ETA texts (FR96, US10):** `eta` answers, in turn:
  - `basis: global, eta_ms 143000, repos 8` → the line ends `· ETA ~2m 23s (global average, 8 repos)`;
  - `basis: none` → `· ETA unknown (no history)`;
  - `basis: repo, eta_ms 59999, samples 1` → `· ETA ~59s (repo average, 1 run)`;
  - `basis: global, eta_ms 61000, repos 1` → `· ETA ~1m 1s (global average, 1 repo)`.
- [ ] **ETA lookup given ten seconds (FR96):** the `eta` run of a notice has a timeout of 10000 ms.
- [ ] **agent type turn announced after admission (FR96):** a turn of an `agentcli:second-opinion` agent, with `eta` held → the turn's answer (`Codex: done.`) arrives before any `eta` runs. Once the timer fires, `eta --scenario second-opinion --cwd /work --json` runs and no toast shows while it is held; when it answers, the toast is `agentcli · spawned second-opinion · trigger agent · ETA ~2m 36s (repo average, 12 runs)`. Two later listings that list the run show no other toast and run no other `eta`.
- [ ] **ask and send announced with the run's own values (FR96):** for `ask`, then for `send`, with `status` and `eta` held → the tool returns `{conversation_id, run_id}` and no toast has shown. Once the timer fires, the mod runs `status run-1 --json` (the run reports scenario `delegation`, source `mod`), then `eta --scenario delegation --cwd /work --json`, then shows `agentcli · spawned delegation · trigger mod · ETA ~2m 36s (repo average, 12 runs)`. Two later listings of the run show no other toast.
- [ ] **ask notice starts from a timer (FR96):** right after the `ask` tool returns, no `status` read has run; once the timer advances, the notice shows.
- [ ] **run first seen finished (FR96, US10):** after an empty first listing, a listing holds a `done` run of source `hook-stop` → a toast and a transcript line, no `eta` run, ending:
  - `· finished in 3s` with `started_at` `2026-01-01T00:00:01Z` and `ended_at` `2026-01-01T00:00:04Z`;
  - `· finished in 1m 35s` with no `started_at` and `ended_at` `2026-01-01T00:01:35Z` (counted from `admitted_at`);
  - `· finished` with no `ended_at`, and with `started_at` `yesterday`.

### Edge cases

- [ ] **every outcome counts, other scenarios do not (FR94):** module case: runs in `/work/a` of 100000 (`ok`), 200000 (`error`), 300000 (`timeout`), 400000 (`lost`), 500000 (`cancelled`) and 600000 ms (no outcome), plus one of another scenario at 90000000 ms → `basis: repo`, `eta_ms` 350000, `samples` 6.
- [ ] **runs without a cwd or a duration are dropped (FR94):** module case asked from `/work/z`: 100000 (`ok`) and 300000 ms (`error`) in `/work/a`, 600000 (`lost`) in `/work/b`, a run with an empty `cwd` at 90000000, a run of another scenario, a run in `/work/d` with no `duration_ms` and one in `/work/e` with a null one → `basis: global`, `eta_ms` 400000, `samples` 3, `repos` 2.
- [ ] **none basis (FR94):** no run of the scenario, asked from `D` → exit 0 and stdout exactly `{"sdk_status":"ok","exit_code":0,"scenario":"code-review","repo":"D","basis":"none","eta_ms":null,"samples":0,"repos":0}` and a newline.
- [ ] **not a repository, directory gone (FR94):** a run of 1000 ms in the plain directory and one of 3000 ms in the gone directory → asked from each, `repo` is that directory string itself, `basis: repo`, `eta_ms` 1000 and 3000, `samples` 1, `repos` 1.
- [ ] **git cannot run (FR94):** no `git` on `PATH`; a run of 50000 ms in the repository's subdirectory `pkg`, asked from the repository → each directory is its own key: `repo` is the repository directory, `basis: global`, `eta_ms` 50000, `samples` 1, `repos` 1.
- [ ] **symbolic links resolved (FR94):** a directory `D` and a link `L` to it; a run of 20000 ms recorded in `D` and one of 40000 ms recorded in `L` → asked from `D` and from `L`, each gives `repo` `D` (resolved), `basis: repo`, `eta_ms` 30000, `samples` 2, `repos` 1.
- [ ] **relative --cwd (FR94):** runs of 20000 and 40000 ms in `<P>/project`, then `eta --scenario code-review --cwd project --json` run from `<P>` → `repo` `<P>/project`, `basis: repo`, `eta_ms` 30000, `samples` 2.
- [ ] **each directory resolved once (FR94):** module case: two runs in `/work/a` and one in `/work/b`, asked from `/work/a` → the key function is called once for each directory.
- [ ] **half away from zero (FR94):** module case: runs of 1000 and 1001 ms → `eta_ms` 1001.
- [ ] **other spinners untouched (FR95):** while `ag-1`'s turn runs, the spinners of `ag-2` and of `main` are drawn as `Sauteing`.
- [ ] **spinner after the turn (FR95):** once `ag-1`'s turn has ended, its spinner is drawn as `Sauteing`.
- [ ] **spinner without summaries (FR95, FR86):** `summaries: false`, after the first raw entry is read → `ag-1`'s spinner is drawn as `agentcli · second-opinion`, and no model request is made.
- [ ] **spinner logged once per request id (FR95):** `ag-1`'s spinner drawn twice and `main`'s once → the debug lines that mention the Spinner are exactly `Spinner raised for ag-1 with the word Sauteing` and `Spinner raised for main with the word Sauteing`.
- [ ] **engine message stays (FR95):** the engine draws `ag-1`'s spinner with the message `Compacting conversation` → it is drawn as `Compacting conversation`.
- [ ] **baseline (FR96):** the first listing of a load holds a running run and a `done` run with no `ended_at` → only the running one is announced, and one `eta` runs.
- [ ] **finished runs of the baseline (FR96):** the first listing holds one `done` run of source `hook-stop`, in turn:
  - ended after the load (`started_at` `1970-01-01T00:00:01Z`, `ended_at` `1970-01-01T00:00:04Z`) → `agentcli · spawned code-review · trigger hook-stop · finished in 3s`;
  - ended in the load's second (both `1970-01-01T00:00:00Z`) → `… · finished in 0s`;
  - ended before it (`ended_at` `1969-12-31T23:59:59Z`), with no `ended_at`, or with `ended_at` `yesterday` → no notice.
  No `eta` runs in any of them.
- [ ] **load second compared whole (FR96, US10):** the test clock advanced to 100 ms before `session.start`; the first listing holds a `done` run that ended at `1970-01-01T00:00:00Z` and one that ended at `1969-12-31T23:59:59Z` → only the first is announced, `agentcli · spawned code-review · trigger hook-stop · finished in 0s`.
- [ ] **new baseline at each session start (FR96):** an empty first listing, then a `session.start` within the same load, then a listing with a `done` run never seen → no notice.
- [ ] **notified ids capped (FR96):** a first listing of 501 `done` runs, `r-500` newest to `r-0` oldest → `spawnNotified` holds 500 ids, `r-500` and `r-1` among them and `r-0` not, and no notice shows.
- [ ] **every run claimed before any notice (FR96):** the first listing holds a newer `done` run `run-e` and an older running run `run-h`, with `eta` held → while it is held, `spawnNotified` already holds both ids. After it answers, the only toast is `run-h`'s, and the next listing adds none.
- [ ] **admission and listing announce once (FR96):** an `ask` admission whose `eta` is held, then a listing that lists the same run → the listing runs no second `eta`, and after the release exactly one toast shows.
- [ ] **two claims together (FR96):** an `ask` admission and a listing claim two runs while the read of `spawnNotified` is held → the second read waits for the first; after the release both notices show and `spawnNotified` holds `run-1` and `run-h`.

### Failure and error handling

- [ ] **missing --scenario (FR94):** `agentcli eta --json` → exit 2 with `sdk_status: usage_error` and `exit_code` 2; `agentcli eta` → exit 2.
- [ ] **ETA unavailable (FR96, US10):** `eta`, in turn, exits non-zero; prints `ETA ~2m`; prints an object with no known basis; a `repo` average with no `samples`; a `global` average with no `repos`; a count of `-1`; a count of `1.5`; a `repo` average with `eta_ms` null; or is rejected → the toast and the transcript line end `· ETA unavailable`.
- [ ] **ETA failure logged once per load (FR96):** two runs whose `eta` exits 1 with stderr `agentcli: no git` → two toasts and one debug line, `could not read the ETA of code-review: agentcli exited 1: no git`. After another `session.start`, a third such run adds a second line.
- [ ] **failed listing logged once until one succeeds (FR96):** the listing exits 1 with stderr `agentcli: store locked`, or is rejected, for three periods → one debug line starting `could not list the runs of the session for spawn notices: ` (`agentcli exited 1: store locked` for the exit). One listing succeeds, then two more fail → a second line.
- [ ] **incomplete listed run skipped (FR96):** for each of `scenario`, `source` and `state`, the first listing holds `run-h` without it → no toast, and the debug log is exactly `skipped run run-h in the spawn scan: the listing gives it no scenario, source or state`. The next listing gives `run-h` all three → the toast `agentcli · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)`.
- [ ] **unreadable listing is a failed scan (FR96):** the listing prints `not json`, then `{"sdk_status":"ok"}` → one debug line, `could not list the runs of the session for spawn notices: the listing could not be read`. A listing with a `done` run then follows → no notice, since it is the baseline.
- [ ] **status read fails at admission (FR96):** after an `ask`, `status run-1 --json` exits 1 or is rejected → the tool returns its ids, no toast shows and no `eta` runs; the next listing announces `agentcli · spawned delegation · trigger mod · ETA ~2m 36s (repo average, 12 runs)`.
- [ ] **status read failure logged once per load (FR96):** `status <run_id> --json` exits non-zero, is rejected, prints `not json`, or prints a run with no scenario, for two `ask` admissions → one debug line, starting `could not read run run-1 to announce it: `.

### Idempotency

- [ ] **listings, polls and a reload (FR96, US10):** a running run announced by the first listing, then two more listings and a 15-second poll → one toast, and `spawnNotified` is `['run-h']`. After another `session.start` and two listings, still one toast and one `eta` run.
- [ ] **announced by an earlier load (FR96):** a run announced in one load; the next load's first listing is empty, then lists the run `done` → no second notice.

## Edge Cases

- [ ] **two prompt sources (FR1):** `agentcli exec --prompt-file p.md "q"` → exit 2 before spawning; the fake was not invoked.
- [ ] **no prompt (FR1):** `agentcli exec` → exit 2.
- [ ] **review with prompt (FR2):** `agentcli review --base main "focus"` → exit 2 before spawning, with a message that review targets take no prompt.
- [ ] **review with two targets (FR2):** `--base main --uncommitted` → exit 2.
- [ ] **cwd outside git (FR13):** `--cwd` set to a non-git temp dir → the plan contains `--skip-git-repo-check`. Inside a git repo it does not.
- [ ] **never ephemeral (FR13):** every dry-run plan → no `--ephemeral`.
- [ ] **reserved passthrough (FR14):** each of `-- -o x`, `-- --json`, `-- -C /x`, `-- -m m`, `-- -s read-only`, `-- --dangerously-bypass-approvals-and-sandbox`, `-- --approve-for-me`, `-- --ephemeral`, `-- -c model=x`, `-- -c model_reasoning_effort=low`, `-- -c sandbox_permissions=[]` → exit 2 with the offending flag named.
- [ ] **allowed passthrough (FR14):** `-- -c features.web_search=true --add-dir /tmp/x` → present in the plan after the SDK's flags.
- [ ] **danger sandbox (FR14):** `--sandbox danger-full-access` → exit 2.
- [ ] **run-id validation (FR9):** `--run-id 'a b'`, `--run-id ..`, `--run-id .hidden`, `--run-id -x` → exit 2 each, with no directory created outside `runs/`. `--run-id ok.id-1` → accepted and used as the directory name. Reusing it → exit 2.
- [ ] **provider mismatch on send (FR20):** a codex conversation → `agentcli send <conversation_id> --provider other "x"` exits 2. Omitting `--provider` uses `codex`.
- [ ] **link without install record (FR43):** no installed-plugins record for `agentcli@agentcli` → `agentcli link` exits 4 with a message. `agentcli link --target /work/agentcli-shim` writes a launcher executing that path with the running binary's `build_seq`.
- [ ] **stats skipped counts (FR33):** one unknown-kind, one unknown-version and one garbage line → `stats --json` has `skipped: {unknown_kind: 1, unknown_version: 1, unparseable: 1}`. `runs` prints one stderr line with the same counts.
- [ ] **stats default window (FR35):** records at 3 days and at 10 days old → `agentcli stats --json` counts only the 3-day record and reports `window_days: 7`.
- [ ] **background with stdin (FR21):** `printf 'long prompt' | agentcli exec --background -` → `prompt.md` holds the full text before the worker starts, and the fake receives it.
- [ ] **concurrent send (FR19, US2):** while a turn-2 job is `running`, `agentcli send <conversation_id> "x"` → exit 3 naming the active run id. The same holds while it is `queued`.
- [ ] **simultaneous admissions (FR19):** two `send` admissions launched at the same instant on an idle conversation → exactly one is admitted and the other exits 3. Turn numbers have no duplicates or gaps.
- [ ] **not resumable (FR20):** turn 1 fails before any `thread.started` → `agentcli send <conversation_id> "x"` exits 6 with the reason.
- [ ] **per-turn override isolation (FR20):** turn 2 with `--effort low` → turn 3 without flags uses the first-turn default effort.
- [ ] **follow-up cwd (FR20):** turn 1 in `/work/a`; `send` issued from `/work/b` → the plan's `-C /work/a`.
- [ ] **empty output (FR28):** exit 0 with an empty `-o` file → outcome `empty`; the exit code passes through as 0.
- [ ] **review usage zero (FR15):** fixture `review-ok` with all-zero usage → record `usage: null`.
- [ ] **unknown events (FR15):** a fixture with an extra unknown event type and one garbage line → the run completes normally and `state.json` has `unparsed_events: 1`.
- [ ] **month boundary fold (FR30, FR33):** a run with `ts` on 2026-09-30T23:59:50Z whose record is appended on 2026-10-01 lands in `2026-10.jsonl`. Its annotation lands in `2026-10.jsonl` too. A `--days` window starting in September returns the run with folded attrs, and the reader opened both month files.
- [ ] **security permissions (NFR Security):** after any run → the home, `runs/<id>` and `conversations` directories are mode 0700 and their files 0600.
- [ ] **prompt never in telemetry (NFR Security):** a prompt containing a unique marker string → no telemetry file contains the marker. `prompt.md` does.
- [ ] **null is a value (FR33):** annotate `--attr-json k=null` after `k=1` → the folded `attrs.k` is `null` and the key is present.
- [ ] **unknown kind/version (FR33):** inject `{"v":2,"kind":"run",...}` and `{"v":1,"kind":"note"}` → both skipped; reader output reports a skipped count of 2.
- [ ] **torn tail (FR30):** a month file ending mid-record without a newline → the next append starts on a new line, the torn line is counted as unparseable, and the new record parses.
- [ ] **launcher foreign file (FR43):** `~/.local/bin/agentcli` is a non-launcher file (e.g. a pipx entry point) → `link` leaves it untouched and prints why; with `--quiet` it prints nothing. Exit 0 in both.
- [ ] **launcher opt-out (FR43):** `AGENTCLI_NO_LINK=1 agentcli link` → no file written.
- [ ] **unsupported platform (FR41):** shim run with `uname` stubbed to `FreeBSD` → prints the supported platforms, exit 70.
- [ ] **arch aliases (FR41):** `uname -m` stubbed to `aarch64`, then `arm64` → both select the `arm64` binary. `x86_64` and `amd64` both select `amd64`.
- [ ] **build_seq from full history (FR40):** the CI checkout is full-history, and the published binary's `build_seq` equals `git rev-list --count` of the built commit.
- [ ] **PII guard (FR44):** the test generates, at run time, a tracked file holding a webmail address → the CI test fails. The same for a generated `/home/<user>/…` path and for a UUID not on the fixture allowlist. Noreply/example emails pass. No such sample is committed literally.
- [ ] **hook keeps artifacts (FR52):** a migrated review hook gets a clean review → the hook stays silent and the run's `output.md` still exists afterwards. The reflection hook's `no new entry` run carries `reflection.applied: false`, and a run that wrote carries `true`.
- [ ] **hook env passthrough (FR12, FR52):** the reflection hook runs with its inherited lock marker set → the fake provider observes that environment variable with the same value.
- [ ] **prune usage errors (FR60):** `prune --older-than x`, `--older-than -1`, `prune some-id`, an unknown flag and `AGENTCLI_RETENTION_DAYS=soon` → exit 2 (JSON: `sdk_status: usage_error`), and no run is removed.
- [ ] **prune qualification (FR60):** a home with old `done`, `failed`, `cancelled`, `timeout` and `lost` runs, a fresh run, a `running` run, a `queued` run, a terminal run with no `ended_at` or an unparseable one, a run directory with no `state.json`, and one with a malformed `state.json` → only the five old terminal runs are removed. Result: `removed: 5`, `kept: 3`, `skipped: 4`, and `bytes_freed` equal to the size of the removed directories.
- [ ] **prune held lock (FR60):** an old terminal run whose run lock another process holds → kept, counted as kept, directory intact.
- [ ] **prune re-check under the lock (FR60):** between the first look and taking the lock, a run turns `running`, or its `ended_at` becomes recent, or a conversation names it as the active turn → the run is kept.
- [ ] **prune active marker (FR60):** an old terminal run that some conversation's `active_run_id` names → kept, and a different old run in the same home is removed.
- [ ] **prune index tidy (FR62):** a session index whose runs were all removed is deleted; an index that still has a run keeps only the ids whose run directory exists.
- [ ] **prune leftovers (FR60):** a `runs/.prune-*` directory left by an interrupted pass is deleted by the next pass and is never listed as a run.
- [ ] **auto prune once a day (FR61):** `prune.stamp` one hour or 23 hours old → `prune --auto` does nothing and leaves the stamp. 25 hours old, in the future, or unreadable → it prunes and rewrites the stamp.
- [ ] **auto prune opt-out (FR61):** `AGENTCLI_NO_PRUNE=1 agentcli prune --auto` → exit 0, nothing printed, no run removed, no stamp written.
- [ ] **auto prune on an empty home (FR61):** the home directory does not exist → exit 0, nothing printed, the home is not created.
- [ ] **auto prune already running (FR61):** another process holds `prune.lock` → exit 0 quietly, no run removed, no stamp written.
- [ ] **auto prune dry run (FR61):** `prune --auto --dry-run --json` reports what would go (`ran: true`), removes nothing and writes no stamp. `--auto --json` on a gated day prints one object with `ran: false`.
- [ ] **auto prune time budget (FR61):** twelve old runs and a budget shorter than the time to handle them (`AGENTCLI_TEST_PRUNE_BUDGET_MS`, `AGENTCLI_TEST_PRUNE_STEP_MS`) → exit 0, silent, some runs removed and some left. A second pass the same day changes nothing; with the stamp 25 hours old, the next pass removes the rest.
- [ ] **conversation without recorded sources (FR63):** a conversation record with no `*_source` fields and a first turn whose `request.json` exists → `send` takes the sources from that request. With the request gone → `profile`.
- [ ] **session ids are file-safe (FR62):** a session id with a path separator, `..`, a leading dot, `_` or 300 characters → indexed in a file inside `index/sessions/`, never shared with another session or with the empty-session key.
- [ ] **concurrent index appends (FR62):** forty admissions to one session at once → forty intact lines.
- [ ] **mod notice size (FR47):** output of 20 KiB → the notice contains the first 8 KiB and the output path.

---

## Failure & Error Handling

- [ ] **provider missing (FR5):** no `codex` on PATH → exit 127, outcome `error`, `error_excerpt` names the missing binary, a telemetry record is written, `state.json` is `failed`.
- [ ] **model rejected (FR15, FR16):** fixture `error-400` with exit 1 → exit 1, outcome `error`, `error_excerpt` is the event's message (≤ 200 chars, no ANSI), stderr only informational.
- [ ] **stderr fallback (FR16):** non-zero exit, no error events, stderr ending in `fatal: boom` → `error_excerpt` = `fatal: boom`.
- [ ] **timeout with descendants (FR26, US3):** the fake spawns a grandchild sleeper and ignores SIGTERM; `--timeout 2` → exit 124 within 2 + 15 s, outcome `timeout`, no surviving descendant processes (checked by process group), output path still printed.
- [ ] **foreground interrupt (FR26):** SIGTERM to `agentcli` while the fake runs → the provider group is gone, outcome `cancelled`, exit 143.
- [ ] **foreground visible to other shells (FR26):** while a foreground run is in progress → `agentcli status <run_id>` from another shell reports `running`, and `wait` returns when it finishes.
- [ ] **worker crash → lost (FR23):** SIGKILL the worker of a running job → the next `agentcli status <run_id>` reports `lost`. `wait` exits 125. The provider group recorded in the state is killed. The conversation is no longer busy. One telemetry record with outcome `lost`.
- [ ] **queued is not lost during admission (FR23):** a `status` issued while a job is `queued` and less than 30 s old with its lock free → it stays `queued`, not `lost`. After 30 s without a lock holder → `lost`.
- [ ] **orphan provider does not keep the lock (FR23):** SIGKILL only the worker while the fake provider keeps running → the run lock is free immediately, `status` reports `lost`, and the provider group is killed.
- [ ] **cancel after provider exit (FR24):** the provider exits 0, then a `cancel` lands before the worker finalizes → the state is `done` with the provider's exit code.
- [ ] **cancel a foreground run (FR24, FR26):** `agentcli cancel <run_id>` against a foreground run → the foreground command exits 130, outcome `cancelled`.
- [ ] **wait returns recorded exit (FR7):** `wait` on a run that ended with provider exit 1 → exits 1. On a foreground run cancelled by a bare SIGTERM → exits 143.
- [ ] **failed admission is recorded (FR21):** a worker that never reaches `running` → admission exits 70 and a telemetry record with outcome `error` exists for that run id.
- [ ] **send with missing cwd (FR20):** the conversation's working directory was deleted → `send` exits 2. `send --cwd <existing dir>` proceeds in that directory for that turn only.
- [ ] **launcher after uninstall (FR43):** the launcher's target directory removed → running `~/.local/bin/agentcli version` prints that the plugin is not installed and exits 127.
- [ ] **lost with reused pgid (FR23):** worker SIGKILLed and the provider gone; the recorded provider pgid now belongs to an unrelated process with a different start time → reconciliation marks `lost` and does not signal the unrelated process.
- [ ] **state write failure (FR29):** `state.json` replacement fails at finalization (read-only run dir injected by the test) → the foreground exit code is the provider's, with a warning on stderr. A later `status` from another shell reconciles the run as `lost`.
- [ ] **cancel running (FR24):** a job running on a slow fake → `agentcli cancel <run_id>` exits 0. Within 5 s + margin the state is `cancelled` (provider group gone), `wait` exits 130, and the telemetry outcome is `cancelled`.
- [ ] **cancel queued (FR24):** a job forced to stay `queued` (worker start blocked by test hook) → `cancel` marks it `cancelled` itself; the worker, if it later starts, exits without running the provider.
- [ ] **cancel terminal (FR24):** `cancel` on a `done` run → exit 0, state unchanged.
- [ ] **worker never reaches running (FR21):** worker start made to stall past 10 s → admission exits 70, state `failed`, the conversation marker is cleared, and no orphan worker remains.
- [ ] **telemetry unwritable (FR29, FR30):** telemetry directory read-only → the run's exit code is the provider's, with one warning line on stderr. The output and state are intact.
- [ ] **telemetry lock held (FR30):** another process holds the telemetry lock for 10 s → the run finishes within the 5 s lock wait and skips the record with a warning; the exit code is unchanged.
- [ ] **unknown ids (FR7, FR32):** `status`, `wait`, `result`, `cancel`, `send` and `annotate` with a nonexistent id → exit 4.
- [ ] **unsupported capability (FR11):** a test provider declaring no review support → `agentcli review --provider testprov --base main` exits 2 naming the capability.
- [ ] **pruned ids are not found (FR60):** after a prune, `status`, `wait`, `result`, `cancel`, `annotate` (by run id and by output path) and `send <run id>` on a pruned run all exit 4, and `status --session-id S` no longer lists it.
- [ ] **run vanishes mid-read (FR60):** a `status`, `wait`, `result`, `cancel`, `annotate` or `send <run id>` whose run directory is removed after it first read the run (test gate `AGENTCLI_TEST_READ_GATE`) exits 4; `result` does not report success with no output.
- [ ] **auto prune failure is loud (FR61):** the `runs` directory is read-only so a removal fails → `prune --auto` exits 70 and prints the reason on stderr.
- [ ] **indexed run directory gone (FR62):** the index lists a run whose directory was deleted → `status --session-id S` skips it without a warning and lists the others.
- [ ] **CI stale publish (FR40):** the dist workflow run for commit A finishes after commit B landed → the HEAD check fails and `dist` is not updated by A.
- [ ] **version not semantic (FR93):** `VERSION` is `0.4`, `v0.4.0`, `0.04.0`, `0.4.0-`, `0.4.0-rc..1`, `0.4.0+`, or a valid version followed by a second line → `publish-release.sh` exits 2 saying the version is not a semantic version, and `gh` is never called.

---

## Idempotency / Re-run Safety

- [ ] **legacy import re-run (FR37, US7):** import the legacy file twice → the second run adds 0 records, and the total equals the legacy line count (1,000 on the owner's file).
- [ ] **legacy duplicate lines (FR37):** a legacy file containing two byte-identical lines → two distinct imported runs (different occurrence index), and the re-run still adds 0.
- [ ] **stats parity (FR35, success metric):** fresh home + imported legacy file → `agentcli stats --all --json` equals the retired analyzer's `--all` output on the same file for every key the analyzer emits.
- [ ] **link re-run (FR43):** `link` twice with the same build → the second is a no-op (file unchanged, mtime unchanged).
- [ ] **link no downgrade (FR43):** a launcher with `build_seq` 50, then `link` from a build with `build_seq` 40 → the launcher is unchanged. From 60 → updated.
- [ ] **link stale path (FR43):** the launcher names an install path that was deleted → `link` from any build rewrites it.
- [ ] **annotate replay (FR33):** the same annotation appended twice → the folded attrs are identical to a single application.
- [ ] **mod dedup across reload (FR47):** a job finishes, its notice is accepted, then `/reload-plugins` → no second notice for that run id.
- [ ] **mod notice window (FR47):** 25 jobs of the session end between two ticks → 25 notices, read by run id; 60 pending entries → 50 status reads this tick, oldest first, the rest next tick.
- [ ] **mod notice rotation (FR47):** 60 pending entries, the 50 oldest still running and the 10 newest done → all 10 notices are submitted within two ticks.
- [ ] **mod notice write failure (FR47):** the queue write fails at admission → one toast, the next tick writes the entry, and the job's notice is delivered when it ends; a store that never accepts the write → the notice is delivered once from memory.
- [ ] **mod notice retry (FR47):** the host drops the notice (`{ drop }`) or the submit rejects → the entry stays and the next tick submits it again, unmarked.
- [ ] **mod notice repeat (FR47):** a reload finds an entry with an attempt recorded → the notice is delivered with ` (possibly repeated)` after the outcome.
- [ ] **mod notice pruned run (FR47):** `status <run_id>` exits 4 → the entry leaves the queue and one debug-log line names it.
- [ ] **mod abandoned queues (FR47):** another session's queue whose newest entry is 31 days old → removed at session start; one 2 days old → kept.
- [ ] **dist reproducible (FR40):** `make dist` twice on the same commit → identical binaries (byte-equal) and identical `SHA256SUMS`.
- [ ] **existing release kept (FR93):** the fake `gh` already has release `v0.3.0` → `publish-release.sh` exits 0, says the release exists, and runs no `gh release create`, so the tag stays on the build it named.

---

## Performance & Limits

- [ ] **wrapper overhead (success metric):** 50 foreground runs against a no-op fake → p50 of (`agentcli` wall time − fake wall time) ≤ 50 ms; report p50/p95. *Target is an assumption under validation.*
- [ ] **hook budget (FR26, US3):** `--timeout 300` with a fake that never exits and ignores SIGTERM → the command returns in ≤ 315 s.
- [ ] **large prompt (FR1, FR21):** a 2 MiB prompt via stdin, foreground and background → delivered intact (byte-equal at the fake), no pipe deadlock.
- [ ] **large output (FR27):** a 5 MiB output file → `result` prints it fully; `output_bytes` is correct.
- [ ] **stderr tail bound (FR27):** the fake writes 10 MiB to stderr → `stderr.tail` is ≤ 64 KiB and holds the last bytes.
- [ ] **status polling cost is flat (FR62):** 200 run directories of other sessions, each with an unreadable `state.json`, plus one indexed run of session X → `status --session-id X` lists that run, exits 0 and prints no warning: the other directories were never opened.
- [ ] **telemetry volume (FR34, FR35):** a month file with 20,000 records → `runs --all` and `stats --all` complete and report all records. Record their wall time as a baseline.
- [ ] **zero runtime dependencies (success metric, US5):** in a minimal container with only `/bin/sh`, coreutils and the fake provider (no Go, Python, Node, jq) → the plugin's shim and binary run exec, review, send, background, wait, runs and stats successfully.

---

## Notes

- Lifecycle cases (timeout, cancel, lost, concurrent admissions) run on Linux in Docker and on the macOS CI runner.
- Real-provider cases (FR57–FR59) run on the owner's machine and consume provider usage. They are not part of CI.
- The mod cases require a Claude Code build with mods enabled. They run through the plugin test runner where possible and manually in a live session for the toast/turn behaviour.
- A live `prices refresh` against the real price list, and the comparison of `stats` cost with an independent exact recomputation over the owner's telemetry (success metric), run on the owner's machine. They are not part of CI.
- A real progress summary against the Anthropic API, and the comparison of the Claude cost `stats` reports with the usage the API returned, run on the owner's machine in a session with a screen. They are not part of CI.
- Fixtures are recorded from codex-cli 0.159.3 and sanitized (fictitious thread/session ids, neutral paths) before they enter the repository (FR44).
