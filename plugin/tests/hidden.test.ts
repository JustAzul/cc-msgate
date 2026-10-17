import { expect, test } from 'claude-code/testing'
import { AGENTS, POLL_MS, SESSION, engineOf, job, listing, ok, startTurn, step, world } from './kit'
import { BAND_REFRESH_MS } from '../hooks/band.js'
import { admitSpawn, followFailureText } from '../hooks/hidden.js'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ROW_TYPE = AGENTS + ':run'
const BACKED = { ...{ [ROW_TYPE]: { command: 'follow', promptMode: 'run-id' } } }
const AUTO_DENIAL = 'msgate: a hidden row does not open in the auto permission mode'
const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }
const BAND_PROPS = { hasSurvey: false } as any

// A flagged run of this session, as status prints it.
const flagged = (over: Record<string, unknown> = {}) =>
  job({ run_id: 'run-hook', source: 'hook-stop', purpose: 'code-review', background: false, session_id: SESSION, agent_feedback: true, state: 'running', started_at: '1970-01-01T00:00:00Z', ...over })
const finished = (over: Record<string, unknown> = {}) => flagged({ state: 'done', outcome: 'ok', ...over })

const hiddenRows = (w: ReturnType<typeof world>) => w.state.get('hiddenRows')?.value
const hide = (w: ReturnType<typeof world>, rows: Record<string, string>) => w.state.set('hiddenRows', { value: rows, version: 1 })

// The engine's hook for an `agent.spawn` that `spawner` made, over a spawn that
// starts the agent `ag-hidden`. `calls` counts how many times the spawn went on.
function spawnAs(w: ReturnType<typeof world>, spawner: string, over: Record<string, unknown> = {}) {
  const calls = { next: 0 }
  const e = { subagentType: ROW_TYPE, prompt: 'msgate run run-hook', permissionMode: 'default', ...over }
  const next = async () => {
    calls.next += 1
    return { model: 'claude-haiku-4-5', agentId: 'ag-hidden' }
  }
  return { calls, result: admitSpawn(engineOf(w), e, spawner, next) }
}

// A hidden row whose run the binary reports: `runs` lists the states it goes
// through, one per wait, and the last is the run's end.
function follower(final: Record<string, unknown> = finished(), waits = 1) {
  let waited = 0
  return (argv: string[]) => {
    if (argv[1] === 'wait') return ++waited < waits ? WAIT_TIMEOUT : ok(JSON.stringify(final))
    if (argv[1] === 'status') return argv[2] === 'run-hook' ? ok(JSON.stringify(final)) : { exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' }
    if (argv[1] === 'progress') return ok(JSON.stringify({ run_id: argv[2], state: 'running', next: Number(argv[4]), entries: [] }))
    return ok('')
  }
}

function hiddenRow(w: ReturnType<typeof world>, runId = 'run-hook') {
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  w.messages['ag-hidden'] = [{ role: 'user', text: 'msgate run ' + runId }]
  hide(w, { 'ag-hidden': runId })
}

test('the spawn of a hidden row by the plugin that backed its type opens the row and registers its run', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS)

  expect(await result).toEqual({ model: 'claude-haiku-4-5', agentId: 'ag-hidden' })
  expect(calls.next).toBe(1)
  expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
})

for (const mode of ['default', 'bypassPermissions', 'acceptEdits', undefined]) {
  test(`a hidden row opens in the ${mode ?? 'unnamed'} permission mode`, async ($, on) => {
    const w = world(on, { backed: BACKED })
    await $.session.start(START)

    const { calls, result } = spawnAs(w, AGENTS, { permissionMode: mode })

    expect(await result).toMatchObject({ agentId: 'ag-hidden' })
    expect(calls.next).toBe(1)
  })
}

test('the spawn of a hidden row in the auto permission mode is refused, so no row opens and none is registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS, { permissionMode: 'auto' })

  expect(await result).toEqual({ deny: AUTO_DENIAL })
  expect(calls.next).toBe(0)
  expect(hiddenRows(w)).toBeUndefined()
})

test('a spawn of a hidden row by another plugin goes on, and the row is not registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)

  const other = spawnAs(w, 'p3')
  const auto = spawnAs(w, 'p3', { permissionMode: 'auto' })

  expect(await other.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(await auto.result).toMatchObject({ agentId: 'ag-hidden' })
  expect([other.calls.next, auto.calls.next]).toEqual([1, 1])
  expect(hiddenRows(w)).toBeUndefined()
})

test('the spawn of a type that follows nothing, or of one no plugin backed, goes on in the auto mode too', async ($, on) => {
  const w = world(on)
  await $.session.start(START)

  const backed = spawnAs(w, AGENTS, { subagentType: AGENTS + ':adhoc', permissionMode: 'auto' })
  const unbacked = spawnAs(w, AGENTS, { subagentType: AGENTS + ':other', permissionMode: 'auto' })
  const builtin = spawnAs(w, AGENTS, { subagentType: undefined, permissionMode: 'auto' })

  expect(await backed.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(await unbacked.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(await builtin.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(hiddenRows(w)).toBeUndefined()
})

test('a hidden row whose prompt names no run opens but is not registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)

  const { result } = spawnAs(w, AGENTS, { prompt: 'check on something' })

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(hiddenRows(w)).toBeUndefined()
})

test('rows spawned together are all registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)
  const engine = engineOf(w)
  const spawn = (agentId: string, runId: string) =>
    admitSpawn(engine, { subagentType: ROW_TYPE, prompt: 'msgate run ' + runId, permissionMode: 'default' }, AGENTS, async () => ({ model: 'm', agentId }))

  await Promise.all([spawn('ag-1', 'run-1'), spawn('ag-2', 'run-2'), spawn('ag-3', 'run-3')])

  expect(hiddenRows(w)).toEqual({ 'ag-1': 'run-1', 'ag-2': 'run-2', 'ag-3': 'run-3' })
})

test('a registry that cannot be written is logged and the row still opens', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.stateWriteFailures = 100
  await $.session.start(START)

  const { result } = spawnAs(w, AGENTS)

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(w.debugLogs.join('\n')).toContain('could not register the hidden row of run-hook')
})

test('the engine hook goes through the spawn of any other agent untouched', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)

  await $.agent.spawn({ tool_use_id: 't', prompt: 'Read README.md.', description: 'read', subagentType: 'general-purpose', provider: { plugin: 'engine', tier: 'core' }, parentModel: 'm', permissionMode: 'auto', background: true, fork: false } as any)

  expect(w.spawns.length).toBe(1)
  expect(hiddenRows(w)).toBeUndefined()
})

test('a registered row follows its run and ends the turn with plain text, never a hand-back', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower(finished(), 2)
  hiddenRow(w)
  await $.session.start(START)

  const out = await step($, 'ag-hidden')

  expect(out.text).toBe('msgate run run-hook ended ok')
  expect(out.result.toolUses).toEqual([])
  expect(out.result.stopReason).toBe('end_turn')
  expect(out.chunks.map((c) => c.kind)).toEqual(['thinking', 'text', 'stop'])
  expect(out.chunks.at(-1)).toMatchObject({ kind: 'stop', stopReason: 'end_turn' })
  expect(w.modelSteps).toEqual([])
  // It admits nothing and cancels nothing: it only reads the run.
  const commands = w.runs.map((r) => r.argv[1]).filter((command) => command !== 'eta')
  expect(commands).toContain('wait')
  expect(commands.filter((command) => ['exec', 'send', 'review', 'cancel'].includes(command!))).toEqual([])
})

test('the progress of the run streams into the row as thinking', async ($, on) => {
  const w = world(on, { backed: BACKED })
  let waited = 0
  w.respond = (argv) => {
    if (argv[1] === 'wait') return ++waited < 2 ? WAIT_TIMEOUT : ok(JSON.stringify(finished()))
    if (argv[1] === 'progress') {
      const entries = argv[4] === '0' ? [{ seq: 0, at: 't', kind: 'command', text: 'rg -n token auth.go' }] : []
      return ok(JSON.stringify({ run_id: 'run-hook', state: 'running', next: 1, entries }))
    }
    return ok(JSON.stringify(finished()))
  }
  hiddenRow(w)
  await $.session.start(START)

  const out = await step($, 'ag-hidden')

  expect(out.chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())).toEqual(['msgate · run', '$ rg -n token auth.go'])
})

test('a row ends with the outcome of the run, whatever it was', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower(finished({ state: 'failed', outcome: 'error', error_excerpt: 'boom' }))
  hiddenRow(w)
  await $.session.start(START)

  expect((await step($, 'ag-hidden')).text).toBe('msgate run run-hook ended error')
})

test('a later step of the same turn repeats the text without reading the run again', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  hiddenRow(w)
  await $.session.start(START)
  const first = await step($, 'ag-hidden', { turnId: 'turn-1', index: 0 })
  const reads = w.runs.length

  const second = await step($, 'ag-hidden', { turnId: 'turn-1', index: 1 })

  expect(first.text).toBe('msgate run run-hook ended ok')
  expect(second.text).toBe(first.text)
  expect(second.result.toolUses).toEqual([])
  expect(w.runs.length).toBe(reads)
  expect(w.modelSteps).toEqual([])
})

test('a hidden row that is not registered passes to the model beneath', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  await $.session.start(START)

  const out = await step($, 'ag-hidden')

  expect(out.text).toBe('a Claude model answered')
  expect(w.modelSteps).toEqual(['ag-hidden'])
  expect(w.runs.filter((r) => r.argv[1] === 'wait')).toEqual([])
})

const NOT_ANSWERED: [string, (argv: string[]) => any][] = [
  ['a run the engine does not know', () => ({ exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' })],
  ['a run of another session', () => ok(JSON.stringify(flagged({ session_id: 'sess-other' })))],
  ['a run that does not ask to be shown', () => ok(JSON.stringify(flagged({ agent_feedback: false })))],
]
for (const [name, status] of NOT_ANSWERED) {
  test(`a registered row that names ${name} passes to the model beneath`, async ($, on) => {
    const w = world(on, { backed: BACKED })
    w.respond = (argv) => status(argv)
    hiddenRow(w)
    await $.session.start(START)

    const out = await step($, 'ag-hidden')

    expect(w.modelSteps).toEqual(['ag-hidden'])
    expect(out.text).toBe('a Claude model answered')
    expect(w.runs.filter((r) => r.argv[1] === 'wait')).toEqual([])
  })
}

test('a run that cannot be read fails the row in plain text and does not pass it to the model', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = () => ({ exitCode: 1, stdout: '', stderr: 'msgate: store is locked\n' })
  hiddenRow(w)
  await $.session.start(START)

  const out = await step($, 'ag-hidden')

  expect(w.modelSteps).toEqual([])
  expect(out.result.toolUses).toEqual([])
  expect(out.text).toContain('the hand-off to the agent failed')
  expect(out.text).toContain('store is locked')
})

test('a run flagged by a hook leaves the band as soon as its row is registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = (argv) => {
    if (argv[1] === 'progress') return ok(JSON.stringify({ run_id: argv[2], state: 'running', next: 0, entries: [] }))
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(flagged(), flagged({ run_id: 'run-other' }))
  }
  await $.session.start(START)
  await w.clock.advance(POLL_MS)
  const band = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: BAND_PROPS })
  expect((await band.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-stop · 15s', 'msgate · code-review · hook-stop · 15s'])

  hide(w, { 'ag-hidden': 'run-hook' })
  await w.clock.advance(BAND_REFRESH_MS)

  expect(await band.findAll({ type: 'Text' })).toHaveLength(1)
  // The next poll lists the run again, and the band does not take it back.
  await w.clock.advance(POLL_MS)
  expect(await band.findAll({ type: 'Text' })).toHaveLength(1)
})

// A step cannot be aborted through the kit, so the end of a follow that threw is
// decided where a test can hand it the signal and the error.
test('a follow that threw after the row was stopped ends with the stopped text, not a hand-off failure', () => {
  const stop = new AbortController()
  stop.abort()

  expect(followFailureText(new Error('$.process.run(/plugin/bin/msgate) aborted'), 'run-hook', stop.signal)).toBe('msgate run run-hook stopped')
})

test('a follow that threw while the row was not stopped ends with the hand-off failure', () => {
  const text = followFailureText(new Error('store is locked'), 'run-hook', new AbortController().signal)

  expect(text).toBe('msgate: the hand-off to the agent failed, so this is not its answer. store is locked')
})

const bash = ($: any) => $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'ag-hidden' } as any)

test('the model beneath keeps its tools in a hidden row the engine passed to it', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  await $.session.start(START)
  await step($, 'ag-hidden')

  const call: any = await bash($)

  expect(call.deny).toBeUndefined()
})

test('the model beneath keeps its tools in a registered row whose run the engine does not answer for', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = () => ({ exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' })
  hiddenRow(w)
  await $.session.start(START)
  await step($, 'ag-hidden')

  const call: any = await bash($)

  expect(call.deny).toBeUndefined()
})

test('a hidden row the engine answers runs no tools', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  hiddenRow(w)
  await $.session.start(START)
  await step($, 'ag-hidden')

  const call: any = await bash($)

  expect(call.deny).toContain('msgate agents run no tools')
})
