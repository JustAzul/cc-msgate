// Shared stubs for the mod tests: a fake world beneath the mod where the
// msgate binary, the store, the clock and the display calls are all in memory.
import { mock } from 'claude-code/testing'
import { createVersionCache, serveBackType, serveFlaggedRuns, serveJobs, serveRun, serveVersion } from '../hooks/iface.js'

export const SESSION = 'sess-1'
// The mod runs the plugin's own shim: an absolute path ending in bin/msgate.
export const isShim = (p: string) => p.startsWith('/') && p.endsWith('/bin/msgate')
export const TOOL = (name: string) => `mcp__msgate__${name}`

export type Run = { argv: string[]; stdin?: string; cwd?: string; timeoutMs?: number }
export type SubmitOutcome = 'accept' | 'drop' | 'throw'
export type Reply = { exitCode: number; stdout: string; stderr: string }
export type Responder = (argv: string[], init: any) => Reply | Promise<Reply>

// The price refresh the mod runs on its own schedule: `<shim> prices refresh …`.
export const isPrices = (argv: readonly string[]) => argv[1] === 'prices'
export const pricesRuns = (w: { runs: Run[] }) => w.runs.filter((r) => isPrices(r.argv))
export const otherRuns = (w: { runs: Run[] }) => w.runs.filter((r) => !isPrices(r.argv))

export function ok(stdout: string): Reply {
  return { exitCode: 0, stdout, stderr: '' }
}

export function job(over: Record<string, unknown> = {}) {
  return {
    run_id: 'run-1',
    conversation_id: 'conv-1',
    state: 'running',
    outcome: null,
    purpose: 'adhoc',
    background: true,
    source: 'mod',
    output_path: '/state/runs/run-1/output.md',
    admitted_at: '2026-01-01T00:00:00Z',
    ...over,
  }
}

export function listing(...runs: unknown[]) {
  return ok(JSON.stringify({ runs }))
}

// The delivery queue of a session lives in the store under one key per session.
export const queueKey = (sessionId = SESSION) => `delivery:${sessionId}`
export const DAY_MS = 86_400_000

export function queued(runId: string, over: Record<string, unknown> = {}) {
  return { run_id: runId, conversation_id: 'conv-1', purpose: null, admitted_at: 1_000, attempted_at: null, ...over }
}

// A store seeded with one session's queue.
export function seeded(entries: unknown[], sessionId = SESSION): Record<string, unknown> {
  return { [queueKey(sessionId)]: entries }
}

// Answers the reads of the delivery: `status <run_id> --json` from the given
// runs (exit 4, run not found, for any other id), `result` with the output, and
// an empty listing for the session.
export function deliveries(runs: Record<string, unknown>, output = 'out'): Responder {
  return (argv) => {
    if (argv[1] === 'result') return ok(output)
    if (argv[1] !== 'status') return listing()
    if (argv[2] === '--json') return listing()
    const run = runs[argv[2]!]
    return run === undefined ? { exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' } : ok(JSON.stringify(run))
  }
}

// The reads of one run's status the delivery made.
export const statusReads = (w: { runs: Run[] }) => w.runs.filter((r) => r.argv[1] === 'status' && r.argv[2] !== '--json').map((r) => r.argv[2]!)

export const NO_USAGE = { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }

// The completions a model can answer with.
export function answered(text: string) {
  return { isAnswered: true, text, usage: { ...NO_USAGE, input_tokens: 458, output_tokens: 18 } }
}
export function apiError(status: number | null) {
  return { isAnswered: false, reason: 'api-error', status, error: status === 429 ? 'rate_limit' : 'unknown', usage: NO_USAGE }
}
export const emptyReply = () => ({ isAnswered: false, reason: 'empty-reply', usage: { ...NO_USAGE, input_tokens: 458, output_tokens: 2 } })

// The types a plugin that backs agent types has backed by session start: the
// engine answers the rows of these. A row test works with them unless it names
// others through the `backed` option of `world`.
export const AGENTS = 'msgate-agents'
export const PROFILES: Record<string, Record<string, unknown>> = {
  [AGENTS + ':second-opinion']: { command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'high', sandbox: 'read-only' },
  [AGENTS + ':code-review']: { command: 'review', promptMode: 'review-target', model: 'gpt-6.1-sol', effort: 'high', sandbox: 'read-only' },
  [AGENTS + ':cross-check']: { command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'high', sandbox: 'read-only' },
  [AGENTS + ':expert-persona']: { command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'xhigh', sandbox: 'read-only' },
  [AGENTS + ':delegation']: { command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'medium', sandbox: 'workspace-write' },
  [AGENTS + ':adhoc']: { command: 'exec', promptMode: 'free' },
}

// world registers every stub a test needs. Call it before the first call on $.
export function world(on: any, opts: { store?: Record<string, unknown>; backed?: Record<string, Record<string, unknown>> } = {}) {
  const w = {
    clock: mock.clock(on),
    runs: [] as Run[],
    toasts: [] as string[],
    logs: [] as string[],
    // The texts sent to the debug log alone.
    debugLogs: [] as string[],
    // Every event name the plugin asked to draw again.
    invalidations: [] as string[],
    statuses: [] as (string | undefined)[],
    // Every notice the mod tried to submit, an attempt that was dropped or threw included.
    submits: [] as string[],
    // How a submission ends: the prompt enters, is refused, or the call throws.
    // A promise holds the submission pending until the test settles it.
    submitOutcome: (() => 'accept') as (text: string) => SubmitOutcome | Promise<SubmitOutcome>,
    tools: [] as any[],
    commands: [] as any[],
    store: new Map<string, unknown>(Object.entries(opts.store ?? {})),
    storeWrites: [] as { key: string; value: unknown }[],
    // How many of the next store writes fail; Infinity fails them all.
    storeSetFailures: 0,
    // While set, a store write that does not fail waits for it before it lands.
    storeSetHold: null as null | Promise<void>,
    sessionId: SESSION,
    refuseCommand: false,
    // The named values of the session's state the interface reads and writes,
    // each with its version.
    state: new Map<string, { value: unknown; version: number }>(),
    // How many of the next writes of those values fail.
    stateWriteFailures: 0,
    // The effort of each agent an Agent call asked for, by agent id: the state
    // family the mod keeps, a consumed member back at null.
    agentEfforts: new Map<string, string | null>(),
    // The Agent calls that reached the bottom and wait for the test to end them.
    // The agent id of every read of the efforts state, in order.
    effortReads: [] as string[],
    // While set, a read of the efforts state answers only after it settles, with
    // the value it held when the read was made.
    effortReadHold: null as null | Promise<void>,
    heldAgentCalls: [] as { toolUseId: string; end: () => void }[],
    onAgentCall: null as null | (() => void),
    // What a process the mod runs answers: the call is kept in `runs`.
    processRun: async (argv: string[], init?: any): Promise<Reply> => {
      w.runs.push({ argv: [...argv], stdin: init?.stdin, cwd: init?.cwd, timeoutMs: init?.timeoutMs })
      const answer = isPrices(argv) ? w.respondPrices : w.respond
      return answer([...argv], init ?? {})
    },
    respond: ((argv: string[]) => {
      throw new Error('unexpected msgate call: ' + argv.join(' '))
    }) as Responder,
    // What `prices refresh` answers; by default a cache that is still fresh.
    respondPrices: (() => ok(JSON.stringify({ sdk_status: 'ok', exit_code: 0, ran: false, changed: false, reason: 'fresh' }))) as Responder,
    agents: [] as { id: string; type: string }[],
    messages: {} as Record<string, { role: 'user' | 'assistant'; text: string }[]>,
    // The agent id of every model request that reached the model beneath the
    // plugin; undefined for the main loop.
    modelSteps: [] as (string | undefined)[],
    spawns: [] as any[],
    // What a spawn answers; by default the agent starts.
    spawnAnswer: null as null | ((e: any) => any),
    // The surfaces the session draws on; none by default, so a test is headless
    // until it opts in with ['terminal'].
    surfaces: [] as string[],
    // Every completion that reached the model beneath the plugin, as the
    // request named it.
    modelRequests: [] as any[],
    // What a completion answers; by default a label.
    respondModel: ((): any => answered('Reading auth.go')) as (request: any) => any,
    // The environment variables the session has set, by name; an unset name reads
    // as undefined.
    env: new Map<string, string>(),
    // While set, reading the environment throws.
    envFails: false,
  }
  w.state.set('backedTypes', { value: opts.backed ?? PROFILES, version: 1 })
  on('session.start', () => ({ cwd: '/work' }))
  on('session.id', () => ({ value: w.sessionId }))
  on('tool.register', (_$: any, e: any) => {
    w.tools.push(e)
    return { value: { tool: 'mcp__msgate__' + e.name } }
  })
  on('command.register', (_$: any, e: any) => {
    if (w.refuseCommand) return { deny: '"/' + e.name + '" refused: it is the built-in /' + e.name }
    w.commands.push(e)
    return { value: { command: e.name } }
  })
  on('ui.toast', (_$: any, e: any) => {
    w.toasts.push(e.text)
    return { value: undefined }
  })
  on('ui.log', (_$: any, e: any) => {
    w.logs.push(e.text)
    if (e.to === 'debug') w.debugLogs.push(e.text)
    return { value: undefined }
  })
  on('ui.status', (_$: any, e: any) => {
    w.statuses.push(e.text)
    return { value: undefined }
  })
  on('prompt.submit', async (_$: any, e: any) => {
    w.submits.push(e.text)
    const outcome = await w.submitOutcome(e.text)
    if (outcome === 'throw') throw new Error('the prompt could not be submitted')
    return outcome === 'drop' ? { drop: 'busy' } : { text: e.text }
  })
  on('store.get', (_$: any, e: any) => ({ value: w.store.get(e.key) }))
  on('store.set', async (_$: any, e: any) => {
    if (w.storeSetFailures > 0) {
      w.storeSetFailures -= 1
      throw new Error('the store is unavailable')
    }
    if (w.storeSetHold !== null) await w.storeSetHold
    w.store.set(e.key, JSON.parse(JSON.stringify(e.value)))
    w.storeWrites.push({ key: e.key, value: e.value })
    return { value: undefined }
  })
  on('store.delete', (_$: any, e: any) => {
    w.store.delete(e.key)
    return { value: undefined }
  })
  on('store.keys', () => ({ value: [...w.store.keys()] }))
  on('process.run', async (_$: any, e: any) => ({ value: await w.processRun(e.argv, e.init) }))
  // The backed types of the interface are the one state value the world holds:
  // the interface methods (driven through msgateAs) write it and the rows' hooks
  // read it, so a test can back a type and then run a row of it.
  for (const key of ['backedTypes', 'hiddenRows']) {
    on('state.get', { plugin: 'msgate', key }, () => ({ value: { value: w.state.get(key)?.value, version: w.state.get(key)?.version ?? 0 } }))
    on('state.set', { plugin: 'msgate', key }, (_$: any, e: any) => ({ value: stateWrite(w.state, key, e.value, e.ifVersion) }))
  }
  // The efforts are a family: a member is told by its id.
  on('state.get', { plugin: 'msgate', key: 'agentEfforts' }, async (_$: any, e: any) => {
    w.effortReads.push(e.id)
    const read = { value: { value: w.agentEfforts.get(e.id), version: w.agentEfforts.has(e.id) ? 1 : 0 } }
    if (w.effortReadHold !== null) await w.effortReadHold
    return read
  })
  on('state.set', { plugin: 'msgate', key: 'agentEfforts' }, (_$: any, e: any) => {
    w.agentEfforts.set(e.id, e.value)
    return { value: { isSet: true, version: 1 } }
  })
  // The compaction beneath the plugin: the engine's own, which keeps nothing.
  on('session.compact', () => ({ messages: [{ role: 'user', text: 'a summary', toolUses: [] }] }))
  on('agent.list', () => ({ value: w.agents.map((a) => ({ ...a, description: '', status: 'running' })) }))
  on('agent.spawn', (_$: any, e: any) => {
    w.spawns.push(e)
    if (w.spawnAnswer) return w.spawnAnswer(e)
    const agentId = 'ag-spawned-' + w.spawns.length
    w.agents.push({ id: agentId, type: e.subagentType ?? e.subagent_type })
    return { model: 'claude-haiku-4-5', agentId }
  })
  on('session.messages', (_$: any, e: any) => {
    const rows = w.messages[e.agentId]
    return { value: rows ?? { deny: 'no agent ' + e.agentId } }
  })
  on('turn.step', async function* (_$: any, e: any) {
    w.modelSteps.push(e.agentId)
    yield { kind: 'text', index: 0, text: 'a Claude model answered' }
    yield { kind: 'stop', stopReason: 'end_turn', usage: null }
    return { turnId: e.turnId, index: e.index, answer: 'a Claude model answered', toolUses: [], stopReason: 'end_turn', usage: null }
  })
  // The engine's own turn start: it opens the turn the steps then run in.
  on('turn.start', (_$: any, e: any) => ({ turnId: e.turnId }))
  on('session.surfaces', () => ({ value: w.surfaces }))
  on('env.get', (_$: any, e: any) => {
    if (w.envFails) throw new Error('the environment is unavailable')
    return { value: w.env.get(e.name) }
  })
  on('model.complete', async (_$: any, e: any) => {
    w.modelRequests.push(e)
    return { value: await w.respondModel(e) }
  })
  // Anything the mod does not answer itself falls through to here. An Agent call
  // is held at the bottom until the test ends it: the test hooks cannot spawn,
  // so the test raises the spawn the engine would nest in the call (see agentCall).
  on('tool.call', async (_$: any, e: any) => {
    if (e.tool !== 'Agent') return { result: 'unanswered' }
    await new Promise<void>((end) => {
      w.heldAgentCalls.push({ toolUseId: e.tool_use_id, end })
      w.onAgentCall?.()
    })
    return { result: 'the agent ended' }
  })
  on('ui.invalidate', (_$: any, e: any) => {
    w.invalidations.push(e.event)
    return { value: undefined }
  })
  // The engine's own spinner: the message while one overrides the word.
  on('ui.render', { component: 'Spinner' }, ($: any, e: any) => {
    const { Text } = $.ui.resolve(e)
    return h(Text, {}, e.props.message ?? e.props.word)
  })
  // The engine's own band above the prompt: a survey while one holds it,
  // otherwise empty.
  on('ui.render', { component: 'AbovePrompt' }, ($: any, e: any) => {
    const { Box, Text } = $.ui.resolve(e)
    return e.props.hasSurvey ? h(Box, {}, h(Text, {}, 'How is Claude doing this session?')) : h(Box, {})
  })
  return w
}

// step raises one model request in the loop of `agentId` (the main loop when
// undefined) and reads the whole response. A later step of the same turn, or a
// new turn, names its `turnId` and `index`.
export async function step($: any, agentId?: string, { turnId = 'turn-1', index = 0 }: { turnId?: string; index?: number } = {}) {
  const { chunks, result } = await drain($.turn.step({ turnId, index, model: 'claude-haiku-4-5', messageCount: 1, agentId }))
  return { chunks, result, text: answerText(chunks, result) }
}

// readStep raises a model request as step does but hands the stream to the
// test: it reads one chunk at a time and can stop the step between two, as the
// host does when the turn is interrupted. The host's abort signal reaches the
// hook only when the stream is next at a chunk, so a stop cannot land while the
// hook awaits something.
export function readStep($: any, agentId?: string, { turnId = 'turn-1', index = 0 }: { turnId?: string; index?: number } = {}) {
  const stream = $.turn.step({ turnId, index, model: 'claude-haiku-4-5', messageCount: 1, agentId })
  return {
    next: async (): Promise<{ done: boolean; value: any }> => stream.next(),
    stop: async () => void (await stream.return(undefined)),
  }
}

// agentCall raises an Agent call for `type` and holds it open, as the engine does
// while the agent it spawns runs. `spawn` raises the `agent.spawn` the engine
// nests in the call, carrying the call's id; `end` ends the call.
export async function agentCall($: any, w: ReturnType<typeof world>, type: string, over: Record<string, unknown> = {}) {
  const arrived = new Promise<void>((resolve) => {
    w.onAgentCall = resolve
  })
  const settled = $.tool.call({ tool: 'Agent', description: 'a task', prompt: 'q', subagent_type: type, ...over })
  await arrived
  const held = w.heldAgentCalls.at(-1)!
  return {
    toolUseId: held.toolUseId,
    spawn: (prompt = 'q', more: Record<string, unknown> = {}) =>
      $.agent.spawn({ prompt, description: 'a task', subagentType: type, tool_use_id: held.toolUseId, ...more }),
    end: async () => {
      held.end()
      await settled
    },
  }
}

// startTurn raises the start of one turn in the loop of `agentId`, with the
// prompt the run begins with.
export async function startTurn($: any, agentId: string, text: string, turnId = 'turn-1') {
  await $.turn.start({ text, turnId, agentId })
}

// drain reads a stream by hand: the step's result is the stream's return
// value, which a for-await loop drops.
async function drain(stream: any) {
  const chunks: any[] = []
  let next = await stream.next()
  while (!next.done) {
    chunks.push(next.value)
    next = await stream.next()
  }
  return { chunks, result: next.value }
}

// The answer as the caller reads it: the message of the SubagentHandback call
// that delivers an msgate subagent's answer, or else the streamed text of the
// last block, since the recorded summaries stream as text ahead of the answer.
function answerText(chunks: any[], result: any): string {
  const handback = result?.toolUses?.find((use: any) => use.name === 'SubagentHandback')
  if (handback) return String(handback.input.message)
  const texts = chunks.filter((c) => c.kind === 'text')
  return texts.length === 0 ? '' : texts.at(-1).text
}

export const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }

export const msg = (text: string) => ({ kind: 'message', text })
export const cmd = (text: string) => ({ kind: 'command', text })

// codex is a run that works through `slices`: each wait times out and the
// progress read after it answers the next slice's entries; the wait after the
// last slice finds the run done.
export function codex(slices: { kind: string; text: string }[][]) {
  let waits = 0
  let reads = 0
  let next = 0
  return (argv: string[]) => {
    switch (argv[1]) {
      case 'exec':
        return ok(JSON.stringify({ conversation_id: 'conv-1', run_id: 'run-1' }))
      case 'wait':
        return ++waits <= slices.length ? WAIT_TIMEOUT : ok(JSON.stringify({ run_id: 'run-1', state: 'done', outcome: 'ok' }))
      case 'progress': {
        const entries = slices[reads++] ?? []
        next += entries.length
        return ok(JSON.stringify({ run_id: 'run-1', state: 'running', next, entries: entries.map((e, i) => ({ seq: next - entries.length + i, at: 't', ...e })) }))
      }
      case 'status':
        return ok(JSON.stringify({ run_id: 'run-1', conversation_id: 'conv-1', state: 'done', outcome: 'ok', output_path: '/o' }))
      default:
        return ok('Codex: done.')
    }
  }
}

// The toasts that are not spawn notices.
export const withoutSpawnNotices = (toasts: string[]) => toasts.filter((text) => !text.startsWith('msgate · spawned '))

export const POLL_MS = 15000

// A write to a versioned value, as the engine's $.state.set answers it.
function stateWrite(state: Map<string, { value: unknown; version: number }>, key: string, value: unknown, ifVersion?: number) {
  const held = state.get(key)
  if (ifVersion !== undefined && ifVersion !== (held?.version ?? 0)) return { isSet: false, version: held?.version ?? 0 }
  const version = (held?.version ?? 0) + 1
  state.set(key, { value: JSON.parse(JSON.stringify(value)), version })
  return { isSet: true, version }
}

// The engine's `$` as the interface methods use it, over the world. The test's
// own `$` carries only the core nouns, so a method that needs the process,
// state or session nouns is handed this one.
export function engineOf(w: ReturnType<typeof world>) {
  return {
    plugin: { name: 'msgate', root: '/plugin' },
    session: { id: async () => w.sessionId },
    process: { run: (argv: string[], init?: any) => w.processRun(argv, init) },
    state: {
      get: async (ref: { key: string }) => ({ value: w.state.get(ref.key)?.value, version: w.state.get(ref.key)?.version ?? 0 }),
      set: async (ref: { key: string }, value: unknown, options?: { ifVersion?: number }) => {
        if (w.stateWriteFailures > 0) {
          w.stateWriteFailures -= 1
          throw new Error('the state is unavailable')
        }
        return stateWrite(w.state, ref.key, value, options?.ifVersion)
      },
    },
    ui: { log: async (text: string) => void w.debugLogs.push(text) },
  }
}

// A second plugin calling the engine's interface: the call carries the plugin
// the host stamps on it (`next.origin.plugin`), which the test's `$` cannot, so
// the methods are driven with the caller named. `versions` is what one load has
// read of the engine's version; a fresh one stands for the module loading again.
export const CALLER = 'p2'
export const OTHER_CALLER = 'p3'
export function msgateAs(w: ReturnType<typeof world>, caller: string = CALLER, versions = createVersionCache()) {
  const $ = engineOf(w)
  return {
    backType: (args: unknown) => serveBackType($, args, caller),
    version: () => serveVersion($, versions),
    run: (args: unknown) => serveRun($, args, caller),
    jobs: (args: unknown) => serveJobs($, args, caller),
    flaggedRuns: () => serveFlaggedRuns($),
  }
}
