import { expect, test } from 'claude-code/testing'
import { AGENTS, SESSION, WAIT_TIMEOUT, answered, apiError, codex, job, msg, ok, readStep, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ROW_TYPE = AGENTS + ':run'
const STEPPED = { [ROW_TYPE]: { command: 'follow', promptMode: 'run-id', stepped: true } }
const PLAIN = { [ROW_TYPE]: { command: 'follow', promptMode: 'run-id' } }
const PROGRESS = 'mcp__msgate__progress'
const isRecordRequest = (request: any) => request.maxTokens === 120

const finished = () =>
  job({ run_id: 'run-hook', source: 'hook-stop', purpose: 'code-review', background: false, session_id: SESSION, agent_feedback: true, state: 'done', outcome: 'ok', started_at: '1970-01-01T00:00:00Z' })

// A hook's run that works through `slices`: each wait times out and the
// progress read after it answers the next slice's entries; the wait after the
// last slice finds the run done. Entries are numbered from 1 across the slices.
function follower(slices: number[]) {
  let waits = 0
  let reads = 0
  let total = 0
  return (argv: string[]) => {
    switch (argv[1]) {
      case 'wait':
        return ++waits <= slices.length ? WAIT_TIMEOUT : ok(JSON.stringify(finished()))
      case 'progress': {
        const count = slices[reads++] ?? 0
        const entries = Array.from({ length: count }, (_, i) => ({ seq: total + i, at: 't', kind: 'message', text: 'entry ' + (total + i + 1) }))
        total += count
        return ok(JSON.stringify({ run_id: 'run-hook', state: 'running', next: total, entries }))
      }
      case 'status':
        return ok(JSON.stringify(finished()))
      default:
        return ok('')
    }
  }
}

// A model that labels each window in turn and records a sentence for each
// recorded window.
function labelling() {
  let labels = 0
  return (request: any) => (isRecordRequest(request) ? answered('Covered a window.') : answered('Label ' + ++labels))
}

// A model that labels each window in turn and records nothing: its record
// requests fail.
function labellingWithoutRecords() {
  let labels = 0
  return (request: any) => (isRecordRequest(request) ? apiError(500) : answered('Label ' + ++labels))
}

async function openRow($: any, w: ReturnType<typeof world>, slices: number[], model: (request: any) => any = labelling()) {
  w.surfaces = ['terminal']
  w.respondModel = model
  w.respond = follower(slices)
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  w.messages['ag-hidden'] = [{ role: 'user', text: 'msgate run run-hook' }]
  w.state.set('hiddenRows', { value: { 'ag-hidden': 'run-hook' }, version: 1 })
  await $.session.start(START)
}

// The step of the row's turn at `index`.
const stepAt = ($: any, index: number) => step($, 'ag-hidden', { turnId: 'turn-1', index })

const textsOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'text').map((c) => c.text)
const toolsOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'tool')
// The block indexes a step wrote besides the thinking block, which grows by deltas.
const blocksOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'text' || c.kind === 'tool').map((c) => c.index)

test('a stepped row follows a run of nine entries in three steps that end in the progress marker, then answers', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3], labellingWithoutRecords())

  const steps = [await stepAt($, 0), await stepAt($, 1), await stepAt($, 2)]
  const last = await stepAt($, 3)

  for (const [i, out] of steps.entries()) {
    expect(textsOf(out.chunks)).toEqual(['» Label ' + (i + 1) + '\n'])
    expect(out.chunks.at(-3)).toEqual({ kind: 'tool', index: 2, id: 'msgate-progress-turn-1-' + i, name: PROGRESS })
    expect(out.chunks.at(-2)).toEqual({ kind: 'input', index: 2, json: '{}' })
    expect(out.chunks.at(-1)).toEqual({ kind: 'stop', stopReason: 'tool_use', usage: null })
    expect(out.result).toEqual({ turnId: 'turn-1', index: i, answer: '» Label ' + (i + 1) + '\n', toolUses: [{ name: PROGRESS, input: {} }], stopReason: 'tool_use', usage: null })
    expect(blocksOf(out.chunks)).toEqual([1, 2])
  }
  expect(textsOf(last.chunks).at(-1)).toBe('msgate run run-hook ended ok')
  expect(toolsOf(last.chunks)).toEqual([])
  expect(last.result.stopReason).toBe('end_turn')
})

test('a row that is not stepped follows the same run in one step, with no marker', async ($, on) => {
  const w = world(on, { backed: PLAIN })
  await openRow($, w, [3, 3, 3])

  const out = await stepAt($, 0)

  expect(toolsOf(out.chunks)).toEqual([])
  expect(textsOf(out.chunks).at(-1)).toBe('msgate run run-hook ended ok')
  expect(out.result.stopReason).toBe('end_turn')
})

test('a recorded sentence that is ready in the middle of the run is shown by the next step, the ones at its end by the last, before the answer', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3, 3, 3])

  const steps = [await stepAt($, 0), await stepAt($, 1), await stepAt($, 2), await stepAt($, 3), await stepAt($, 4)]
  const last = await stepAt($, 5)

  expect(steps.map((out) => textsOf(out.chunks))).toEqual([
    ['» Label 1\n'],
    ['» Label 2\n'],
    ['» Label 3\n', '» 1–9: Covered a window.\n'],
    ['» Label 4\n'],
    ['» Label 5\n'],
  ])
  expect(textsOf(last.chunks)).toEqual(['» 10–15: Covered a window.\n', 'msgate run run-hook ended ok'])
})

test('a run that ends before a window is full is answered in the first step', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [2])

  const out = await stepAt($, 0)

  expect(toolsOf(out.chunks)).toEqual([])
  expect(textsOf(out.chunks).at(-1)).toBe('msgate run run-hook ended ok')
})

test('with the summaries off a stepped row answers in one step and shows the raw entries as thinking', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  w.env.set('MSGATE_SUMMARIES', '0')
  await openRow($, w, [3, 3, 3])

  const out = await stepAt($, 0)

  expect(toolsOf(out.chunks)).toEqual([])
  expect(out.chunks.filter((c) => c.kind === 'thinking').map((c) => c.text)).toEqual(['entry 1\n', 'entry 2\n', 'entry 3\n', 'entry 4\n', 'entry 5\n', 'entry 6\n', 'entry 7\n', 'entry 8\n', 'entry 9\n'])
  expect(textsOf(out.chunks).at(-1)).toBe('msgate run run-hook ended ok')
})

test('a turn already in steps finishes in the step it is in when the summaries are switched off', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])
  const first = await stepAt($, 0)

  w.env.set('MSGATE_SUMMARIES', '0')
  const second = await stepAt($, 1)

  expect(toolsOf(first.chunks)).toHaveLength(1)
  expect(toolsOf(second.chunks)).toEqual([])
  expect(textsOf(second.chunks).at(-1)).toBe('msgate run run-hook ended ok')
  expect(debugOf(w)).toEqual([])
})

test('a headless session answers a stepped row in one step', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])
  w.surfaces = []

  const out = await stepAt($, 0)

  expect(toolsOf(out.chunks)).toEqual([])
  expect(textsOf(out.chunks).at(-1)).toBe('msgate run run-hook ended ok')
})

test('a stepped row answers in one step, with no tool call, when the progress tool did not register in this load', async ($, on) => {
  on('tool.register', { name: 'progress' } as any, () => {
    throw new Error('the host refused the tool')
  })
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])

  const out = await stepAt($, 0)

  expect(toolsOf(out.chunks)).toEqual([])
  expect(out.result.stopReason).toBe('end_turn')
  expect(textsOf(out.chunks).at(-1)).toBe('msgate run run-hook ended ok')
})

const ENDED = { answer: '', durationMs: 1, isAborted: true, reason: 'aborted' } as const
const debugOf = (w: ReturnType<typeof world>) => w.debugLogs.filter((text) => text.includes('found no follow'))

test('a turn that ends between two steps lets go of its follow, so a step that comes after finds none and starts a new one', async ($, on) => {
  on('turn.complete', (_$: any, e: any) => ({ text: e.answer }))
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])
  await stepAt($, 0)
  expect(debugOf(w)).toEqual([])

  await $.turn.complete({ turnId: 'turn-1', agentId: 'ag-hidden', ...ENDED })
  const out = await stepAt($, 1)

  expect(debugOf(w)).toEqual(['agent ag-hidden found no follow at step 1 of turn turn-1: a new one starts'])
  expect(toolsOf(out.chunks)).toHaveLength(1)
})

test('a hidden row whose follow is lost reads the entries over and goes on in steps', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])

  const out = await stepAt($, 2)

  expect(debugOf(w)).toHaveLength(1)
  expect(textsOf(out.chunks)).toEqual(['» Label 1\n'])
  expect(toolsOf(out.chunks)).toEqual([{ kind: 'tool', index: 2, id: 'msgate-progress-turn-1-2', name: PROGRESS }])
})

test('a turn interrupted while a step waits starts no more waits and lets go of its follow', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [1, 0, 0, 0])
  const reading = readStep($, 'ag-hidden', { turnId: 'turn-1', index: 0 })

  // The opening line, then the label, which the second slice shows.
  expect((await reading.next()).value).toMatchObject({ kind: 'thinking', text: 'msgate · run\n' })
  expect((await reading.next()).value).toMatchObject({ kind: 'thinking', text: '» Label 1\n' })
  await reading.stop()
  const waits = w.runs.filter((r) => r.argv[1] === 'wait').length
  await stepAt($, 1)

  expect(waits).toBe(2)
  expect(debugOf(w)).toHaveLength(1)
})

const OWN_TYPE = AGENTS + ':delegation'
const OWN = { [OWN_TYPE]: { command: 'exec', promptMode: 'free', stepped: true } }

// A row of an agent type that starts its own run, which works through
// `windows` windows of three entries.
async function openOwnRow($: any, w: ReturnType<typeof world>, windows: number) {
  w.surfaces = ['terminal']
  w.respondModel = labellingWithoutRecords()
  w.respond = codex(Array.from({ length: windows }, (_, i) => [msg('entry ' + (3 * i + 1)), msg('entry ' + (3 * i + 2)), msg('entry ' + (3 * i + 3))]))
  w.agents.push({ id: 'ag-own', type: OWN_TYPE })
  w.messages['ag-own'] = [{ role: 'user', text: 'Review the cache.' }]
  await $.session.start(START)
}

const ownStep = ($: any, index: number) => step($, 'ag-own', { turnId: 'turn-1', index })
const commandsOf = (w: ReturnType<typeof world>) => w.runs.map((r) => r.argv[1]).filter((command) => command !== 'eta')

test('a stepped row that starts its own run admits it once, ends each window in the progress marker and hands the answer back in the last step', async ($, on) => {
  const w = world(on, { backed: OWN })
  await openOwnRow($, w, 2)

  const first = await ownStep($, 0)
  const second = await ownStep($, 1)
  const last = await ownStep($, 2)

  expect([first, second].map((out) => out.result.toolUses.map((use: any) => use.name))).toEqual([[PROGRESS], [PROGRESS]])
  expect(last.result.toolUses.map((use: any) => use.name)).toEqual(['SubagentHandback'])
  expect(last.text).toContain('Codex: done.')
  expect(commandsOf(w).filter((command) => command === 'exec')).toHaveLength(1)
  expect(w.modelSteps).toEqual([])
})

test('the steps that end in the progress marker do not count toward the step limit, so no step of a long run reaches the model beneath', async ($, on) => {
  const w = world(on, { backed: OWN })
  await openOwnRow($, w, 12)

  const steps = []
  for (let index = 0; index <= 12; index += 1) steps.push(await ownStep($, index))

  expect(steps.slice(0, 12).every((out) => out.result.toolUses[0].name === PROGRESS)).toBe(true)
  expect(steps[12]!.result.toolUses[0].name).toBe('SubagentHandback')
  expect(w.modelSteps).toEqual([])
})

test('a stepped row that finds its follow lost after the first step fails the hand-off instead of starting its run again', async ($, on) => {
  const w = world(on, { backed: OWN })
  await openOwnRow($, w, 2)

  const out = await ownStep($, 1)

  expect(out.text).toContain('no longer followed')
  expect(commandsOf(w)).not.toContain('exec')
})

test('a turn that ends interrupted between two steps cancels the run its row started', async ($, on) => {
  on('turn.complete', (_$: any, e: any) => ({ text: e.answer }))
  const w = world(on, { backed: OWN })
  await openOwnRow($, w, 3)
  await ownStep($, 0)

  await $.turn.complete({ turnId: 'turn-1', agentId: 'ag-own', ...ENDED })

  expect(w.runs.filter((r) => r.argv[1] === 'cancel').map((r) => r.argv[2])).toEqual(['run-1'])
})
