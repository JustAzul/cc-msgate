import { expect, test } from 'claude-code/testing'
import { AGENTS, POLL_MS, SESSION, engineOf, job, listing, ok, startTurn, step, world } from './kit'
import { BAND_REFRESH_MS } from '../hooks/band.js'
import { SHOWN_PROMPT_MAX_BYTES, admitSpawn, followFailureText, isRewrittenPrompt, runIdOfPrompt } from '../hooks/hidden.js'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ROW_TYPE = AGENTS + ':run'
const BACKED = { ...{ [ROW_TYPE]: { command: 'follow', promptMode: 'run-id' } } }
const AUTO_DENIAL = 'msgate: a hidden row does not open, since MSGATE_ROW_MODE=auto refuses it'
const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }
const BAND_PROPS = { hasSurvey: false } as any

// A flagged run of this session, as status prints it.
const flagged = (over: Record<string, unknown> = {}) =>
  job({ run_id: 'run-hook', source: 'hook-stop', purpose: 'code-review', background: false, session_id: SESSION, agent_feedback: true, state: 'running', started_at: '1970-01-01T00:00:00Z', ...over })
const finished = (over: Record<string, unknown> = {}) => flagged({ state: 'done', outcome: 'ok', ...over })

const hiddenRows = (w: ReturnType<typeof world>) => w.state.get('hiddenRows')?.value
const hide = (w: ReturnType<typeof world>, rows: Record<string, string>) => w.state.set('hiddenRows', { value: rows, version: 1 })

// The engine's `$` with the environment of the world, which `engineOf` does not carry.
const withEnv = (w: ReturnType<typeof world>) => ({
  ...engineOf(w),
  env: {
    get: async (name: string) => {
      if (w.envFails) throw new Error('the environment is unavailable')
      return w.env.get(name)
    },
  },
})

// The engine's hook for an `agent.spawn` that `spawner` made, over a spawn that
// starts the agent `ag-hidden`. `calls` counts how many times the spawn went on.
function spawnAs(w: ReturnType<typeof world>, spawner: string, over: Record<string, unknown> = {}) {
  const calls = { next: 0, sent: [] as any[] }
  const e = { subagentType: ROW_TYPE, prompt: 'msgate run run-hook', permissionMode: 'default', ...over }
  const next = async (sent: any) => {
    calls.next += 1
    calls.sent.push(sent)
    return { model: 'claude-haiku-4-5', agentId: 'ag-hidden' }
  }
  return { calls, result: admitSpawn(withEnv(w), e, spawner, next) }
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

// What the binary answers for the run `run-hook` of this session: its status,
// and its prompt as `prompt` prints it.
function promptServer(text: string, status: Record<string, unknown> = flagged()) {
  return (argv: string[]) => {
    if (argv[1] === 'status') return ok(JSON.stringify(status))
    if (argv[1] === 'prompt') return ok(text)
    throw new Error('unexpected msgate call: ' + argv.join(' '))
  }
}

// promptServer with some of its answers replaced, by command; an answer that is
// a function is called as the command runs.
function promptServerOf(over: Record<string, any>) {
  const normal = promptServer('a prompt')
  return (argv: string[]) => {
    const answer = over[argv[1]!]
    if (answer === undefined) return normal(argv)
    return typeof answer === 'function' ? answer(argv) : answer
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

test('a hidden row opens in the auto permission mode when MSGATE_ROW_MODE is not set, and is registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS, { permissionMode: 'auto' })

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(calls.next).toBe(1)
  expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
})

test('MSGATE_ROW_MODE=auto refuses the spawn of a hidden row in any permission mode, so no row opens and none is registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.env.set('MSGATE_ROW_MODE', 'auto')
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS, { permissionMode: 'default' })

  expect(await result).toEqual({ deny: AUTO_DENIAL })
  expect(calls.next).toBe(0)
  expect(hiddenRows(w)).toBeUndefined()
})

test('MSGATE_ROW_MODE=default does not refuse a hidden row in the auto permission mode', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.env.set('MSGATE_ROW_MODE', 'default')
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS, { permissionMode: 'auto' })

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(calls.next).toBe(1)
  expect(w.debugLogs.filter((text) => text.includes('MSGATE_ROW_MODE'))).toEqual([])
})

test('a MSGATE_ROW_MODE that names no permission mode opens the row and is noted once', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.env.set('MSGATE_ROW_MODE', 'banana')
  await $.session.start(START)

  const first = spawnAs(w, AGENTS, { permissionMode: 'auto' })
  const second = spawnAs(w, AGENTS, { permissionMode: 'auto' })

  expect(await first.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(await second.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(w.debugLogs.filter((text) => text.includes('MSGATE_ROW_MODE'))).toEqual([
    'MSGATE_ROW_MODE=banana is not a permission mode the host knows; only auto refuses the row',
  ])
})

test('an environment that cannot be read counts as MSGATE_ROW_MODE not set, and the failure is logged once', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.envFails = true
  await $.session.start(START)

  const first = spawnAs(w, AGENTS, { permissionMode: 'auto' })
  const second = spawnAs(w, AGENTS, { permissionMode: 'auto' })

  expect(await first.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(await second.result).toMatchObject({ agentId: 'ag-hidden' })
  expect([first.calls.next, second.calls.next]).toEqual([1, 1])
  expect(w.debugLogs.filter((text) => text.includes('MSGATE_ROW_MODE'))).toEqual([
    'MSGATE_ROW_MODE could not be read: the environment is unavailable; it counts as not set',
  ])
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

test('a later step of the same turn ends with the stop alone and no text, without reading the run again', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  hiddenRow(w)
  await $.session.start(START)
  const first = await step($, 'ag-hidden', { turnId: 'turn-1', index: 0 })
  const reads = w.runs.length

  const second = await step($, 'ag-hidden', { turnId: 'turn-1', index: 1 })

  expect(first.text).toBe('msgate run run-hook ended ok')
  expect(second.chunks).toEqual([{ kind: 'stop', stopReason: 'end_turn', usage: null }])
  expect(second.result).toEqual({ turnId: 'turn-1', index: 1, answer: '', toolUses: [], stopReason: 'end_turn', usage: null })
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

test('a row that is not registered and carries the plain prompt of a run passes to the model beneath', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  w.messages['ag-hidden'] = [{ role: 'user', text: 'msgate run run-hook' }]
  await $.session.start(START)

  const out = await step($, 'ag-hidden')

  expect(out.text).toBe('a Claude model answered')
  expect(w.modelSteps).toEqual(['ag-hidden'])
})

test('a row that is not registered and carries the rewritten prompt ends in plain text and never passes to the model beneath', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = follower()
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  await $.session.start(START)
  await startTurn($, 'ag-hidden', 'msgate run run-hook\n\nlinha 1\nlinha 2')

  const out = await step($, 'ag-hidden')

  expect(out.text).toBe('msgate run run-hook is not available to this row')
  expect(out.result.toolUses).toEqual([])
  expect(w.modelSteps).toEqual([])
  expect(w.runs).toEqual([])
})

test('a row whose registry write failed and whose session saves no transcript still never passes a prompt of 3 MiB to the model beneath', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.stateWriteFailures = 100
  await $.session.start(START)
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  const { result } = spawnAs(w, AGENTS)
  await result
  await startTurn($, 'ag-hidden', 'msgate run run-hook\n\n' + 'x'.repeat(3 * 1024 * 1024))

  const out = await step($, 'ag-hidden')

  expect(hiddenRows(w)).toBeUndefined()
  expect(w.modelSteps).toEqual([])
  expect(out.text).toBe('msgate run run-hook is not available to this row')
})

test('a rewritten prompt that the host wraps in reminders is still recognised, and the row never passes it to the model beneath', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  await $.session.start(START)
  await startTurn($, 'ag-hidden', '<system-reminder>\nsession context\n</system-reminder>\nmsgate run run-hook\n\nlinha 1')

  const out = await step($, 'ag-hidden')

  expect(out.text).toBe('msgate run run-hook is not available to this row')
  expect(w.modelSteps).toEqual([])
})

test('a start of a row with a prompt that is not the rewritten form clears what an earlier start left, and the plain prompt passes to the model beneath', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  await $.session.start(START)
  await startTurn($, 'ag-hidden', 'msgate run run-hook\n\nlinha 1', 'turn-1')
  await startTurn($, 'ag-hidden', 'msgate run run-hook', 'turn-1')

  const out = await step($, 'ag-hidden', { turnId: 'turn-1' })

  expect(out.text).toBe('a Claude model answered')
  expect(w.modelSteps).toEqual(['ag-hidden'])
})

test('what a row\'s turn started with does not carry to another turn that has no start of its own', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  await $.session.start(START)
  await startTurn($, 'ag-hidden', 'msgate run run-hook\n\nlinha 1', 'turn-1')

  const out = await step($, 'ag-hidden', { turnId: 'turn-2' })

  expect(out.text).toBe('a Claude model answered')
})

const NOT_ANSWERED: [string, (argv: string[]) => any][] = [
  ['a run the engine does not know', () => ({ exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' })],
  ['a run of another session', () => ok(JSON.stringify(flagged({ session_id: 'sess-other' })))],
  ['a run that does not ask to be shown', () => ok(JSON.stringify(flagged({ agent_feedback: false })))],
]
for (const [name, status] of NOT_ANSWERED) {
  test(`a registered row that names ${name} ends in plain text saying so and never passes to the model beneath`, async ($, on) => {
    const w = world(on, { backed: BACKED })
    w.respond = (argv) => status(argv)
    hiddenRow(w)
    await $.session.start(START)

    const out = await step($, 'ag-hidden')

    expect(w.modelSteps).toEqual([])
    expect(out.text).toBe('msgate run run-hook is not available to this row')
    expect(out.result.toolUses).toEqual([])
    expect(out.result.stopReason).toBe('end_turn')
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

test('a registered row whose run the engine does not answer for runs no tools, since no model answers it', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = () => ({ exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' })
  hiddenRow(w)
  await $.session.start(START)
  await step($, 'ag-hidden')

  const call: any = await bash($)

  expect(call.deny).toContain('msgate agents run no tools')
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

test('a run that gets a hidden row keeps its summary across the polls that follow', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.surfaces = ['terminal']
  w.respondModel = () => ({ isAnswered: true, text: 'Reading auth.go', usage: { input_tokens: 1, output_tokens: 1, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } })
  w.respond = (argv) => {
    if (argv[1] === 'progress') {
      const entries = argv[4] === '0' ? [{ seq: 0, at: 't', kind: 'message', text: 'I will read auth.go' }] : []
      return ok(JSON.stringify({ run_id: 'run-hook', state: 'running', next: 1, entries }))
    }
    if (argv[1] === 'wait') return ok(JSON.stringify(finished()))
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    if (argv[1] === 'usage') return ok('')
    if (argv[1] === 'status' && argv[2] === 'run-hook') return ok(JSON.stringify(finished()))
    return listing(flagged())
  }
  await $.session.start(START)
  await w.clock.advance(POLL_MS)
  expect(w.modelRequests).toHaveLength(1)

  hiddenRow(w)
  await w.clock.advance(POLL_MS * 3)
  const out = await step($, 'ag-hidden')

  // The label that settled while the band showed the run is the row's now.
  expect(out.chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())).toContain('» Reading auth.go')
  expect(w.modelRequests).toHaveLength(1)
})

test('the run a hidden row names is read from the first line of its prompt, with a body after it or without', () => {
  expect(runIdOfPrompt('msgate run r-1')).toBe('r-1')
  expect(runIdOfPrompt('msgate run r-1\n\ncorpo')).toBe('r-1')
  expect(runIdOfPrompt('outra coisa\nmsgate run r-1')).toBeNull()
  expect(runIdOfPrompt('msgate run r-1 and more')).toBeNull()
  expect(runIdOfPrompt(undefined)).toBeNull()
})

test('a prompt is the rewritten form when its first line is followed by a blank line and a body that is not empty', () => {
  expect(isRewrittenPrompt('msgate run r-1\n\nlinha 1\nlinha 2')).toBe(true)
  expect(isRewrittenPrompt('msgate run r-1')).toBe(false)
  expect(isRewrittenPrompt('msgate run r-1\n\n')).toBe(false)
  expect(isRewrittenPrompt('msgate run r-1\n\n  \n')).toBe(false)
  expect(isRewrittenPrompt('msgate run r-1\ncorpo')).toBe(false)
  expect(isRewrittenPrompt('outra coisa\n\ncorpo')).toBe(false)
  expect(isRewrittenPrompt(undefined)).toBe(false)
})

test('the spawn of a hidden row shows the prompt of its run whole below the run id, and the rest of the call goes on intact', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = promptServer('linha 1\nlinha 2')
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS, { description: 'review the diff', mode: 'acceptEdits' })

  expect(await result).toEqual({ model: 'claude-haiku-4-5', agentId: 'ag-hidden' })
  expect(calls.sent).toEqual([
    { subagentType: ROW_TYPE, prompt: 'msgate run run-hook\n\nlinha 1\nlinha 2', permissionMode: 'default', description: 'review the diff', mode: 'acceptEdits' },
  ])
  expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
})

for (const [name, size] of [['5 kB', 5 * 1024], ['exactly the limit, 1 MiB', SHOWN_PROMPT_MAX_BYTES]] as const) {
  test(`a prompt of ${name} reaches the row whole`, async ($, on) => {
    const w = world(on, { backed: BACKED })
    const prompt = 'x'.repeat(size - 1) + '\n'
    w.respond = promptServer(prompt)
    await $.session.start(START)

    const { calls, result } = spawnAs(w, AGENTS)
    await result

    // Compared as a flag and a length: a failing diff of megabytes ends the run unreported.
    const sent: string = calls.sent[0].prompt
    expect(sent.length).toBe(prompt.length + 'msgate run run-hook\n\n'.length)
    expect(sent === 'msgate run run-hook\n\n' + prompt).toBe(true)
  })
}

test('a prompt of 3 MiB is not shown, and the row opens with the plain prompt', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = promptServer('x'.repeat(3 * 1024 * 1024 - 1) + '\n')
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS)

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(calls.sent.map((sent) => sent.prompt.slice(0, 60))).toEqual(['msgate run run-hook'])
  expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
})

test('a prompt the binary could not return whole is not shown, and the row opens with the plain prompt', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = (argv) => (argv[1] === 'prompt' ? ({ ...ok('the first 4 MiB of it'), isStdoutTruncated: true } as any) : promptServer('')(argv))
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS)

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(calls.sent.map((sent) => sent.prompt)).toEqual(['msgate run run-hook'])
  expect(w.debugLogs.join('\n')).toContain('the row of run-hook opens without the prompt of its run: the prompt is longer than the output msgate can return')
  expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
})

test('a prompt one byte over the limit is not shown, and the row opens with the plain prompt and is registered', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = promptServer('x'.repeat(SHOWN_PROMPT_MAX_BYTES + 1))
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS)

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  // Cut to a head: a failing diff of megabytes ends the run unreported.
  expect(calls.sent.map((sent) => sent.prompt.slice(0, 60))).toEqual(['msgate run run-hook'])
  expect(w.debugLogs.join('\n')).toContain('the row of run-hook opens without the prompt of its run: the prompt is longer than the 1048576 bytes a row can show')
  expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
})

test('a prompt whose characters are under the limit but whose UTF-8 bytes are over it is not shown', async ($, on) => {
  const w = world(on, { backed: BACKED })
  // 600000 characters of 2 bytes each: 1200000 bytes.
  const prompt = 'é'.repeat(600000)
  expect(prompt.length).toBeLessThan(SHOWN_PROMPT_MAX_BYTES)
  w.respond = promptServer(prompt)
  await $.session.start(START)

  const { calls, result } = spawnAs(w, AGENTS)

  expect(await result).toMatchObject({ agentId: 'ag-hidden' })
  expect(calls.sent.map((sent) => sent.prompt.slice(0, 60))).toEqual(['msgate run run-hook'])
  expect(w.debugLogs.join('\n')).toContain('the prompt is longer than the 1048576 bytes a row can show')
})

test('the spawn of another plugin\'s hidden row, of a row that is not hidden, and of one MSGATE_ROW_MODE=auto refuses read no prompt', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = promptServer('linha 1')
  w.env.set('MSGATE_ROW_MODE', 'auto')
  await $.session.start(START)
  const refused = spawnAs(w, AGENTS)
  expect(await refused.result).toEqual({ deny: AUTO_DENIAL })
  w.env.set('MSGATE_ROW_MODE', '')
  const other = spawnAs(w, 'p3')
  const notHidden = spawnAs(w, AGENTS, { subagentType: AGENTS + ':adhoc' })

  expect(await other.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(await notHidden.result).toMatchObject({ agentId: 'ag-hidden' })
  expect(w.runs).toEqual([])
  expect([...other.calls.sent, ...notHidden.calls.sent].map((sent) => sent.prompt)).toEqual(['msgate run run-hook', 'msgate run run-hook'])
})

// The text a hidden row's turn started with can be megabytes and no step of the
// row reads it, so the engine keeps none: a start text kept for the row would
// answer once the same agent id is a row the engine runs a job for.
test('the prompt a hidden row\'s turn started with is not kept', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  w.messages['ag-hidden'] = []
  await $.session.start(START)
  await startTurn($, 'ag-hidden', 'msgate run run-hook\n\n' + 'x'.repeat(4096))
  w.state.set('backedTypes', { value: { [ROW_TYPE]: { command: 'exec', promptMode: 'free' } }, version: 2 })

  const out = await step($, 'ag-hidden')

  expect(out.result.toolUses[0].input.message).toContain('the prompt is empty')
  expect(w.runs).toEqual([])
})

// The host refuses a prompt that has a body, the way `refuse` says: it throws or answers with a denial.
const REFUSALS: [string, () => any][] = [
  ['throws', () => { throw new Error('the prompt is too long') }],
  ['denies it', () => ({ deny: 'the prompt is too long' })],
]
for (const [how, refuse] of REFUSALS) {
  test(`a host that ${how} the full prompt gets the plain prompt once, the row opens and is registered, and the reason is logged`, async ($, on) => {
    const w = world(on, { backed: BACKED })
    w.respond = promptServer('linha 1')
    await $.session.start(START)
    const sent: string[] = []
    const next = async (call: any) => {
      sent.push(call.prompt)
      return call.prompt.includes('\n') ? refuse() : { model: 'claude-haiku-4-5', agentId: 'ag-hidden' }
    }

    const result = await admitSpawn(withEnv(w), { subagentType: ROW_TYPE, prompt: 'msgate run run-hook', permissionMode: 'default' }, AGENTS, next)

    expect(result).toEqual({ model: 'claude-haiku-4-5', agentId: 'ag-hidden' })
    expect(sent).toEqual(['msgate run run-hook\n\nlinha 1', 'msgate run run-hook'])
    expect(w.debugLogs.join('\n')).toContain('the host refused the full prompt of a hidden row, which opens with the plain one: the prompt is too long')
    expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
  })
}

test('a host that refuses the plain prompt too is not asked a third time, and its answer is the answer of the spawn', async ($, on) => {
  const w = world(on, { backed: BACKED })
  w.respond = promptServer('linha 1')
  await $.session.start(START)
  const sent: string[] = []
  const next = async (call: any) => {
    sent.push(call.prompt)
    return { deny: 'no agents now' }
  }

  const result = await admitSpawn(withEnv(w), { subagentType: ROW_TYPE, prompt: 'msgate run run-hook', permissionMode: 'default' }, AGENTS, next)

  expect(result).toEqual({ deny: 'no agents now' })
  expect(sent).toHaveLength(2)
  expect(hiddenRows(w)).toBeUndefined()
})

const PROMPT_UNREAD: [string, (argv: string[]) => any, string][] = [
  ['the run is of another session', promptServerOf({ status: ok(JSON.stringify(flagged({ session_id: 'sess-other' }))) }), 'the engine does not answer for the run'],
  ['the run is unknown', promptServerOf({ status: { exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' } }), 'the engine does not answer for the run'],
  ['the status cannot be read', promptServerOf({ status: { exitCode: 1, stdout: '', stderr: 'msgate: store is locked\n' } }), 'store is locked'],
  ['the prompt command fails', promptServerOf({ prompt: { exitCode: 70, stdout: '', stderr: 'msgate: prompt unreadable\n' } }), 'prompt unreadable'],
  ['the prompt is empty', promptServerOf({ prompt: ok('') }), 'the prompt is empty'],
  ['the prompt is blank', promptServerOf({ prompt: ok(' \n\n') }), 'the prompt is empty'],
  [
    'the prompt command cannot run',
    promptServerOf({
      prompt: () => {
        throw new Error('spawn failed')
      },
    }),
    'spawn failed',
  ],
]
for (const [name, respond, reason] of PROMPT_UNREAD) {
  test(`when ${name}, the row opens with the plain prompt, is registered, and the reason is logged`, async ($, on) => {
    const w = world(on, { backed: BACKED })
    w.respond = respond
    await $.session.start(START)

    const { calls, result } = spawnAs(w, AGENTS)

    expect(await result).toMatchObject({ agentId: 'ag-hidden' })
    expect(calls.sent.map((sent) => sent.prompt)).toEqual(['msgate run run-hook'])
    expect(w.debugLogs.join('\n')).toContain('the row of run-hook opens without the prompt of its run: ')
    expect(w.debugLogs.join('\n')).toContain(reason)
    expect(hiddenRows(w)).toEqual({ 'ag-hidden': 'run-hook' })
  })
}
