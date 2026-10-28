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
- a Claude Code plugin whose mod adds the tools `run` and `jobs` (and an
  internal `progress` marker the model never calls), finish and spawn notices, a
  band and a status line, and the `/msgate-jobs` command;
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
- [Provider health](#provider-health)
- [One-off completions](#one-off-completions)
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
next session start. An update installs only when a release changes `VERSION`:
a build pushed between two releases reaches no installed copy.

Engine 1.4.0 is the first version with the `models` method of the
[interface](#for-plugin-authors-the-msgate-interface) and with `purpose` and
`stepped` in `backType`. A plugin that registers agent types per model, or that
asks for rows that follow a run [in steps](#rows-the-engine-answers), needs
that version or a later one.

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
| `send` | runs the next turn of a conversation, or of a Codex thread msgate did not start |
| `status` | shows one run, or the session's active and recent runs |
| `wait` | blocks until a run finishes, then prints it like a foreground run |
| `result` | prints a finished run's output; `--report` prints a delivery report |
| `cancel` | asks a run to stop |
| `conversations` | lists conversations with provider, turns, status and last activity |
| `progress` | prints what a run's provider has done so far |
| `prompt` | prints the prompt a run was started with, byte for byte |
| `models` | prints the models a provider offers, as the provider lists them |
| `providers` | shows which providers can take work now; `providers refresh` checks them, `providers mark` marks one down |
| `complete` | answers one request from Codex, with no conversation and no run |
| `runs` | prints the folded telemetry runs as JSONL |
| `annotate` | attaches attributes to a finished run's telemetry |
| `stats` | summarizes telemetry: volume, durations, usage and cost, and runs by purpose and origin |
| `eta` | estimates how long a run of a purpose takes |
| `prices` | shows the price cache; `prices refresh` updates it |
| `usage add` | records the token usage of a model call |
| `prune` | removes the run directories of old finished runs |
| `link` | puts the `msgate` launcher on the PATH (`~/.local/bin`) |
| `version` | prints version, source commit, build sequence and platform |

`msgate prompt <run_id>` writes the run's `prompt.md` to standard output exactly
as stored: no added newline and no `--json` form. An unknown run exits 4, a
prompt that cannot be read exits 70, and a missing or extra argument exits 2.

`msgate models [--provider <name>] [--json]` asks the provider for its model
catalog (`codex debug models` for Codex, stopped after 10 seconds) and prints it
unfiltered, in the provider's order. Each line holds `id`, `name` and
`visibility` separated by tabs; with `--json` it prints
`{"provider":"codex","models":[{"id":"…","name":"…","visibility":"…"}]}`.
`--provider` defaults to `codex`, the only provider that ships, and any other
name exits 2. A provider CLI that is not on the `PATH` exits 127, and a catalog
that cannot be read exits 70.

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

### Continuing a Codex thread msgate did not start

`send` also takes the id of a Codex thread opened with `codex`, `codex exec`,
the TUI or another tool. It resolves the id in this order: a conversation id, a
run id, a conversation that already holds that provider session id, then the
provider's own sessions. Codex session ids are UUIDs. An id that names none of
them exits 4 with `no conversation, run or provider session "<id>"`.

```sh
msgate send "$thread_id" "Continue with the failing test."   # a Codex thread id
```

A thread no conversation holds is adopted: a new conversation holds it from
this turn on, and later sends continue that conversation like any other.
- **cwd, model and effort** come from the thread's last turn, the model and
  effort with source `provider-session`. With no turn recorded, the thread's
  own cwd and `provider-default` apply. A cwd that is missing or gone exits 2
  and asks for `--cwd`.
- **sandbox and purpose** are `read-only` (source `default`) and `adhoc`
  unless passed.
- **flags** override all of these and become the conversation's defaults.
  `--dry-run` plans the turn without adopting the thread.
- **two first sends** of one thread make one conversation; the other continues
  on it and exits 3 while the first runs. A send waits up to 5 seconds while a
  run of the same provider has not reported its thread yet, so it finds that
  run's conversation instead of making a second.

msgate guards only its own runs. It does not know whether another tool is
running the thread: not continuing a thread that is live elsewhere is up to the
caller.

A Codex multi-agent sub-agent cannot be resumed directly. `send <sub-agent id>`
runs a turn of the parent thread's conversation instead, and the parent is
adopted first when no conversation holds it. The prompt asks the parent to give
it to that agent with `followup_task`, wait, and reply with the agent's answer
as given. The flags configure the parent's turn: for that turn only when a
conversation already holds the parent, and as the conversation's defaults when
the send adopts it. `output.md` is the parent's final message. `request.json`
records `relay` (`provider_session_id`, `address`), and `prompt.md` keeps your
prompt, not the wrapped one. A relay has these tradeoffs:
- a sub-agent is reachable only between its parent's turns; while the parent
  runs, the send exits 3;
- one parent's sub-agents are reached one at a time, as turns of one
  conversation;
- it costs a full parent turn, and the recorded cost leaves out the
  sub-agent's tokens.

A sub-agent that cannot be relayed (its parent's rollout is missing, or its
parent is itself a sub-agent) and a review sub-thread are resumed as
themselves. If Codex refuses, its refusal is the run's error.

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
| `provider-session` | not passed; read from the last turn of a Codex thread msgate adopted |

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

### Harness levels

`--harness <full|lean|bare>` sets how much of the provider's own instructions,
skills and tools a run keeps. `exec`, `send` and `review` take it. Without it a
new conversation runs at `full`, which changes nothing: the provider runs as it
would on its own.

| Level | What a Codex run keeps |
|---|---|
| `full` | everything Codex adds on its own |
| `lean` | Codex's base instructions are replaced by one line (`Follow the user's instructions. Be concise.`), and the skills catalog, the collaboration-mode instructions, the apps and plugins notes and the multi-agent role are off. The shell and editing tools stay. |
| `bare` | `lean`, with the environment note, the shell tool, the other optional tools (browser, computer use, image viewing and generation, web search), hooks and every MCP server Codex would start for the run also off |

Measured on Codex 0.162.1, in an empty directory, with the prompt "Say OK", a
`lean` run sent 7,469 input tokens and a `bare` run 2,938.

```sh
msgate exec --purpose label --harness lean --prompt-file brief.md
msgate send "$run" --harness bare "Now answer in one word."
```

- **The level is a conversation default.** `exec` and `review` store it, and
  `send` runs at the stored level; a conversation with none runs at `full`.
  `--harness` on `send` applies to that turn only and does not change the
  stored level.
- **A review needs the provider's tools.** `review --harness bare` exits 2;
  `lean` runs.
- **A level the provider does not run exits 2** as an unsupported capability.
  Codex runs all three.
- **Native flags cannot undo a level.** At `lean` and `bare`, a `-c` override
  of a key the level sets, or a `--enable` or `--disable` of a feature it sets,
  passed after `--` is refused with exit 2, like the other reserved flags. At
  `full` the level sets nothing, so nothing is refused for it.

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

A resumed turn (`send`) records its own usage. Codex reports the thread's
running total, and msgate takes off the total from before the run. A record
written before this stays as it was.

Records, `status --json`, `eta --json` and the `runs` export name the field
`purpose`. A record written under the old name, with `scenario`, is read as
`purpose` by every reader.

A model call made outside a run, such as one of the mod's progress summaries,
appends a `model_call` record instead: provider, model, source, session id,
the run it was about and its token usage. `msgate usage add` writes it;
runs and their counts never include it. `msgate complete` writes one for its
own call (see [One-off completions](#one-off-completions)).

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
  provider and adds the model calls under the provider that answered them
  (`anthropic`, or `codex` for a call made with `msgate complete`), each with its
  own `cost_usd`. `model_calls_by_source` counts the model calls of each source
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

## Provider health

`msgate providers` says whether each provider can take work now. It knows
`codex` and `anthropic` (Claude). It answers at once from a cache and never
waits for a check:

```sh
msgate providers
msgate providers --json
```

```
anthropic: available, check 2026-10-23T14:02:11Z
codex: unavailable (rate-limited) until 2026-10-23T18:00:00Z, check 2026-10-23T14:02:11Z
summary codex/gpt-6-luna: $0.000412 per call, estimated
```

With `--json` it prints one object:
- `providers` holds, per provider, `provider`, `available`, `reason`,
  `checked_at`, `resets_at`, `source` and, when a check could not run, `skipped`.
  `source` is `check`, `mark` or `none`. A provider with no cached entry reads as
  available, with `source` `none` and `checked_at` null.
- `summary_models` is the ranking described [below](#the-summary-ranking).

The reasons a provider is unavailable are `not-installed`, `not-logged-in`,
`rate-limited`, `quota-exhausted` and `provider-outage`. An available provider
carries the reason `unknown` when none of its checks could run.

### The cache

The cache is `providers.json` in the msgate home. An entry goes stale after 5
minutes when the provider is available and after 1 minute when it is not. Any
stale or missing entry starts one detached `msgate providers refresh`, whose
report goes to a log in the home; a mark with a `resets_at` in the future stays
fresh until then.

`msgate providers refresh` checks every provider at once, writes the cache and
recomputes the summary ranking. It prints `providers: refreshed (<n> checked)`.
When another refresh is running it prints `providers: another refresh is running`
and exits 0. A check never replaces a mark whose `resets_at` is still in the
future.

What a check looks at:
- **Codex:** the binary on the `PATH` (`not-installed`); `codex login status`
  (`not-logged-in`); the account's rate-limit windows, read through the Codex app
  server (`rate-limited`, with the time the used-up windows reset); and OpenAI's
  status page (`provider-outage`, on a major outage of a component that serves
  Codex). The first layer that fails decides. A layer that could not run is passed
  over and named in `skipped`; when none could run, Codex is available with the
  reason `unknown`.
- **Claude:** Anthropic's status page only (`provider-outage`); with the page
  unreadable or switched off, Claude is available with the reason `unknown`. The
  plugin marks Claude down from its own rate-limit windows (see
  [Claude's state](#claudes-state)).

`MSGATE_STATUS_URL_OPENAI` and `MSGATE_STATUS_URL_ANTHROPIC` point the status
page check at another source, and `off` switches it off, as `MSGATE_PRICES_URL`
does for the price list. A check that is switched off is not listed in `skipped`.

### Marking a provider down

```sh
msgate providers mark codex --reason rate-limited --until 2026-10-23T18:00:00Z
```

`providers mark <provider> --reason <reason> [--until <RFC 3339>]` writes the
provider as unavailable, with the source `mark`, and prints
`providers: <provider> marked unavailable (<reason>)`. `--reason` is one of
`not-installed`, `not-logged-in`, `rate-limited`, `quota-exhausted` and
`provider-outage`. Without `--until` the mark lasts until the next check, which
is a minute; with it, until that time. An unknown provider, a reason outside the
list or a time that is not RFC 3339 exits 2.

A run that fails, and a `complete` that ends in an API error, mark their provider
down when the provider classes the error text as authentication (`not-logged-in`),
quota (`quota-exhausted`) or rate limit (`rate-limited`), without `--until`. Any
other failure marks nothing. A mark that cannot be written is a warning.

### The summary ranking

`summary_models` lists the models that can write a summary, cheapest first, each
as `{ provider, model, cost_usd, basis, calls }`: the cost of one summary call at
the list prices of [Cost](#cost). The `basis` is `measured` when `cost_usd` is
the mean of the model's own summary calls recorded in the last 30 days (`calls`
counts them), and `estimated` when it has none and a typical call is priced
instead. Codex models come from its catalog and Claude models from the price
table; a model with no price is left out.

Only `providers refresh` computes the ranking. `providers` prints what is stored,
so it is `[]` before the first refresh. When a refresh finds no usable price
table it leaves the stored ranking as it was.

## One-off completions

`msgate complete` asks Codex one question and prints the answer. It creates no
conversation and no run, and writes one model-call record.

```sh
echo '{"system": "Answer in one word.", "messages": [{"role": "user", "text": "Is 7 prime?"}]}' |
  msgate complete --provider codex --model gpt-6-luna --effort low --source my-hook
```

The request is a JSON object on standard input:
- `system` is optional text that replaces the instructions of the call; without
  it the call runs on the one-line instructions of the `lean` and `bare` levels
  (see [Harness levels](#harness-levels)).
- `messages` is a list of `{ "role": "user" | "assistant", "text": "<text>" }`,
  each with non-empty text, ending on a `user` message. A single message is sent
  as it is; several are sent as a transcript with a `User:` or `Assistant:` label
  on each.

A request that is not one such object exits 2.

The flags are `--provider codex` (the default and the only value), `--model`,
`--effort`, `--session-id` (default `$CLAUDE_CODE_SESSION_ID`), `--run-id`,
`--source` (default `cli`), `--call-id`, `--attr <key>=<value>`, repeatable,
`--timeout <seconds>` and `--json`. `--session-id`, `--run-id`, `--source`,
`--call-id` and `--attr` are recorded as for [`usage add`](#telemetry); a call
that names no model records `provider-default`.

The call runs Codex at the `bare` level, ephemeral and read-only, in the
directory `oneshot` of the msgate home.

The answer goes to standard output. With `--json` it is one object:

```json
{"outcome": "answered", "text": "Yes.", "model": "gpt-6-luna",
 "usage": {"input_tokens": 2950, "cached_input_tokens": 2432, "output_tokens": 4, "reasoning_output_tokens": 0}}
```

- `outcome` is `answered`, `empty-reply` (Codex ended well and said nothing) or
  `api-error`.
- `failure` is present only for an `api-error`: `auth`, `quota`, `rate-limit` or
  `other`.
- `usage` is what Codex reported for the call, all zeros when it reported none.

The exit code is:

| Code | Meaning |
|---|---|
| 0 | `answered` or `empty-reply` |
| the provider's own | `api-error` |
| 2 | usage error |
| 70 | internal error, or an answer that cannot be read or printed |
| 124 | `--timeout` ended the call |
| 127 | the Codex binary was not found |
| 130 | the call was cancelled (SIGINT or SIGTERM) |

The call is recorded whenever Codex reported usage for it, an API error included.

## Inside Claude Code

The plugin adds a mod. It has no skill and no agent type of its own; for those,
install [`msgate-agents`](https://github.com/JustAzul/msgate-agents).

The mod registers three tools. Two start and read background jobs, for work
Claude wants to run directly; the third is internal:

| Tool | Does |
|---|---|
| `mcp__msgate__run` | starts a job: `prompt` (required), and optionally `conversation_id`, `purpose`, `provider`, `model`, `effort`, `sandbox`, `harness`, `cwd` |
| `mcp__msgate__jobs` | `action` is `list`, `status`, `result` or `cancel`; `run_id` names the run for the last three |
| `mcp__msgate__progress` | takes no arguments and returns nothing: the marker that ends a step of an agent row, which Claude never calls |

`run` without a `conversation_id` starts a conversation (`exec`); with one it
continues that conversation (`send`). Its `harness` is a
[harness level](#harness-levels). The id may also be a Codex thread id that
msgate did not start (see [Continuing a Codex thread msgate did not start](#continuing-a-codex-thread-msgate-did-not-start)).
It returns at once with
`{conversation_id, run_id, continued}`, `continued` being true when it continued
the conversation it was given. Its runs carry the source `mod`, and they are
started with `--agent-feedback`: each is a [flagged run](#the-band-the-spinner-and-the-status-line),
so a plugin that opens agent rows for flagged runs shows it as a row until it
ends, and with none it shows in the band. The tool's description asks Claude to
start an agent type of an installed plugin instead when one exists for the
purpose and the model it wants, and to keep `run` for what the Agent tool
cannot set: `sandbox`, `cwd`, `provider` or `conversation_id`. `jobs` with
`result` returns the output cut at 64 KiB.

`progress` is for the [rows the engine answers](#rows-the-engine-answers): a
call from the main loop is refused, and the call and result lines of a row are
drawn as nothing, so the person sees neither.

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

A run that asks to be shown with `--agent-feedback` (see
[below](#using-it-from-hooks-and-scripts)) is a **flagged run**: a hook's
review, for example, or a job started through the `run` tool. While it works it
shows in a band above the prompt: one line that starts `msgate · ` with its
purpose, its source, how long it has run, what it has cost so far and a short
summary of what it is doing, gone once it ends. The band shows the flagged runs
whose source is not `agent`, since the rows the engine answers show their own
runs, and not those that have a [hidden row](#hidden-rows). Only you see the
band: nothing about the run enters the conversation, and the caller that started
it delivers its result.

The summaries come from the cheapest model that can write one (see
[Which model writes a summary](#which-model-writes-a-summary)), at low effort:
the mod sends a run's newest entries, each cut to 300 characters, to Claude
through the session's own sign-in or to Codex through `msgate complete`. An
**entry** is one item of a run's progress: a command the provider ran, a
message, a reasoning step. (A **step**, below, is a request
Claude Code makes to an agent row within a turn.) There are two kinds, each with
its own interval, and the plugin's `liveSummaryEvery` and `recordedSummaryEvery`
options set them:
- **Live summary.** A label of three to five words (`» Reading workerlog.go`),
  asked on the run's first entry and then every three (`liveSummaryEvery`, from
  1 to 10). It is shown while the run works. A row whose type is backed with
  `stepped` also writes each window's label into the row's history, as the text
  that ends the window's step (see [Steps](#rows-the-engine-answers)); anywhere
  else the label is not kept.
- **Recorded summary.** One or two sentences about a window of entries
  (`» 1–8: Read the worker and ran its tests.`), asked once eight entries have
  gathered (`recordedSummaryEvery`, from 1 to 24; `0` asks none), with every
  entry not yet recorded in its window, and once more, at the end of the run,
  for the entries left over (waiting at most 15 seconds for a Claude call and
  30 for a Codex one). The sentences are
  stored in the history of the row that follows the run, an agent row or a
  [hidden row](#hidden-rows). A sentence whose request fails is missing, and the
  next one covers what was left. Nothing of them is in the report that returns
  to Claude.

Both are skipped when nothing is on screen (`claude -p`). The plugin's
`summaries` option turns them off, and so does the environment variable
`MSGATE_SUMMARIES=0`; `MSGATE_SUMMARIES=1` turns them on where there is a
screen. The variable wins over the option when it is set, and any other value
leaves the option in force. With summaries off the mod makes no summary request
and adds nothing of its own to a row: it shows only what the provider emits
(commands, messages, and reasoning that arrives as text), with no
`msgate · <type>` line and no elapsed-time line, and nothing for reasoning the
provider encrypts. While summaries are on, no raw entry shows in a row or the
band: a summary that fails leaves the last one in place, and before a run's
first summary its band line ends after the cost. The one exception is a band
line while no provider can write a summary (see
[Which model writes a summary](#which-model-writes-a-summary)).

While the turn of a row the engine answers runs, the mod sets the row's
spinner message to the newest summary, or its random word while no provider can
write one, or to `msgate · <type>` until there is one, unless Claude Code has
already set a message of its own there.

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

### Which model writes a summary

Live and recorded summaries take the first model of the
[summary ranking](#the-summary-ranking) whose provider is available, at low
effort. The mod reads the ranking and each provider's state from
`msgate providers --json` and holds them for 5 minutes, or for 1 minute while a
provider is down. A ranking that is empty or cannot be read leaves one
candidate, `claude-haiku-5-5`.
- **Slots.** Claude and Codex each have 2 summary requests in flight at once,
  shared by the live and the recorded summaries.
- **A refusal for rate limit.** A 429 pauses that provider alone, for a minute,
  and the request goes once to the cheapest candidate of the other provider.
- **Codex.** A Codex summary runs as `msgate complete --source mod-summary`,
  which records the call; a Claude summary is recorded by the mod with the same
  source.
- **The end of a run.** The recorder waits up to 15 seconds for a Claude call
  and up to 30 for a Codex one.
- **No provider available.** When every candidate's provider is paused or
  marked down, no model is asked; a provider whose slots are only busy is
  waited for. A live summary that is due is then a random word
  (`» Pondering…`), shown in the spinner and the thinking line only: the band
  shows the run's newest raw entry instead, and no step of a row's history ends
  on a word. A recorded window is spent as its newest message, else its newest
  command (`» 9–16: $ go test ./...`), and a window of reasoning alone records
  nothing. The window is written at once, without waiting for its interval,
  when a step of a `stepped` row ends, so the step ends on that line, and at
  the end of a run.

### Claude's state

Claude has no check of the CLI's own beyond its status page, so the plugin tells
the [cache](#the-cache) what it sees. Before each Claude completion the mod
makes, at most once a minute, it reads the session's rate-limit windows. When a
`five_hour`, `seven_day` or `spend_limit` window is full, it runs
`msgate providers mark anthropic --reason rate-limited --until <time>`, with the
window's reset or one minute from now, whichever comes first, and renews the
mark at the next look while a window stays full. A window limited to one model
does not count. A completion Claude refuses with a 429 marks `anthropic` down
without `--until`, so until the next check.

## Rows the engine answers

A plugin can register an agent type with Claude Code and ask `msgate` to answer
it with runs, by **backing** the type through the interface (see
[`backType`](#for-plugin-authors-the-msgate-interface)). The engine then answers every model request of an
agent of that type in the session, whoever spawned it, usually Claude. No Claude
model runs inside such an agent; a dispatch that fails says so in its answer
instead of falling back to a Claude reply. Every other agent passes through
untouched. This is how `msgate-agents` offers its agents.

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
  few entries (`» Reading workerlog.go`), with an elapsed-time line when it is
  quiet, and stores a sentence about every eight entries in the agent's
  history; with summaries off, only what the provider emits. A type backed with
  `stepped` shows it in steps (see Steps).
- **Steps.** A type backed with `stepped: true` is followed in several steps
  instead of one. Each step covers a window of `liveSummaryEvery` entries and
  waits for the label of that window. It ends with the label as text, then the
  recorded sentences that have settled since the last step, then a call of the
  internal `progress` tool, which the engine answers so that Claude Code asks
  for the next step. The label stays in the row's history as text, where in one
  step it is only drawn while the run works. The step in which the run ends
  carries the recorded sentences not yet written and the answer. The rules:
  - A row answers in one step, as it did before, when its type is not backed
    with `stepped`, when summaries are off, when the session has no screen
    (`claude -p`), and when the `progress` tool did not register in this load of
    the mod. A turn that is already in steps when the summaries are switched off
    ends in the step it is in.
  - The steps that end in `progress`, and the one that ends the turn, do not
    count toward the step limit below, so a run of any length is followed to its
    end. Only the first step of a stepped turn counts.
  - The engine keeps what a stepped turn needs between its steps (the run and
    how far it has been read) and lets go of it when the turn ends. When
    Claude Code reports the turn complete between two steps because the row was
    stopped, a row that started its own run cancels it, as stopping inside a
    step does; a cancel that fails goes to the debug log with the run id.
  - When the engine reloads in the middle of a stepped run, a hidden row goes
    on with a new follow that reads the run's entries again, with one line in
    the debug log. A row that started its own run ends its turn with the
    hand-off failure, which says to read the run with the `jobs` tool; the run
    goes on to its end and the engine delivers nothing for it.
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
  registered ends the turn, and it writes one line to the debug log. A stepped
  turn counts only its first step.

### Hidden rows

A plugin can show a flagged run as a row of its own. It backs a type with
`command: 'follow'` and `promptMode: 'run-id'`, lists the runs to show with
[`flaggedRuns()`](#flaggedruns) and spawns one row per run, with the prompt
`msgate run <run_id>`. The engine answers such a row only when the plugin that
backed its type spawned it, and only for a run of the session that asks to be
shown.
- **Prompt.** The engine opens the row with the whole prompt of its run below
  the run id: `msgate run <run_id>`, a blank line, then the output of
  `msgate prompt <run_id>`, up to 1 MiB (1048576 bytes of UTF-8, counted on that
  output). It makes two reads for that, `msgate status` and `msgate prompt`,
  each limited to 5 seconds. A read that fails, a run that is not one the
  engine answers for, an empty prompt, an output over 1 MiB, or output that
  reached the 4 MiB the mod reads from a command (`$.process.run` keeps no more
  and marks the rest as truncated) leaves the row opening with the plain
  prompt, one line in the debug log saying why: a prompt larger than 1 MiB
  therefore shows only the run id. The cap exists because the model the row
  runs on has a context window that a larger prompt overflows: the host accepts
  the spawn, then the turn of the row fails at once with "Prompt is too long",
  and the row ends with no labels and no final text, which is worse than a row
  that shows only the run id. The limit was measured with the `haiku` row of
  msgate-agents (a text of 2900000 characters ran, one of 3100000 failed) and
  holds for that model. A host that refuses the whole prompt is asked once more
  with the plain one.
- **Where the prompt is kept.** The whole prompt enters the transcript of the
  row's session, which Claude Code keeps on disk, where before only
  `msgate run <run_id>` did.
- **Registered.** The engine registers a row when it opens, with the run its
  prompt names. The run leaves the band then, and `flaggedRuns()` no longer
  lists it, whatever becomes of the row.
- **Follow.** The row shows what the run is doing, as a backed row does, and
  stores the same recorded summaries in its history, in steps when its type is
  backed with `stepped`. It starts no run: the hook that started the run
  delivers its result.
- **Answer.** The turn ends with the plain text
  `msgate run <run_id> ended <outcome>`, never a hand-back. When Claude Code
  calls for another step of the turn after that text, as it does in the auto
  permission mode, the step ends with a stop and no text, so the transcript does
  not repeat it. Nothing of it reaches Claude.
- **Stop.** Stopping the row hides it and does not cancel the run, and the band
  does not show that run again.
- **Permission modes.** The row opens in every permission mode, `auto` included.
  In `auto`, Claude Code does not accept a row that ends with plain text: it
  calls for the step three more times and raises `SubagentStop` four times for
  the row, and the model still receives nothing from it (see
  [`docs/hidden-rows-proof.md`](docs/hidden-rows-proof.md)). Claude Code also
  runs its auto-mode classifier once per row, with the main model, when the
  row's turn ends: a request of about 139 thousand characters whose size does
  not depend on the run's prompt (measured with prompts of 5 kB and 1 MiB), and
  nothing of it enters the main session's transcript. `MSGATE_ROW_MODE=auto`
  restores the refusal: the spawn is then refused
  (`msgate: a hidden row does not open, since MSGATE_ROW_MODE=auto refuses it`)
  and the run stays in the band. The variable is read at each spawn (see
  [Environment variables](#environment-variables)).
- **Guard.** A hidden row never reaches the model beneath with a prompt it
  shows whole, since that prompt can be megabytes. A registered row whose run the
  engine does not answer for (the run is unknown, belongs to another session or
  does not ask to be shown) ends its turn with the text
  `msgate run <run_id> is not available to this row` and runs no tools; so does
  a row that is not registered but whose turn started with the whole prompt,
  such as one whose registration could not be written. The engine tells the two
  forms by what the turn started with, and keeps only the run id of a whole
  prompt, never its text. Only a row that is not registered and whose turn
  started with the plain `msgate run <run_id>` passes to the model beneath, as
  does any other row of the type that the plugin that backed it did not spawn.

## For plugin authors: the msgate interface

The mod adds the interface `msgate` to Claude Code, so another plugin's mod
reaches the engine as `$.msgate`, with no binary to run and no agent type to
install. It is an object of seven functions:
`{ complete, backType, version, run, jobs, flaggedRuns, models }`. The engine knows its caller from
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
- `request` is a `$.model.complete` request. For Claude it is passed through
  unchanged (cache marks and timeout included). The answer is that call's reply,
  unchanged.
- `meta` is `{ contract, callId, provider?, attrs? }`. `contract` must be `1`.
  `callId` is the caller's own id for the call (letters, digits and `:._-`,
  starting with a letter or digit, at most 200 characters). `provider`, when
  given, must be `anthropic` (the default) or `codex`. `attrs` is an object of strings; the engine keeps
  at most 16 whose key is 1 to 32 characters of `a-z` and `_` and whose value
  has at most 200 characters, and drops the rest without refusing the call.
- The model is called once per call: no retry, and a `callId` already being
  served is refused.
- With `provider: 'codex'` the call is answered by `msgate complete` (see
  [One-off completions](#one-off-completions)). Only a text request goes: a text
  `prompt` and, optionally, a text `system` (a string, or a list of text blocks
  joined with nothing between them), with `model`, `effort` and `timeoutMs`
  honoured (`timeoutMs` is rounded up to whole seconds; with none, the call ends
  after 600 seconds). Cache marks and `maxTokens` are ignored. A request with tools, images or
  a block that is not text is refused with `msgate:pre-model:request`. The reply
  has the same arms as for Claude: answered, `empty-reply`, `api-error` and
  `aborted`. An `api-error` carries the status 429 for a rate-limit failure and
  the kind `rate_limit`, `authentication_failed` or `billing_error` for a
  rate-limit, auth or quota one.
- After a billed Claude reply the call is recorded without being awaited, as a
  best-effort model-call record with the source `mod:<calling plugin>`, the
  caller's `callId` and attrs. A Codex call is recorded by the CLI with the same
  source, `callId` and attrs, so it is not recorded twice.
- Refusals are `msgate:pre-model:<why>` when the model was not called and can
  no longer be (`contract`, `call-id`, `unsupported-provider`, `request`,
  `duplicate`, `cancelled`, `abandoned-<kind>`, `re-entry`), and
  `msgate:after-model:<why>` when it may have been (`throw`, `timeout`,
  `refused`). A call the engine holds no state for is refused with
  `msgate:unknown-state:<kind>`, which a caller should read as ambiguous. A call
  that reaches the interface's own body because no hook answered it throws
  `msgate:unserved`.

### `backType({ type, command, promptMode, model?, effort?, sandbox?, harness?, purpose?, stepped? })`

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
- `harness` is an optional [harness level](#harness-levels), `full`, `lean` or
  `bare`, that the type's runs carry, recorded as the conversation's default. A
  `follow` type takes none, and a `review` type cannot be `bare`.
- `purpose` is optional non-empty text: the purpose the type's runs are
  recorded with (`--purpose`), the type's own name when it is left out. It sets
  nothing else, as for any purpose.
- `stepped` is an optional boolean. `true` asks for the type's rows to be
  followed [in steps](#rows-the-engine-answers); left out or `false`, a row is
  followed in one step.
- Anything else is refused with `msgate:backType:<field>`, naming the field
  (`type`, `command`, `promptMode`, `model`, `effort`, `sandbox`, `harness`,
  `purpose` or `stepped`).
- The backed types live in the engine's session state, keyed by full type, so an
  engine reload keeps them. A later call for the same type replaces its data.
  A plugin calls `backType` at every session start.

### `run({ prompt, conversation_id?, purpose?, provider?, model?, effort?, sandbox?, harness?, cwd? })`

Admits a job like the `run` tool and resolves
`{ conversation_id, run_id, continued }`. As in the tool, `conversation_id` in
the call may be a Codex thread id that msgate did not start. The run's source is
`plugin:<caller>`. The engine does not deliver its result to Claude and adds no
delivery-queue entry: the caller reads the result with `jobs`. `harness` is a
[harness level](#harness-levels): it sets the default of a new conversation, and
on a continued one it applies to that turn only. Left out, the stored level
applies.

### `jobs({ action, run_id? })`

`action` is `list`, `status`, `result` or `cancel`, as for the `jobs` tool.
A plugin sees only its own runs: `list` returns the session's runs whose source
is `plugin:<caller>`, and `status`, `result` and `cancel` refuse a run whose
source is not, with `msgate:jobs:not-yours`. A plugin cannot read or cancel the
runs of Claude or of another plugin.

### `models({ provider? })`

Resolves `{ provider, models: [{ id, name, visibility }], available, reason?,
checked_at? }`: the catalog `msgate models --json` prints for the provider, which
is `codex` when none is named, in the provider's order and unfiltered, and
whether the provider can take work. Any plugin can call it.
- `available` and `checked_at` (an RFC 3339 instant) come from
  `msgate providers --json` (see [Provider health](#provider-health)). A
  provider that is unavailable answers `models: []` with the `reason` and the
  `checked_at` of the check, and its catalog is not read. When the listing cannot
  be read, does not name the provider or has no check time, the provider counts as
  available and `checked_at` is left out.
- A provider whose binary is not installed answers `models: []`, `available:
  false`, `reason: "not-installed"` and the current time as `checked_at`, instead
  of a refusal.
- The engine reads a catalog once per load for each provider asked for and
  keeps it, since the read can take up to 10 seconds. A read that failed is not
  kept: the next call makes it again. The provider's availability is held for 5
  minutes, or 1 minute while any provider is down.
- It is refused with `msgate:models:failed` when `provider` is given and is not
  non-empty text, when the binary fails for any reason but a missing provider
  CLI, and when what it prints is not a catalog (`id` must be non-empty text,
  `name` and `visibility` text). The reason goes to the debug log.
- A consumer that keeps the first answer it gets per load now has an empty list
  for the load when Codex was down at load time, and with it no per-model types
  for that load. It should ask again once `available` is true.

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
| `MSGATE_STATUS_URL_OPENAI` | another source for the OpenAI status page that the Codex health check reads; `off` never goes to the network (see [Provider health](#provider-health)) |
| `MSGATE_STATUS_URL_ANTHROPIC` | the same for the Anthropic status page that decides Claude's health |
| `MSGATE_SUMMARIES` | the mod's summaries: `0` off, `1` on where there is a screen; it wins over the `summaries` option, and any other value leaves the option in force |
| `MSGATE_ROW_MODE` | read by the mod at each spawn of a hidden row. Empty or unset: no override. `auto`: the spawn is refused (see [Hidden rows](#hidden-rows)). Any other value is accepted and refuses nothing, and one that is not the name of a permission mode (`default`, `acceptEdits`, `bypassPermissions`, `plan`, `dontAsk`, `auto`; exact spelling) adds a note to the debug log |
| `MSGATE_TEST_*` | switches for the test suites; not for use |

No `AGENTCLI_*` variable is read.

## Exit codes

`msgate` exits with the provider's own exit code, except:

| Code | Meaning |
|---|---|
| 1 | the home migration was refused or failed (see [Moving from agentcli](#moving-from-agentcli)) |
| 2 | usage error (bad flag, missing prompt, reserved native flag, unsupported capability) |
| 3 | conversation busy, or `result` on an unfinished run |
| 4 | run or conversation not found; for `send`, an id that names no conversation, run or provider session |
| 5 | `wait` reached its own `--timeout` (the run keeps going), or `result --report` on an unfinished run |
| 6 | conversation not resumable yet |
| 7 | `prices refresh` could not get the price list (the cache is kept) |
| 70 | internal error, a job whose worker never started, or a `complete` answer that cannot be read or printed |
| 124 | the run or the `complete` call hit `--timeout` |
| 125 | the run was lost (its worker died) |
| 127 | provider CLI not found, or the launcher found no installed `msgate` plugin |
| 130 | cancelled by `msgate cancel`, or a `complete` call that got SIGINT or SIGTERM |
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
- Prompts go to the provider on stdin and are stored in the run directory
  (mode 0600, directories 0700). Telemetry never holds the prompt. The prompt of
  a run shown as a [hidden row](#hidden-rows) is also in the transcript of that
  row's session, which Claude Code keeps on disk.
- A plugin using the interface sees only the runs it started.
- With summaries on, the mod sends a run's newest entries, cut short, to the
  Anthropic API or to Codex, whichever is the cheaper available (see
  [Which model writes a summary](#which-model-writes-a-summary)); the
  `summaries` option and `MSGATE_SUMMARIES=0` turn that off.
- `msgate complete` runs Codex bare, ephemeral and read-only, in a directory of
  its own in the msgate home.

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
  workflow runs the release gate, builds the plugin tree, scans it for personal
  data and calls the whole CI, and publishes nothing. Only a release publishes:
  a commit whose subject is `chore: release <version>`, which changes only
  `VERSION`. A separate job, which starts only after the whole CI has passed on
  that commit, then builds the plugin tree as a single commit, tags it
  `v<version>`, creates the GitHub release with notes listing the commits since
  the previous version, and moves the `dist` branch to that commit last. The
  marketplace manifest on `main` points at `dist`. The build's manifest carries
  `VERSION` as its `version`, so an installed copy updates only when a release
  changes it. The release steps are in the guide at
  <https://raw.githubusercontent.com/JustAzul/agent-docs/main/plugin-releasing-dist.md>.
- The plugin's mod is plain JavaScript, tested with `claude plugin test plugin`.
  `make verify-plugin` loads the plugin in a model-free `claude -p` and fails
  unless the `msgate` interface is mounted with its seven methods; run it before
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
