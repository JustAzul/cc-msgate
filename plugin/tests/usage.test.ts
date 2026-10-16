import { expect, test } from 'claude-code/testing'
import { BAND_REFRESH_MS } from '../hooks/band.js'
import { NO_USAGE, POLL_MS, SESSION, answered, apiError, emptyReply, job, listing, ok, step, withoutSpawnNotices, world } from './kit'
import type { Reply } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }
const BILLED = { input_tokens: 458, output_tokens: 18, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }

const isUsageAdd = (argv: string[]) => argv[1] === 'usage'
const usageRuns = (w: ReturnType<typeof world>) => w.runs.filter((r) => isUsageAdd(r.argv))
const statsReads = (w: ReturnType<typeof world>) => w.runs.filter((r) => r.argv[1] === 'stats').length
const recordFailures = (w: ReturnType<typeof world>) => w.logs.filter((l) => l.includes('could not record'))

function deferred<T>() {
  let release!: (value: T) => void
  const promise = new Promise<T>((resolve) => {
    release = resolve
  })
  return { promise, release }
}

// codex is a run that works through `slices`: each wait times out and the
// progress read after it answers the next slice's entries; the wait after the
// last slice finds the run done.
function codex(slices: { kind: string; text: string }[][]) {
  let waits = 0
  let reads = 0
  let next = 0
  return (argv: string[]): Reply => {
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

// row runs the agent of an msgate type over a run of `slices`, with `usage
// add` answered by `recording`, and reads its row.
async function row($: any, w: ReturnType<typeof world>, slices: { kind: string; text: string }[][], recording: (argv: string[]) => Reply | Promise<Reply> = () => ok('')) {
  const run = codex(slices)
  w.surfaces = ['terminal']
  w.respond = (argv) => (isUsageAdd(argv) ? recording(argv) : run(argv))
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  return step($, 'ag-1')
}

const entry = (text: string) => ({ kind: 'message', text })

test('an answered reply is recorded with its usage on standard input', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  await row($, w, [[entry('e1')], []])

  expect(usageRuns(w)).toHaveLength(1)
  const [recorded] = usageRuns(w)
  expect(recorded!.argv[0]!.endsWith('/bin/msgate')).toBe(true)
  expect(recorded!.argv.slice(1)).toEqual([
    'usage', 'add', '--session-id', SESSION, '--provider', 'anthropic', '--model', 'claude-haiku-5-5',
    '--source', 'mod-summary', '--run-id', 'run-1', '--json',
  ])
  expect(recorded!.stdin).toBe(JSON.stringify(BILLED))
})

test('an empty reply that was billed is recorded', async ($, on) => {
  const w = world(on)
  w.respondModel = () => emptyReply()
  await row($, w, [[entry('e1')], []])

  expect(usageRuns(w)).toHaveLength(1)
  expect(usageRuns(w)[0]!.stdin).toBe(JSON.stringify({ ...NO_USAGE, input_tokens: 458, output_tokens: 2 }))
})

test('an answered reply whose label is rejected is recorded', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('# Reading auth.go')
  await row($, w, [[entry('e1')], []])

  expect(usageRuns(w)).toHaveLength(1)
})

const UNBILLED = {
  'a completion with no usage': () => ({ isAnswered: true, text: 'Reading auth.go', usage: NO_USAGE }),
  'an api error': () => apiError(500),
  'a 429': () => apiError(429),
  'a timeout': () => ({ isAnswered: false, reason: 'timeout', usage: NO_USAGE }),
  'an abort': () => ({ isAnswered: false, reason: 'aborted', usage: NO_USAGE }),
  'a rejection': () => {
    throw new Error('the engine refused the model')
  },
}

for (const [name, reply] of Object.entries(UNBILLED)) {
  test(name + ' is not recorded', async ($, on) => {
    const w = world(on)
    w.respondModel = reply
    await row($, w, [[entry('e1')], []])

    expect(w.modelRequests).toHaveLength(1)
    expect(usageRuns(w)).toEqual([])
  })
}

test('the label is shown while the recording is still held', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  const held = deferred<Reply>()
  const out = await row($, w, [[entry('e1')], []], () => held.promise)

  expect(usageRuns(w)).toHaveLength(1)
  expect(out.chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())).toEqual(['msgate · second-opinion', '» Reading auth.go'])
  expect(out.text).toContain('Codex: done.')
  held.release(ok(''))
})

test('a recording that fails is logged once, until one succeeds', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  const replies: (Reply | Error)[] = [
    { exitCode: 70, stdout: '', stderr: 'msgate: telemetry lock busy\n' },
    new Error('process timed out'),
    ok(''),
    { exitCode: 2, stdout: '', stderr: 'bad usage\n' },
  ]
  await row($, w, [[entry('e1')], [entry('e2'), entry('e3'), entry('e4')], [entry('e5'), entry('e6'), entry('e7')], [entry('e8'), entry('e9'), entry('e10')], []], () => {
    const reply = replies.shift()!
    if (reply instanceof Error) throw reply
    return reply
  })

  expect(usageRuns(w)).toHaveLength(4)
  expect(recordFailures(w)).toHaveLength(2)
  expect(recordFailures(w)[0]).toContain('telemetry lock busy')
  expect(recordFailures(w)[1]).toContain('bad usage')
})

// The status line reads the costs again after a recorded call. A summary of a
// flagged run, in the band, records a call on a poll; the next poll can read.

const stats = JSON.stringify({ usage_by_provider: { codex: { cost_usd: '5.820000', cost_complete: true }, anthropic: { cost_usd: '0.030000', cost_complete: true } } })

// flagged follows one run another caller flagged, one entry long.
function flagged(w: ReturnType<typeof world>, recording: (argv: string[]) => Reply | Promise<Reply> = () => ok(''), costs: (argv: string[]) => Reply | Promise<Reply> = () => ok(stats)) {
  const entries = [entry('e1')]
  w.surfaces = ['terminal']
  w.respond = (argv) => {
    if (isUsageAdd(argv)) return recording(argv)
    if (argv[1] === 'stats') return costs(argv)
    if (argv[1] === 'progress') {
      const from = Number(argv[4])
      return ok(JSON.stringify({ run_id: 'run-h', state: 'running', next: entries.length, entries: entries.slice(from).map((e, i) => ({ seq: from + i, at: 't', ...e })) }))
    }
    return listing(job({ run_id: 'run-h', state: 'running', source: 'hook-post-commit', purpose: 'code-review', background: false, agent_feedback: true, started_at: '1970-01-01T00:00:00Z' }))
  }
  return entries
}

test('a recorded call makes the next poll read the costs again, once', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  flagged(w)
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(usageRuns(w)).toHaveLength(1)
  expect(statsReads(w)).toBe(0)

  await w.clock.advance(POLL_MS)
  expect(statsReads(w)).toBe(1)
  expect(w.statuses.at(-1)).toBe('💸 Codex $5.82 | Claude $0.03')

  // No further add, no run ended, no price change: nothing to read.
  await w.clock.advance(POLL_MS * 3)
  expect(statsReads(w)).toBe(1)
})

test('a recording that failed does not make the next poll read the costs', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  flagged(w, () => ({ exitCode: 70, stdout: '', stderr: 'busy\n' }))
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 3)

  // The one call is tried again on each poll, and never recorded.
  expect(new Set(usageRuns(w).map((r) => r.stdin)).size).toBe(1)
  expect(statsReads(w)).toBe(0)
})

test('a call recorded while the costs are being read makes the poll after it read them again', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  const recording = deferred<Reply>()
  const reading = deferred<Reply>()
  let reads = 0
  const entries = flagged(
    w,
    () => recording.promise,
    () => (++reads === 1 ? reading.promise : ok(stats)),
  )
  await $.session.start(START)

  // The first summary is out; its recording is held.
  await w.clock.advance(POLL_MS)
  expect(usageRuns(w)).toHaveLength(1)
  recording.release(ok(''))
  await w.clock.advance(0)

  // The read that sees the add starts and is held; a second call is recorded meanwhile.
  await w.clock.advance(POLL_MS)
  expect(statsReads(w)).toBe(1)
  entries.push(entry('e2'), entry('e3'), entry('e4'))
  await w.clock.advance(BAND_REFRESH_MS)
  expect(usageRuns(w)).toHaveLength(2)
  reading.release(ok(stats))
  await w.clock.advance(0)

  await w.clock.advance(POLL_MS)
  expect(statsReads(w)).toBe(2)
  await w.clock.advance(POLL_MS * 3)
  expect(statsReads(w)).toBe(2)
})

test('a read of the costs that fails leaves the recorded call due', async ($, on) => {
  const w = world(on)
  w.respondModel = () => answered('Reading auth.go')
  let reads = 0
  flagged(
    w,
    () => ok(''),
    () => (++reads === 1 ? { exitCode: 70, stdout: '', stderr: 'busy\n' } : ok(stats)),
  )
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 4)

  expect(statsReads(w)).toBe(2)
  expect(w.statuses.at(-1)).toBe('💸 Codex $5.82 | Claude $0.03')
})

// A recording that fails is kept and tried again on each poll.

const BUSY = { exitCode: 70, stdout: '', stderr: 'busy\n' }
const DROP_TOAST = 'some summary costs could not be recorded; the Claude cost is a lower bound'
const MAX_PENDING = 100

// billedBy answers each summary call with its own usage, so a recording is told
// from the others by its standard input.
function billedBy(w: ReturnType<typeof world>) {
  let calls = 0
  w.respondModel = () => ({ isAnswered: true, text: 'Reading auth.go', usage: { ...NO_USAGE, input_tokens: ++calls, output_tokens: 1 } })
}

const stdinOf = (calls: number) => JSON.stringify({ ...NO_USAGE, input_tokens: calls, output_tokens: 1 })
const stdins = (w: ReturnType<typeof world>) => usageRuns(w).map((r) => r.stdin)
const unique = <T>(items: T[]) => [...new Set(items)]

test('a recording that failed is tried again on the next poll with the same standard input', async ($, on) => {
  const w = world(on)
  billedBy(w)
  let isBusy = true
  flagged(w, () => (isBusy ? BUSY : ok('')))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(unique(stdins(w))).toEqual([stdinOf(1)])
  expect(recordFailures(w)).toHaveLength(1)

  isBusy = false
  const failed = usageRuns(w).length
  await w.clock.advance(POLL_MS)

  expect(usageRuns(w).length).toBe(failed + 1)
  expect(usageRuns(w).at(-1)!.argv).toEqual(usageRuns(w)[0]!.argv)
  expect(usageRuns(w).at(-1)!.stdin).toBe(stdinOf(1))

  // Recorded now: it is not tried again.
  await w.clock.advance(POLL_MS * 3)
  expect(usageRuns(w).length).toBe(failed + 1)
})

test('a recording that failed is tried again for the session it was made in, not the one running by then', async ($, on) => {
  const w = world(on)
  billedBy(w)
  let isBusy = true
  flagged(w, () => (isBusy ? BUSY : ok('')))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(recordFailures(w)).toHaveLength(1)

  isBusy = false
  w.sessionId = 'sess-2'
  const failed = usageRuns(w).length
  await w.clock.advance(POLL_MS)

  const retry = usageRuns(w).at(-1)!
  expect(usageRuns(w).length).toBe(failed + 1)
  expect(retry.argv[retry.argv.indexOf('--session-id') + 1]).toBe(SESSION)
})

test('a recording that succeeds on a retry makes the same poll read the costs again', async ($, on) => {
  const w = world(on)
  billedBy(w)
  let isBusy = true
  flagged(w, () => (isBusy ? BUSY : ok('')))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(statsReads(w)).toBe(0)

  isBusy = false
  await w.clock.advance(POLL_MS)

  expect(statsReads(w)).toBe(1)
  expect(w.statuses.at(-1)).toBe('💸 Codex $5.82 | Claude $0.03')
})

test('the retries stop at the first recording that fails again, and go on in order once it is recorded', async ($, on) => {
  const w = world(on)
  billedBy(w)
  let isBusy = true
  const entries = flagged(w, () => (isBusy ? BUSY : ok('')))
  await $.session.start(START)

  // The first poll starts the band; its refresh summarizes the first entry.
  await w.clock.advance(POLL_MS)
  for (let i = 0; i < 2; i += 1) {
    entries.push(entry('a'), entry('b'), entry('c'))
    await w.clock.advance(BAND_REFRESH_MS)
  }
  const kept = unique(stdins(w))
  expect(kept).toEqual([stdinOf(1), stdinOf(2), stdinOf(3)])

  // Nothing new to summarize: one poll tries the oldest only, which fails again.
  const before = usageRuns(w).length
  await w.clock.advance(POLL_MS)
  const retried = usageRuns(w).slice(before)
  expect(retried.map((r) => r.stdin)).toEqual([kept[0]])

  isBusy = false
  const failed = usageRuns(w).length
  await w.clock.advance(POLL_MS)
  expect(usageRuns(w).slice(failed).map((r) => r.stdin)).toEqual(kept)
})

test('the 101st failed recording drops the oldest, with one toast per load', async ($, on) => {
  const w = world(on)
  billedBy(w)
  let isBusy = true
  const entries = flagged(w, () => (isBusy ? BUSY : ok('')))
  await $.session.start(START)

  // The first poll starts the band; its refresh summarizes the first entry.
  await w.clock.advance(POLL_MS)
  for (let kept = 1; kept < MAX_PENDING; kept += 1) {
    entries.push(entry('a'), entry('b'), entry('c'))
    await w.clock.advance(BAND_REFRESH_MS)
  }
  expect(unique(stdins(w))).toHaveLength(MAX_PENDING)
  expect(withoutSpawnNotices(w.toasts)).toEqual([])

  entries.push(entry('a'), entry('b'), entry('c'))
  await w.clock.advance(BAND_REFRESH_MS)
  expect(unique(stdins(w))).toHaveLength(MAX_PENDING + 1)
  expect(withoutSpawnNotices(w.toasts)).toEqual([DROP_TOAST])
  expect(w.logs.filter((l) => l.includes('dropped'))).toHaveLength(1)

  // A second drop is a loss too, and is logged, but shows no second toast.
  entries.push(entry('a'), entry('b'), entry('c'))
  await w.clock.advance(BAND_REFRESH_MS)
  expect(withoutSpawnNotices(w.toasts)).toEqual([DROP_TOAST])
  expect(w.logs.filter((l) => l.includes('dropped'))).toHaveLength(2)

  // The two oldest are gone for good; the other hundred are recorded in order.
  isBusy = false
  const failed = usageRuns(w).length
  await w.clock.advance(POLL_MS)
  const recorded = usageRuns(w).slice(failed).map((r) => r.stdin)
  expect(recorded).toEqual(Array.from({ length: MAX_PENDING }, (_, i) => stdinOf(i + 3)))
})
