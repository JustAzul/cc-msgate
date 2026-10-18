# msgate

[![ci](https://github.com/JustAzul/cc-msgate/actions/workflows/ci.yml/badge.svg)](https://github.com/JustAzul/cc-msgate/actions/workflows/ci.yml)
[![dist](https://github.com/JustAzul/cc-msgate/actions/workflows/dist.yml/badge.svg)](https://github.com/JustAzul/cc-msgate/actions/workflows/dist.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**`msgate` is the engine that lets Claude Code, hooks, scripts and other
plugins run agent CLIs.** It runs one-shot prompts and resumable conversations,
in the foreground or as background jobs, records every run and what it cost in
an append-only telemetry stream that takes new attributes without a code
change, answers the agent rows that other plugins back with runs, and serves
runs and model-call recording to those plugins through one interface,
`$.msgate`.

Codex is the first provider. The provider interface is built so Claude Code
can follow without changing the commands, the run layout or the telemetry.

The repository is `cc-msgate`; the Claude Code plugin, the command line, the
interface and the state directory are all called `msgate`.

## What it is and what it is not

`msgate` is:
- a command line, `msgate`, that runs agent CLIs and reads back their runs,
  telemetry, cost and ETA;
- a Claude Code plugin whose mod adds two tools (`run` and `jobs`), finish and
  spawn notices, a band and a status line, and the `/msgate-jobs` command;
- the answerer of every agent row that a plugin backs with a run (see
  [Rows the engine answers](#rows-the-engine-answers));
- an interface for other plugins: model-call recording, runs and jobs (see
  [For plugin authors](#for-plugin-authors-the-msgate-interface)).

`msgate` is not a set of ready-made agents. It ships no agent type, no profile
(model, effort and sandbox for a kind of work) and no skill that teaches Claude
when to ask another agent. Those are the plugin
[`msgate-agents`](https://github.com/JustAzul/msgate-agents), which uses this
engine and which you can leave out. The engine knows nothing of the plugins that
use it: a run's purpose is a label it records, and no rule of the engine depends
on one.

It ships as a Claude Code plugin with prebuilt static binaries for
linux/darwin × amd64/arm64. Installing it needs nothing besides the provider
CLI itself.

## Contents

- [Install](#install)
- [Moving from agentcli](#moving-from-agentcli)
- [Quick start](#quick-start)
- [Conversations and jobs](#conversations-and-jobs)
- [Purposes and settings](#purposes-and-settings)
- [Telemetry](#telemetry)
- [Cost](#cost)
- [Inside Claude Code](#inside-claude-code)
- [Rows the engine answers](#rows-the-engine-answers)
- [For plugin authors: the msgate interface](#for-plugin-authors-the-msgate-interface)
- [Using it from hooks and scripts](#using-it-from-hooks-and-scripts)
- [Retention](#retention)
- [Environment variables](#environment-variables)
- [Exit codes](#exit-codes)
- [Safety](#safety)
- [How it is built](#how-it-is-built)
- [History](#history)

## Install

```sh
claude plugin marketplace add JustAzul/cc-msgate
claude plugin install msgate@cc-msgate
```

The plugin puts `msgate` on the PATH of Claude's Bash tool. Its `SessionStart`
hook also keeps a small launcher at `~/.local/bin/msgate`, so your own settings
hooks and shell scripts can call it too. The launcher only ever points at the
installed plugin. It never replaces a file it did not write, and
`MSGATE_NO_LINK=1` turns it off. With no `msgate@cc-msgate` install on the
machine, the launcher says so and exits 127. The same hook prunes old run
directories once a day (see [Retention](#retention)).

Restart Claude Code after installing. For the ready-made agents, also install
[`msgate-agents`](https://github.com/JustAzul/msgate-agents).

Requirements:
- the provider CLI on `PATH` and logged in (`codex` for the Codex provider);
- Linux or macOS on amd64 or arm64.

### Update

```sh
claude plugin marketplace update cc-msgate
claude plugin update msgate@cc-msgate
```

Restart Claude Code to load the new version. The launcher follows it at the
next session start.

Versions follow [Semantic Versioning](https://semver.org), and the public API
is the documented contract: commands, flags, exit codes, JSON outputs,
persisted formats, the mod's tools and the `msgate` interface. Each version has
a [GitHub release](https://github.com/JustAzul/cc-msgate/releases) whose notes
list the commits since the previous one. Its tag, `v<version>`, names that
version's build, which stays there after `dist` moves on.

### Uninstall

```sh
claude plugin uninstall msgate@cc-msgate
claude plugin marketplace remove cc-msgate
rm ~/.local/bin/msgate
```

Runs and telemetry stay in `~/.local/state/msgate` (or `$MSGATE_HOME`, or
`$XDG_STATE_HOME/msgate`) until you delete it.

### Build from source

Everything builds in a pinned Go container, so the host needs only Docker (see
[How it is built](#how-it-is-built)). `make dist` writes the complete plugin,
binaries included, to `dist/`; `claude --plugin-dir dist` loads it for one
session.

## Moving from agentcli

`msgate` continues the project that was called `agentcli`. It ships with only
the new names and no aliases: there is no `agentcli` binary or launcher, no
`AGENTCLI_*` variable is read, `--scenario` is an unknown flag (exit 2), and
the interface is `$.msgate`, not `$.agentcli`. Anything of yours that called
the old names, such as hooks, scripts or other plugins, has to move to the new
ones in the same step.

| agentcli | msgate |
|---|---|
| `agentcli` command and `~/.local/bin/agentcli` | `msgate` and `~/.local/bin/msgate` |
| `AGENTCLI_*` variables | `MSGATE_*` variables |
| `--scenario` | `--purpose` |
| `~/.local/state/agentcli` | `~/.local/state/msgate` |
| tools `mcp__agentcli__ask`, `send`, `jobs` | tools `mcp__msgate__run`, `mcp__msgate__jobs` |
| command `/agentcli-jobs` | command `/msgate-jobs` |
| interface `$.agentcli` and refusals `agentcli:…` | interface `$.msgate` and refusals `msgate:…` |
| agent types `agentcli:<type>` and skill `agentcli:dispatch` | [`msgate-agents`](https://github.com/JustAzul/msgate-agents): `msgate-agents:<type>` and `msgate-agents:dispatch` |

To move, let your running jobs finish, remove the old plugin and install the
new ones. Do not keep both enabled.

```sh
claude plugin uninstall agentcli@agentcli
claude plugin marketplace remove agentcli
rm ~/.local/bin/agentcli
claude plugin marketplace add JustAzul/cc-msgate
claude plugin install msgate@cc-msgate
```

### Your runs and telemetry move once

Every `msgate` command resolves the home directory first. When `MSGATE_HOME`
is unset, the new home does not exist and the old `agentcli` home of the same
kind does (`$XDG_STATE_HOME/agentcli` when `XDG_STATE_HOME` is set, else
`~/.local/state/agentcli`), the command moves the old directory to the new
path with a single rename. Under a lock file in the parent directory, it first
marks as `lost` any run whose worker is gone. If a run is still running or
queued after that, the command exits 1 naming the run and moves nothing, so no
live worker writes into a moved directory: wait for it to finish and run the
command again. If the rename itself fails, the command
exits 1 naming both paths and changes nothing. No link is left at the old path.
Once the new home exists, the old one is never looked at again.

`AGENTCLI_HOME` is not read, so a home you placed with it is not moved: set
`MSGATE_HOME` to that directory instead.

Telemetry written under the old name is read in full: a record's `scenario` is
read as its `purpose`, and the same goes for an old conversation's defaults and
an old run's `request.json`.

### Claude Code state starts empty

The plugin's own Claude Code state starts empty under the new name:
- the `summaries` option returns to its default, on;
- an agent row of the old plugin cannot be continued;
- finish notices the old plugin had not delivered are not carried over; their
  results stay readable with `msgate result <run_id>`.

## Quick start

```sh
# A question to another agent: read-only unless you pass --sandbox
msgate exec --purpose second-opinion --model gpt-6.1-sol --effort high \
  "Is a TTL cache safe for this lookup?"

# Review the working tree, a branch diff or one commit
msgate review --uncommitted
msgate review --base main
msgate review --commit 3f2c1ab

# Long prompts: from a file or from standard input
msgate exec --purpose cross-check --prompt-file brief.md
git diff | msgate exec --purpose code-review -
```

A foreground run prints the path of the output file on its last stdout line.
With `--json` it prints one object instead:

```json
{"sdk_status": "ok", "exit_code": 0, "provider_exit": 0, "state": "done", "outcome": "ok",
 "run_id": "r-…", "conversation_id": "c-…", "run_dir": "…/runs/r-…", "output_path": "…/runs/r-…/output.md"}
```

`--dry-run` shows exactly what would run, without running it:

```sh
msgate exec --purpose second-opinion --model gpt-6.1-sol --effort high \
  --cwd /work/demo/repo --dry-run "Is a TTL cache safe here?"
```

```json
{
  "command": "exec",
  "provider": "codex",
  "argv": ["codex", "exec", "-C", "/work/demo/repo", "-s", "read-only", "-m", "gpt-6.1-sol",
           "-c", "model_reasoning_effort=high", "--json", "-o", "/work/demo/state/runs/r-…/output.md", "-"],
  "stdin": "prompt",
  "cwd": "/work/demo/repo"
}
```

The commands:

| Command | Does |
|---|---|
| `exec` | starts a conversation and runs its first turn |
| `review` | runs a provider review of a branch diff, the working tree or a commit |
| `send` | runs the next turn of a conversation |
| `status` | shows one run, or the session's active and recent runs |
| `wait` | blocks until a run finishes, then prints it like a foreground run |
| `result` | prints a finished run's output; `--report` prints a delivery report |
| `cancel` | asks a run to stop |
| `conversations` | lists conversations with provider, turns, status and last activity |
| `progress` | prints what a run's provider has done so far |
| `runs` | prints the folded telemetry runs as JSONL |
| `annotate` | attaches attributes to a finished run's telemetry |
| `stats` | summarizes telemetry: volume, durations, usage and cost, and runs by purpose and origin |
| `eta` | estimates how long a run of a purpose takes |
| `prices` | shows the price cache; `prices refresh` updates it |
| `usage add` | records the token usage of a model call |
| `prune` | removes the run directories of old finished runs |
| `link` | puts the `msgate` launcher on the PATH (`~/.local/bin`) |
| `version` | prints version, source commit, build sequence and platform |

## Conversations and jobs

Every run is a turn of a conversation. `exec` starts one, `send` continues it,
and both take `--background`:

```sh
# Start a job; the last stdout line is its run id
run=$(msgate exec --purpose delegation --sandbox workspace-write --background --prompt-file task.md | tail -n 1)

msgate status "$run"          # queued, running, done, failed, cancelled, timeout or lost
msgate wait "$run" --timeout 600
msgate result "$run"          # the output

# Follow up in the same provider session, by conversation id or by any of its run ids
msgate send "$run" "Now add tests for the edge cases you listed."

msgate cancel "$run"
msgate conversations
```

How jobs behave:
- A job runs in a detached worker that outlives the Claude Code session that
  started it. There is no daemon.
- A conversation runs one turn at a time. A second turn while one is active
  exits 3.
- A worker that dies is found and marked `lost` by the next command that looks
  at its run.
- `--timeout N` stops the provider's whole process group and exits 124.

Every run keeps its files under the msgate home: `$MSGATE_HOME`, else
`$XDG_STATE_HOME/msgate`, else `~/.local/state/msgate`. Each run directory
holds:
- `prompt.md`, `request.json` and `state.json`;
- `output.md`;
- `stderr.tail`, the last 64 KiB of provider stderr;
- `progress.jsonl`, what the provider did while it worked (`msgate progress
  <run_id>` prints it);
- for a job, `worker.log`.

## Purposes and settings

`--purpose <label>` says what a run is for. It is a label of your choosing:
the engine records it and groups `stats` and `eta` by it, and it never selects
a model, effort or sandbox. Without it the purpose is `adhoc`. `exec`,
`review`, `send` and `eta` take it; on `eta` it is required.

A run's settings are:
- **model and effort:** the flag's value, else the provider's default;
- **sandbox:** the flag's value, else `read-only`. A run that writes needs
  `--sandbox workspace-write`.

Each run records where every value came from:

| Source | Meaning |
|---|---|
| `flag` | passed on the command line, or the effort passed with `--effort-source flag` |
| `profile` | passed on the command line by a caller applying a profile, with `--settings-source profile` |
| `provider-default` | not passed; the provider chose |
| `default` | the sandbox, not passed, so the engine's read-only default applied |

`--settings-source <flag|profile>` (default `flag`) is for a caller that
resolves its own profile and hands the values over as flags, such as an agent
type of `msgate-agents`: the model, effort and sandbox passed as flags are then
recorded with source `profile`. `--effort-source <flag|profile>` sets the source
of the effort alone, so a caller can hand over a profile's model and sandbox
together with an effort someone asked for explicitly: `--settings-source profile
--effort-source flag` records the model and sandbox as `profile` and the effort
as `flag`. Without `--effort` it changes nothing. `send` reuses the defaults of
its conversation and their sources; a conversation recorded before the sandbox
default existed, with no sandbox, continues read-only with source `default`.

## Telemetry

Each run appends one record to `telemetry/YYYY-MM.jsonl` under the home, one
file per UTC month. Files are never rewritten.

A record carries:
- run and conversation ids, turn, provider and provider version;
- command, purpose, model/effort/sandbox and their sources;
- `model_used` and `effort_used`, what the provider reports it actually ran
  with (for Codex, read from its own session file), so a run on the provider's
  default model records that model too;
- source, session id, cwd;
- exit code, outcome, duration, timeout;
- output path and size, error excerpt, token usage;
- an `attrs` object for anything else.

Records, `status --json`, `eta --json` and the `runs` export name the field
`purpose`. A record written under the old name, with `scenario`, is read as
`purpose` by every reader.

A model call made outside a run, such as one of the mod's progress summaries,
appends a `model_call` record instead: provider, model, source, session id,
the run it was about and its token usage. `msgate usage add` writes it;
runs and their counts never include it.

`usage add` also takes `--call-id <id>`, the call's own id (a call recorded
twice under one id counts once), `--attr <key>=<value>`, repeatable, for an
`attrs` object on the record (keys of `a-z` and `_`, at most 32 attrs, values up
to 200 characters), and `--ts <RFC 3339>`, when the call was made, which also
picks the month file the record goes in. Without them the id is generated, the
record has no `attrs` and the time is now. A reader that predates `attrs` reads
the record and ignores them.

Attributes are how consumers add data without a schema change:

```sh
# At dispatch time
msgate review --base main --attr pr=128 --attr-json 'labels=["api","auth"]'

# After the fact, for example findings parsed from the output
msgate annotate "$run" --attr-json 'review.findings={"high":1,"medium":3}'
```

Annotations are folded into their run, later values winning per key, by every
reader:

```sh
msgate runs --days 30 | jq 'select(.attrs["review.findings"].high > 0)'
msgate stats --days 7      # volume, durations, usage, and runs by purpose and origin
msgate stats --all --json  # usage_totals, and usage_by_model per model
msgate stats --session-id "$CLAUDE_CODE_SESSION_ID"   # one Claude Code session's runs and tokens
```

`stats` reports runs by purpose and by origin. A run's origin is `plugin` when
its source is one the engine's own plugin writes (`mod`, `agent`, or one
starting with `plugin:`) and `cli` for every other source: hooks, skills,
scripts and people alike. The engine selects by no other label. In
`stats --json`, `by_purpose` is an object keyed by purpose in name order; each
value holds a `cli` and a `plugin` object, present only when that origin has
runs of the purpose, each `{ runs, outcomes, findings }`:
- `outcomes` counts each outcome, `no-outcome` when a run has none;
- `findings` sums, over the runs carrying a `review.findings` annotation, the
  keys `total`, `critical`, `high`, `medium`, `low` and `violations`, and adds
  `annotated` (those runs), `with_findings` (those whose `total` is above 0)
  and `clean` (those whose `total` is 0). It is `{ annotated: 0 }` when no run
  carries the annotation.

The text form prints the same counts per purpose and origin. The sections
agentcli selected by hook and code-review runs (`reliability`, `outcomes`,
`findings` and `review_findings_proxy`) are gone from the JSON; every other key
is unchanged.

`eta` estimates how long a run of a purpose takes, from the whole history:

```sh
msgate eta --purpose code-review                 # ETA ~2m 36s (repo average, 12 runs)
msgate eta --purpose code-review --cwd ../api --json
```

It averages the durations of the purpose's runs, whatever their outcome, in
the git repository of `--cwd` (default: the current directory), its worktrees
and subdirectories included. With none there, it takes the mean of each
repository's mean, so every repository weighs the same
(`ETA ~2m 23s (global average, 8 repos)`); with no run of the purpose at all,
it prints `ETA unknown (no history)` and exits 0. `--json` prints one object
with eight keys: `sdk_status`, `exit_code`, `purpose`, `repo` (the repository
key of `--cwd`), `basis` (`repo`, `global` or `none`), `eta_ms` (null with no
history), `samples` and `repos`.

## Cost

`stats` also reports what the runs would cost at the provider's API list
prices, in US dollars, whatever the sign-in (a subscription included). Prices
come from the public [LiteLLM price list](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json),
cached locally for the models your providers offer (`codex debug models`) and
the ones your runs used, nothing else.

```sh
msgate prices refresh --json     # fetch the prices the cache needs (conditional, ETag)
msgate prices refresh --max-age 24h   # only when the cache is older than a day
msgate prices                    # what is cached, from where and when
msgate stats --session-id "$CLAUDE_CODE_SESSION_ID" --all --json | jq .usage_totals.cost_usd
msgate stats --session-id "$CLAUDE_CODE_SESSION_ID" --all --json | jq .usage_by_provider
```

- A run's cost counts its input in three parts: uncached input at the input
  price, cached input at the cache-read price, and tokens written to the
  prompt cache at the cache-write price. Reasoning tokens are part of output.
- Codex reports no cache writes under a ChatGPT sign-in, so when a Codex run
  reports none, every uncached input token is counted as written, as implicit
  prompt caching does. Anthropic reports the writes it made, so a model call
  is priced exactly as reported.
- `usage_totals` is what the runs cost. `usage_by_provider` splits it per
  provider and adds the model calls (`anthropic`), each with its own
  `cost_usd`. `model_calls_by_source` counts the model calls of each source
  (`mod-summary`, `mod:<plugin>`) with their tokens and `cost_usd`; a call id
  recorded twice counts once.
- Costs are exact (no floating point) and rounded once: 6 decimals in JSON,
  cents in text.
- A model the price list does not price (`gpt-reserve`, `codex-auto-review`)
  makes the total a lower bound: `cost_complete` is false and the status line
  shows `≥`.
- Only short-context standard-tier prices are used: no Fast mode, no Batch, no
  long-context rate.
- `MSGATE_PRICES_URL` points the refresh at another copy of the list;
  `MSGATE_PRICES_URL=off` stops it from going to the network at all.

## Inside Claude Code

The plugin adds a mod. It has no skill and no agent type of its own; for those,
install [`msgate-agents`](https://github.com/JustAzul/msgate-agents).

The mod adds two tools that start and read background jobs, for work Claude
wants to run directly:

| Tool | Does |
|---|---|
| `mcp__msgate__run` | starts a job: `prompt` (required), and optionally `conversation_id`, `purpose`, `provider`, `model`, `effort`, `sandbox`, `cwd` |
| `mcp__msgate__jobs` | `action` is `list`, `status`, `result` or `cancel`; `run_id` names the run for the last three |

`run` without a `conversation_id` starts a conversation (`exec`); with one it
continues that conversation (`send`). It returns at once with
`{conversation_id, run_id, continued}`, `continued` being true when it continued
the conversation it was given. Its runs carry the source `mod`. `jobs` with
`result` returns the output cut at 64 KiB.

### Finish notices

Every job started through `run` gets an entry in a **delivery queue** before
the tool returns. The queue is persisted in the plugin store, one key per
Claude Code session, so two sessions never write the same key, and it survives
a reload of the engine.

A timer reads each pending entry's run by id every 15 seconds, so no limit on
how many runs the session has started applies. For a run that has finished, the
mod shows a toast and submits a notice to Claude:

```
msgate job r-… (second-opinion, conversation c-…) finished: ok
```

followed by the output inline when it is at most 8 KiB, otherwise its first
8 KiB and the output path. The entry is removed only when Claude Code accepts
the submit; a notice it refuses, or a submit that throws, is tried again on the
next tick. A run that has been pruned has nothing to report: its entry is
removed. At most 50 pending entries are read per tick, oldest first.

Delivery is at least once. If the engine stops after submitting a notice and
before the entry is removed, the notice is delivered again after the next load
with ` (possibly repeated)` after the outcome. Queues of other sessions whose
newest entry is more than 30 days old are removed at session start.

Runs a plugin starts through the [interface](#for-plugin-authors-the-msgate-interface)
get no entry and no notice: that plugin reads them with `jobs`.

### The band, the spinner and the status line

A run any other caller starts with `--agent-feedback` (see
[below](#using-it-from-hooks-and-scripts)), such as a hook's review, is a
**flagged run**. While it works it shows in a band above the prompt: one line
that starts `msgate · ` with its purpose, its source, how long it has run, what
it has cost so far and a short summary of what it is doing, gone once it ends.
The band shows the flagged runs whose source is not `agent`, since the rows the
engine answers show their own runs, and not those that have a
[hidden row](#hidden-rows). Only you see the band: nothing about the run
enters the conversation, and the caller that started it delivers its result.

The summaries come from `claude-haiku-5-5` at low effort: the mod sends a
run's newest steps, each cut to 300 characters, to the Anthropic API through
the session's own sign-in. There are two kinds, each with its own interval, and
the plugin's `liveSummaryEvery` and `recordedSummaryEvery` options set them:
- **Live summary.** A label of three to five words (`» Reading workerlog.go`),
  asked on the run's first step and then every three (`liveSummaryEvery`, from
  1 to 10). It is shown while the run works and not kept.
- **Recorded summary.** One or two sentences about a window of steps
  (`» 1–8: Read the worker and ran its tests.`), asked once eight steps have
  gathered (`recordedSummaryEvery`, from 1 to 24; `0` asks none), with every
  step not yet recorded in its window, and once more, at the end of the run,
  for the steps left over (waiting at most 15 seconds). The sentences are stored in the history of the row that follows the run, an agent
  row or a [hidden row](#hidden-rows), where the live labels are lost. A
  sentence whose request fails is missing, and the next one covers what was
  left. Nothing of them is in the report that returns to Claude.

Both are skipped when nothing is on screen (`claude -p`). The plugin's
`summaries` option turns them off, and so does the environment variable
`MSGATE_SUMMARIES=0`; `MSGATE_SUMMARIES=1` turns them on where there is a
screen. The variable wins over the option when it is set, and any other value
leaves the option in force. With summaries off the mod makes no summary request and adds nothing of
its own to a row: it shows only what the provider emits (commands, messages,
and reasoning that arrives as text), with no `msgate · <type>` line and no
elapsed-time line, and nothing for reasoning the provider encrypts. While
summaries are on, no raw step shows in a row or the band: a summary that fails
leaves the last one in place, and before a run's first summary its band line
ends after the cost.

While the turn of a row the engine answers runs, the mod sets the row's
spinner message to the newest summary, or to `msgate · <type>` until there is
one, unless Claude Code has already set a message of its own there.

Whenever a run of the session starts, whoever started it (an agent row, the
tools, a hook, the CLI), the mod shows a toast and writes one line in the
transcript, once per run:

```
msgate · spawned code-review · trigger my-hook · ETA ~2m 36s (repo average, 12 runs)
```

The ETA comes from `msgate eta` (see [Telemetry](#telemetry)): the mean
duration of the same purpose's runs in the session's repository, else the mean
of the per-repository means (`ETA ~2m 23s (global average, 8 repos)`), else
`ETA unknown (no history)`. `ETA unavailable` means `eta` failed, gave no
answer within 10 seconds, or printed something the mod could not read. The mod
looks for runs started outside it every 5 seconds, in the session's 20 newest
runs, so when more than 20 runs start between two looks, the oldest of those it
did not start itself get no notice. A run that has already ended when it is
found is announced as `finished in 3s`, with no ETA. In the first look after
the mod loads, though, a run that ended before the second the mod loaded in is
not announced. The notices for the rows and the tools never hold up the tool
result or the agent's turn.

The status line counts the running jobs the mod started and shows what the
session's runs cost, hook runs included, and what the mod's own model calls
cost (`💸 1 job running · Codex $0.68 | Claude $0.01`; `≥` when part of it has
no price). Both are estimates at API list prices. The mod refreshes the price
cache when the session starts (at most once a day) and whenever the
conversation is compacted. `/msgate-jobs` lists the session's jobs at once,
even while Claude is working.

## Rows the engine answers

A plugin can register an agent type with Claude Code and ask `msgate` to answer
it with runs, by **backing** the type through the interface (see
[`backType`](#for-plugin-authors-the-msgate-interface)). The engine then answers every model request of an
agent of that type in the session, whoever spawned it, usually Claude. No Claude
model runs inside such an agent; a dispatch that fails says so in its answer
instead of falling back to a Claude reply. Every other agent passes through
untouched. This is how `msgate-agents` offers its six agents.

How a backed row behaves:
- **Prompt.** The prompt of a turn is the newest user message of the agent's
  conversation, system reminders removed; when there is none, the text the turn
  started with. A prompt that is empty ends the turn with a hand-off failure and
  starts nothing.
- **First turn.** With no conversation recorded for the agent, the engine
  starts one with the data the type was backed with: `exec` with the prompt
  as the question, or `review` with the prompt as the whole target, exactly one
  of `uncommitted`, `base <branch>` or `commit <sha>`; anything else is refused
  naming the three forms. The run carries the type's name as its purpose, the
  source `agent` and the settings source `profile`.
- **Effort.** An `effort` given in the Agent call that spawned the agent
  replaces the type's: the first turn runs with it, recorded with
  `--effort-source flag`, and the later turns keep it through the conversation.
  The value reaches the provider as given; Codex takes `minimal`, `low`,
  `medium`, `high` and `xhigh`, so a `max` ends as the run's failure. Without an
  effort in the call, the type's effort applies, then the provider's default;
  the session's own effort setting never applies. A fork, a resumed agent, a
  teammate and a workflow's agent take no effort from the call.
- **Later turns.** A SendMessage to the agent continues the same conversation.
  An agent whose earlier answer is saved but whose conversation is not recorded
  answers that it cannot be resumed.
- **Progress.** The row shows what the run is doing as a short summary every
  few steps (`» Reading workerlog.go`), with an elapsed-time line when it is
  quiet, and stores a sentence about every eight steps in the agent's history;
  with summaries off, only what the provider emits.
- **Answer.** The report is the output cut at 64 KiB, led by how the run ended
  when it did not deliver, and closed by a trailer:
  `[msgate run <run_id> · conversation <conversation_id> · <outcome>]`. It is
  handed back through Claude Code's `SubagentHandback` in the auto permission
  mode, where only a hand-back reaches Claude, and as the agent's final text in
  the other modes.
- **Failure.** Every failure answers
  `msgate: the hand-off to the agent failed, so this is not its answer.`
- **Stop.** Stopping the agent (Esc, TaskStop) cancels its run.
- **Step limit.** The engine answers at most eight steps of a turn. From the
  ninth on it passes the step down, so the fallback model the agent type
  registered ends the turn, and it writes one line to the debug log.

### Hidden rows

A plugin can show a flagged run as a row of its own. It backs a type with
`command: 'follow'` and `promptMode: 'run-id'`, lists the runs to show with
[`flaggedRuns()`](#flaggedruns) and spawns one row per run, with the prompt
`msgate run <run_id>`. The engine answers such a row only when the plugin that
backed its type spawned it, and only for a run of the session that asks to be
shown; any other row of the type passes to the model beneath.
- **Registered.** The engine registers a row when it opens, with the run its
  prompt names. The run leaves the band then, and `flaggedRuns()` no longer
  lists it, whatever becomes of the row.
- **Follow.** The row shows what the run is doing, as a backed row does, and
  stores the same recorded summaries in its history. It starts no run: the hook
  that started the run delivers its result.
- **Answer.** The turn ends with the plain text
  `msgate run <run_id> ended <outcome>`, never a hand-back, and a later step of
  the turn repeats it. Nothing of it reaches Claude.
- **Stop.** Stopping the row hides it and does not cancel the run, and the band
  does not show that run again.
- **Auto permission mode.** There Claude Code does not let a row end with plain
  text, and a hand-back would deliver the row's text to Claude, so the spawn is
  refused (`msgate: a hidden row does not open in the auto permission mode`)
  and the run stays in the band.

## For plugin authors: the msgate interface

The mod adds the interface `msgate` to Claude Code, so another plugin's mod
reaches the engine as `$.msgate`, with no binary to run and no agent type to
install. It is an object of six functions:
`{ complete, backType, version, run, jobs, flaggedRuns }`. The engine knows its caller from
the plugin name Claude Code stamps on the call, never from an argument. A
refusal reaches the caller as an error whose message starts
`msgate:<method>:`. Where `msgate` is not installed, `$.msgate` is undefined
and calling it throws; a plugin that can do without the engine checks for that.

**Versioning.** The interface is versioned by the engine's own Semantic
Versioning release: a change that breaks any method's contract bumps the major
version and a new method bumps the minor. Call `version()` to find out which
you have, and check the major.

### `version()`

Resolves to the engine's release version string, for example `"1.0.0"`.

### `complete({ request, meta })`

Makes a model completion for the caller and records it, so its cost shows in
`stats` next to the runs.
- `request` is a `$.model.complete` request, passed through unchanged (cache
  marks and timeout included). The answer is that call's reply, unchanged.
- `meta` is `{ contract, callId, provider?, attrs? }`. `contract` must be `1`.
  `callId` is the caller's own id for the call (letters, digits and `:._-`,
  starting with a letter or digit, at most 200 characters). `provider`, when
  given, must be `anthropic`. `attrs` is an object of strings; the engine keeps
  at most 16 whose key is 1 to 32 characters of `a-z` and `_` and whose value
  has at most 200 characters, and drops the rest without refusing the call.
- The model is called once per call: no retry, and a `callId` already being
  served is refused.
- After a billed reply the call is recorded without being awaited, as a
  best-effort model-call record with the source `mod:<calling plugin>`, the
  caller's `callId` and attrs.
- Refusals are `msgate:pre-model:<why>` when the model was not called and can
  no longer be (`contract`, `call-id`, `unsupported-provider`, `request`,
  `duplicate`, `cancelled`, `abandoned-<kind>`, `re-entry`), and
  `msgate:after-model:<why>` when it may have been (`throw`, `timeout`,
  `refused`). A call the engine holds no state for is refused with
  `msgate:unknown-state:<kind>`, which a caller should read as ambiguous. A call
  that reaches the interface's own body because no hook answered it throws
  `msgate:unserved`.

### `backType({ type, command, promptMode, model?, effort?, sandbox? })`

Marks the caller's agent type `<caller>:<type>` as backed, so the engine answers
its rows (see [Rows the engine answers](#rows-the-engine-answers)). It resolves
`{ type: "<caller>:<type>" }`. A plugin can back only its own types, since the
full type takes the caller's name.
- `type` matches `^[a-z0-9][a-z0-9-]{0,63}$`.
- `command` is `exec`, `review` or `follow`.
- `promptMode` is `free`, `review-target` or `run-id`; `exec` requires `free`,
  `review` requires `review-target` and `follow` requires `run-id`. A `follow`
  type names a run in its prompt and the engine follows it without starting
  one; it needs no `model`, `effort` or `sandbox`.
- `model`, `effort` and `sandbox` are optional strings, and `sandbox` is
  `read-only` or `workspace-write`. These are the type's profile, handed over as
  data: the engine passes them to the run as flags with the settings source
  `profile`. An `effort` in the Agent call that spawns a row replaces the
  profile's for that row.
- Anything else is refused with `msgate:backType:<field>`, naming the field.
- The backed types live in the engine's session state, keyed by full type, so an
  engine reload keeps them. A later call for the same type replaces its data.
  A plugin calls `backType` at every session start.

### `run({ prompt, conversation_id?, purpose?, provider?, model?, effort?, sandbox?, cwd? })`

Admits a job like the `run` tool and resolves
`{ conversation_id, run_id, continued }`. The run's source is
`plugin:<caller>`. The engine does not deliver its result to Claude and adds no
delivery-queue entry: the caller reads the result with `jobs`.

### `jobs({ action, run_id? })`

`action` is `list`, `status`, `result` or `cancel`, as for the `jobs` tool.
A plugin sees only its own runs: `list` returns the session's runs whose source
is `plugin:<caller>`, and `status`, `result` and `cancel` refuse a run whose
source is not, with `msgate:jobs:not-yours`. A plugin cannot read or cancel the
runs of Claude or of another plugin.

### `flaggedRuns()`

Resolves `[{ run_id, purpose, source, started_at }]`: the session's runs that
asked to be shown with `--agent-feedback`, are not finished, and whose source is
not `agent`, leaving out every run that already has a [hidden row](#hidden-rows). `purpose` is
`""` for a run with none, and `started_at` is `null` for a run that has not
started. Any plugin can call it. It is refused with `msgate:flaggedRuns:failed`.

## Using it from hooks and scripts

Settings hooks do not see the plugin's `bin/` directory, so call the launcher:

```sh
msgate="${MSGATE_BIN:-$HOME/.local/bin/msgate}"
out=$("$msgate" exec --purpose code-review --source my-review-hook \
        --model gpt-6.1-sol --effort high --sandbox read-only --timeout 300 \
        --clean-sentinel "No material findings." --material-label findings \
        --cwd "$repo" --prompt-file "$prompt" | tail -n 1)
```

(`MSGATE_BIN` is a convention of your own script, not a variable `msgate`
reads.) A run passes its settings as flags because a purpose carries none: say
the model, effort and sandbox every time, and add `--settings-source profile`
when the values come from a profile you keep, with `--effort-source flag` when
the effort alone does not.

Useful flags:
- `--agent-feedback` shows the run in the band above the prompt of its Claude
  Code session while it works; `MSGATE_AGENT_FEEDBACK=1` does the same for a
  caller that cannot change its arguments. It needs a session id; without one
  the run goes ahead unflagged and stderr says why.
- `--source` labels who dispatched the run, for `stats`.
- `--session-id` ties the run to a Claude Code session. It defaults to
  `$CLAUDE_CODE_SESSION_ID`.
- `--clean-sentinel` and `--material-label` turn "nothing to report" into the
  outcome `clean`.
- `--run-id` lets a caller pick the id.

### A report to hand back

`msgate result <run_id> --report` prints a delivery report for a finished run,
ready to give to Claude. The argument can also be the output path. The report
holds:
- a first line, `## msgate report · <purpose> · <model> · <outcome>`, where the
  model is the one the run used, else the one requested, else the word
  `default`;
- when the run did not deliver, `ended <outcome>: <error excerpt>`;
- the output, with every `<` that opens a tag-shaped token written `&lt;`, so
  markup in the output cannot act as markup in Claude's context;
- the trailer `[msgate run <run_id> · conversation <conversation_id> · <outcome>]`
  and `Full output: <path>`.

A run that has not finished exits 5; an unknown run or path exits 4. A settings
hook that wants Claude to hear a result prints the report to stderr and exits 2
(with `asyncRewake: true` on its settings entry), whether the run found
something, found nothing or failed.

## Retention

Run directories hold prompts and outputs, so they are pruned; telemetry is
never pruned.

```sh
msgate prune --dry-run            # what would go
msgate prune --older-than 14      # remove finished runs that ended over 14 days ago
```

- A run is removed only when it is finished, ended more than N days ago
  (default 30, or `MSGATE_RETENTION_DAYS`), is not the active turn of a
  conversation, and nothing holds its lock.
- Conversations keep working after their old runs are pruned: `send` still
  resumes them. A pruned run id reads as not found (exit 4).
- The plugin runs `msgate prune --auto` at session start, at most once a day
  and for at most 3 seconds. `MSGATE_NO_PRUNE=1` turns it off.
- `status` reads a small per-session index, so its cost does not grow with the
  number of runs on disk.

## Environment variables

| Variable | Effect |
|---|---|
| `MSGATE_HOME` | the home directory; else `$XDG_STATE_HOME/msgate`, else `~/.local/state/msgate` |
| `MSGATE_AGENT_FEEDBACK` | `1` flags the run as `--agent-feedback` does |
| `MSGATE_NO_LINK` | `1` stops the plugin from writing the launcher |
| `MSGATE_NO_PRUNE` | `1` stops the daily automatic prune |
| `MSGATE_RETENTION_DAYS` | how old a finished run must be to be pruned (default 30) |
| `MSGATE_PRICES_URL` | another copy of the price list; `off` never goes to the network |
| `MSGATE_TEST_*` | switches for the test suites; not for use |

No `AGENTCLI_*` variable is read.

## Exit codes

`msgate` exits with the provider's own exit code, except:

| Code | Meaning |
|---|---|
| 1 | the home migration was refused or failed (see [Moving from agentcli](#moving-from-agentcli)) |
| 2 | usage error (bad flag, missing prompt, reserved native flag, unsupported capability) |
| 3 | conversation busy, or `result` on an unfinished run |
| 4 | run or conversation not found |
| 5 | `wait` reached its own `--timeout` (the run keeps going), or `result --report` on an unfinished run |
| 6 | conversation not resumable yet |
| 7 | `prices refresh` could not get the price list (the cache is kept) |
| 70 | internal error, or a job whose worker never started |
| 124 | the run hit `--timeout` |
| 125 | the run was lost (its worker died) |
| 127 | provider CLI not found, or the launcher found no installed `msgate` plugin |
| 130 | cancelled by `msgate cancel` |
| 128+n | stopped by signal n |

With `--json`, every exit path prints exactly one JSON object with
`sdk_status`, `exit_code` and `error`, so a provider's exit 2 is never confused
with a usage error.

## Safety

- `danger-full-access` is refused, whatever the purpose.
- A run writes only with `--sandbox workspace-write`; without `--sandbox` it
  runs read-only.
- Native flags that would override what msgate controls are refused with exit
  2. That covers the output file, JSON mode, working directory, model, sandbox,
  config profile, `--dangerously-*`, `--ephemeral`, and `-c` overrides of
  model, effort, profile or sandbox keys. Other native flags pass through after
  `--`.
- Prompts go to the provider on stdin and are stored only in the run directory
  (mode 0600, directories 0700). Telemetry never holds the prompt.
- A plugin using the interface sees only the runs it started.
- With summaries on, the mod sends a run's newest steps, cut short, to the
  Anthropic API (see [Inside Claude Code](#inside-claude-code)); the
  `summaries` option and `MSGATE_SUMMARIES=0` turn that off.

## How it is built

- Go, standard library plus `golang.org/x/sys`, built with `CGO_ENABLED=0` and
  `-trimpath`. Everything runs in a pinned Go container, so the host needs only
  Docker:

  ```sh
  make test        # go test ./...
  make vet         # go vet ./... (and make vet-darwin)
  make build       # build/msgate
  make dist        # the plugin tree with the four binaries and SHA256SUMS
  ```

- CI runs the tests on Linux and macOS. On every push to `main`, a separate
  workflow builds the plugin tree, scans it for personal data, and publishes it
  as a single commit on the `dist` branch. The marketplace manifest on `main`
  points at that branch. When `VERSION` names a version that has no release
  yet, the workflow also creates the GitHub release `v<version>`, tagged on
  that build, with notes listing the commits since the previous version.
- The plugin's mod is plain JavaScript, tested with `claude plugin test plugin`.
  `make verify-plugin` loads the plugin in a model-free `claude -p` and fails
  unless the `msgate` interface is mounted with its six methods; run it before
  every push that changes `plugin/` (see [`AGENTS.md`](AGENTS.md)).
- [`docs/split-PRD.md`](docs/split-PRD.md) and
  [`docs/split-test-cases.md`](docs/split-test-cases.md) hold the requirements
  and test cases of `msgate`. [`docs/PRD.md`](docs/PRD.md),
  [`docs/test-cases.md`](docs/test-cases.md) and [`docs/FRAME.md`](docs/FRAME.md)
  describe agentcli up to 0.6.3; the split requirements amend and supersede them.

## History

This repository continues the history of `agentcli`, which was called
`agent-cli-sdk` before that: every commit and tag up to `v0.6.3` is that
project's. `msgate` 1.0.0 is the first release under the new name, with the
ready-made agents split out into
[`msgate-agents`](https://github.com/JustAzul/msgate-agents).

## License

MIT. See [LICENSE](LICENSE).
