import { expect, test } from 'claude-code/testing'
import { AGENTS, SESSION, WAIT_TIMEOUT, answered, apiError, codex, job, msg, ok, readStep, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ROW_TYPE = AGENTS + ':run'
const BACKED = { [ROW_TYPE]: { command: 'follow', promptMode: 'run-id' } }
const isRecordRequest = (request: any) => request.maxTokens === 120

const finished = () =>
  job({ run_id: 'run-hook', source: 'hook-stop', purpose: 'code-review', background: false, session_id: SESSION, agent_feedback: true, state: 'done', outcome: 'ok', started_at: '1970-01-01T00:00:00Z' })

// follower is a hook's run that works through `slices`: each wait times out and
// the progress read after it answers the next slice's entries; the wait after the
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

// A model that answers a record request with the entries its window covers.
function recordingModel(request: any) {
  if (!isRecordRequest(request)) return answered('Reading auth.go')
  const lines = request.prompt.split('\n').slice(1)
  return answered('Covered ' + lines[0].slice(2) + ' to ' + lines.at(-1).slice(2) + '.')
}

async function openHiddenRow($: any, w: ReturnType<typeof world>, slices: number[], model: (request: any) => any = recordingModel) {
  w.surfaces = ['terminal']
  w.respondModel = model
  w.respond = follower(slices)
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  w.messages['ag-hidden'] = [{ role: 'user', text: 'msgate run run-hook' }]
  w.state.set('hiddenRows', { value: { 'ag-hidden': 'run-hook' }, version: 1 })
  await $.session.start(START)
}

async function hiddenRow($: any, w: ReturnType<typeof world>, slices: number[], model?: (request: any) => any) {
  await openHiddenRow($, w, slices, model)
  return step($, 'ag-hidden')
}

const textBlocks = (chunks: any[]) => chunks.filter((c) => c.kind === 'text').map((c) => ({ index: c.index, text: c.text }))

test('a hidden row records a sentence for each window of its run, ahead of its final text', async ($, on) => {
  const w = world(on, { backed: BACKED })
  const out = await hiddenRow($, w, [4, 4, 4, 4, 4])

  expect(textBlocks(out.chunks)).toEqual([
    { index: 1, text: '» 1–8: Covered entry 1 to entry 8.\n' },
    { index: 2, text: '» 9–16: Covered entry 9 to entry 16.\n' },
    { index: 3, text: '» 17–20: Covered entry 17 to entry 20.\n' },
    { index: 4, text: 'msgate run run-hook ended ok' },
  ])
  expect(out.chunks.find((c) => c.kind === 'thinking')).toMatchObject({ index: 0 })
  const requests = w.modelRequests.filter(isRecordRequest)
  expect(requests).toHaveLength(3)
  expect(requests[0]).toMatchObject({
    model: 'claude-haiku-5-5',
    effort: 'low',
    maxTokens: 120,
    timeoutMs: 15000,
    prompt: 'Newest activity of the agent, oldest first:\n' + Array.from({ length: 8 }, (_, i) => '- entry ' + (i + 1)).join('\n'),
  })
})

// A run of `count` entries, `perSlice` to a slice, numbered from 1.
function slicesOf(count: number, perSlice: number) {
  const slices: { kind: string; text: string }[][] = []
  for (let first = 1; first <= count; first += perSlice) {
    slices.push(Array.from({ length: Math.min(perSlice, count - first + 1) }, (_, i) => msg('entry ' + (first + i))))
  }
  return slices
}

async function backedRow($: any, w: ReturnType<typeof world>, slices: { kind: string; text: string }[][]) {
  w.surfaces = ['terminal']
  w.respondModel = recordingModel
  w.respond = codex([...slices, []])
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  return step($, 'ag-1')
}

const delivered = (out: any): string => out.result.toolUses[0].input.message

test('a row the Claude calls records the same blocks ahead of its hand-back and hands back what it always did', async ($, on) => {
  const w = world(on)
  const out = await backedRow($, w, slicesOf(20, 4))
  const second = await step($, 'ag-1', { index: 1 })

  expect(textBlocks(out.chunks)).toEqual([
    { index: 1, text: '» 1–8: Covered entry 1 to entry 8.\n' },
    { index: 2, text: '» 9–16: Covered entry 9 to entry 16.\n' },
    { index: 3, text: '» 17–20: Covered entry 17 to entry 20.\n' },
  ])
  expect(out.chunks.filter((c) => c.kind === 'tool' || c.kind === 'input').map((c) => [c.kind, c.index])).toEqual([['tool', 4], ['input', 4]])
  expect(out.result.toolUses).toHaveLength(1)
  expect(delivered(out)).not.toContain('»')
  expect(delivered(out)).toContain('Codex: done.')
  // The step after the hand-back repeats the answer alone, where it always was.
  expect(textBlocks(second.chunks)).toEqual([{ index: 1, text: delivered(out) }])
})

// How many record requests had reached the model when each wait was asked.
function recordsAtWaits(w: ReturnType<typeof world>) {
  const asked: number[] = []
  const run = w.respond
  w.respond = (argv, init) => {
    if (argv[1] === 'wait') asked.push(w.modelRequests.filter(isRecordRequest).length)
    return run(argv, init)
  }
  return asked
}

test('a run of fewer entries than a window is recorded once, when it ends, and not while it runs', async ($, on) => {
  const w = world(on, { backed: BACKED })
  const asked = recordsAtWaits(w)
  const out = await hiddenRow($, w, [3, 2])

  expect(textBlocks(out.chunks)).toEqual([
    { index: 1, text: '» 1–5: Covered entry 1 to entry 5.\n' },
    { index: 2, text: 'msgate run run-hook ended ok' },
  ])
  expect(w.modelRequests.filter(isRecordRequest)).toHaveLength(1)
  expect(asked.every((count) => count === 0)).toBe(true)
})

test('a run with no entries asks for no record and streams no block ahead of its text', async ($, on) => {
  const w = world(on, { backed: BACKED })
  const out = await hiddenRow($, w, [0, 0])

  expect(w.modelRequests).toEqual([])
  expect(textBlocks(out.chunks)).toEqual([{ index: 1, text: 'msgate run run-hook ended ok' }])
})

test('a record request that fails leaves its block out, logs it once, and the next window covers what was left', async ($, on) => {
  const w = world(on, { backed: BACKED })
  let isFirst = true
  const out = await hiddenRow($, w, [4, 4, 4, 4, 4], (request) => {
    if (isRecordRequest(request) && isFirst) {
      isFirst = false
      return apiError(500)
    }
    return recordingModel(request)
  })

  expect(textBlocks(out.chunks)).toEqual([
    { index: 1, text: '» 1–12: Covered entry 1 to entry 12.\n' },
    { index: 2, text: '» 13–20: Covered entry 13 to entry 20.\n' },
    { index: 3, text: 'msgate run run-hook ended ok' },
  ])
  expect(w.debugLogs.filter((text) => text.startsWith('could not record the progress of run run-hook'))).toEqual([
    'could not record the progress of run run-hook: no reply: api-error (status 500)',
  ])
})

test('recordedSummaryEvery sets how many entries make a window', { options: { recordedSummaryEvery: 4 } }, async ($, on) => {
  const w = world(on, { backed: BACKED })
  const out = await hiddenRow($, w, [4, 4, 4])

  expect(textBlocks(out.chunks).map((block) => block.text.split(':')[0])).toEqual(['» 1–4', '» 5–8', '» 9–12', 'msgate run run-hook ended ok'])
})

test('recordedSummaryEvery 0 records nothing', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on, { backed: BACKED })
  const out = await hiddenRow($, w, [4, 4, 4, 4, 4])

  expect(w.modelRequests.filter(isRecordRequest)).toEqual([])
  expect(textBlocks(out.chunks)).toEqual([{ index: 1, text: 'msgate run run-hook ended ok' }])
})

test('the result of a step that streamed records names the text it streamed', async ($, on) => {
  const w = world(on, { backed: BACKED })
  const hidden = await hiddenRow($, w, [4, 4])

  expect(hidden.result.answer).toBe(textBlocks(hidden.chunks).map((block) => block.text).join(''))
  expect(hidden.result.toolUses).toEqual([])
})

test('the result of a hand-back step that streamed records names the records and the hand-back call', async ($, on) => {
  const w = world(on)
  const out = await backedRow($, w, slicesOf(8, 4))

  expect(out.result.answer).toBe('» 1–8: Covered entry 1 to entry 8.\n')
  expect(out.result.toolUses).toEqual([{ name: 'SubagentHandback', input: { message: delivered(out) } }])
})

function deferred<T>() {
  let release!: (value: T) => void
  const promise = new Promise<T>((resolve) => {
    release = resolve
  })
  return { promise, release }
}

// until lets the mod's own awaits run until `condition` holds.
async function until(condition: () => boolean) {
  for (let i = 0; i < 200 && !condition(); i++) await new Promise((resolve) => setTimeout(resolve, 1))
  expect(condition()).toBe(true)
}

test('a row stopped while a window is out streams no block and asks for no last window', async ($, on) => {
  const w = world(on, { backed: BACKED })
  const held = deferred<unknown>()
  await openHiddenRow($, w, [4, 4, 4, 4, 4], (request) => (isRecordRequest(request) ? held.promise : answered('Reading auth.go')))
  const row = readStep($, 'ag-hidden')

  const chunks: any[] = []
  do chunks.push((await row.next()).value)
  while (!(chunks.at(-1).kind === 'thinking' && chunks.at(-1).text.startsWith('» ')))
  await until(() => w.modelRequests.filter(isRecordRequest).length === 1)
  await row.stop()
  held.release(answered('Covered entry 1 to entry 8.'))
  await w.clock.advance(60000)

  expect(chunks.filter((c) => c.kind === 'text')).toEqual([])
  expect(w.modelRequests.filter(isRecordRequest)).toHaveLength(1)
})

test('a row stopped mid-run leaves no summary behind for the next follow of the same run', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await openHiddenRow($, w, [4, 4, 4])
  const row = readStep($, 'ag-hidden')
  let chunk
  do chunk = (await row.next()).value
  while (!(chunk.kind === 'thinking' && chunk.text.startsWith('» ')))
  await row.stop()

  w.respond = follower([1])
  await step($, 'ag-hidden', { turnId: 'turn-2' })

  // The first request of the new follow carries no label of the stopped one.
  const live = w.modelRequests.filter((request) => !isRecordRequest(request))
  expect(live.at(-1).prompt).toBe('Newest activity of the agent, oldest first:\n- entry 1')
})

const thinkingOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())

test('a hidden row with the summaries off streams the entries of its run, asks for nothing and records nothing', { options: { summaries: false } }, async ($, on) => {
  const w = world(on, { backed: BACKED })
  const out = await hiddenRow($, w, [4, 4, 4])

  expect(thinkingOf(out.chunks)).toEqual(Array.from({ length: 12 }, (_, i) => 'entry ' + (i + 1)))
  expect(w.modelRequests).toEqual([])
  expect(textBlocks(out.chunks)).toEqual([{ index: 1, text: 'msgate run run-hook ended ok' }])
})

test('a hidden row of a session with no surface asks for nothing and shows the opening line', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await openHiddenRow($, w, [4, 4], recordingModel)
  w.surfaces = []
  const out = await step($, 'ag-hidden')

  expect(thinkingOf(out.chunks)).toEqual(['msgate · run', ...Array.from({ length: 8 }, (_, i) => 'entry ' + (i + 1))])
  expect(w.modelRequests).toEqual([])
  expect(textBlocks(out.chunks)).toEqual([{ index: 1, text: 'msgate run run-hook ended ok' }])
})

test('every record request is counted with the usage of the run, like the live labels', async ($, on) => {
  const w = world(on, { backed: BACKED })
  await hiddenRow($, w, [4, 4, 4, 4, 4], (request) => (isRecordRequest(request) ? recordingModel(request) : answered('Reading auth.go')))
  await w.clock.advance(0)

  const adds = w.runs.filter((r) => r.argv[1] === 'usage')
  expect(w.modelRequests.filter(isRecordRequest)).toHaveLength(3)
  expect(adds).toHaveLength(w.modelRequests.length)
  expect(adds.every((add) => add.argv.includes('mod-summary') && add.argv.includes('run-hook'))).toBe(true)
})
