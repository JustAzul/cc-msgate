# Hidden rows: proof on Claude Code 2.1.300

FR59 of the split PRD gates phase 3 (hidden rows for flagged runs) on a proof run on the installed Claude Code build. This page records that proof and what it changed in FR58.

## Setup

Two throwaway plugins played the two roles:
- **Spawner** (the agents plugin's role):
  - registers a hidden agent type and refuses to offer it to any model (`agent.offer`);
  - its own `$.clock.every(5000)` timer spawns one row per run id, with the prompt `msgate run <run_id>`.
- **Answerer** (the engine's role): answers that row through its turn hooks with a few progress steps, then ends the turn with the plain text `msgate run <run_id> ended ok`.

A project-settings `PostToolUse` hook with `asyncRewake: true` stood in for a review hook. It waited for the row to end, then delivered one message by `exit 2`.

Each mode ran a real `claude -p` session with streaming input, which keeps the main loop alive while the async hook runs. A third plugin read the main loop's API messages at each step. That shows what the model received, which is narrower than what the host's event stream carries.

## Measures

1. The row ends its turn once: one turn end in the answerer, one `SubagentStop`.
2. Nothing from the row reaches the model: neither its text nor its run id appears in the main loop's API messages.
3. The hook's delivery reaches the model once.

## Result

| Mode | Measure 1 | Measure 2 | Measure 3 |
|---|---|---|---|
| default | pass | pass | pass |
| bypassPermissions | pass | pass | pass |
| auto | fail | pass | pass |

In the auto mode, Claude Code does not accept a row that ends with plain text. It prompts the row three times to call `SubagentHandback`, and then ends it as having delivered no report. That makes four `SubagentStop` events for one row. The model still receives nothing from the row.

Two variants were measured in the auto mode:

| | Row opens | Measure 1 | Measure 2 | Measure 3 |
|---|---|---|---|---|
| The row ends with `SubagentHandback` | yes | pass | **fail**: the hand-back reaches the model as a new turn ("Another Claude session sent a message: … msgate run … ended ok") | pass |
| The engine's `agent.spawn` hook returns `{ deny }` | no | not applicable | pass | pass |

`$.agent.spawn` resolves `{ deny }` as a value and does not throw.

## Consequences for FR58

- **Auto mode:** the engine refuses the spawn of a hidden row (`{ deny }`). No row opens, and the run stays in the band.
- **Other modes:** the row ends with plain text, never with a hand-back.
- **Who spawned the row:** the engine reads the spawner as the origin plugin of the spawn, in its `agent.spawn` hook. `$.agent.list()` does not report who spawned a row that a plugin's own timer started.
- **Timer chain:** a row spawned in a chain that a plugin's own timer started does not reach that plugin's turn hooks, but it does reach another plugin's. This is why the spawner (the agents plugin) and the answerer (the engine) must be different plugins.
