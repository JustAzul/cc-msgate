import { expect, test } from 'claude-code/testing'
import { WAIT_TIMEOUT, answered, engineOf, job, ok, world } from './kit'
import { RECORD_MAX_TOKENS } from '../hooks/recorder.js'
import { advanceFollow, createFollow, finishFollow, watchRun } from '../hooks/register.js'

const RUN = 'run-1'
const BIN = '/plugin/bin/msgate'
const isRecordRequest = (request: any) => request.maxTokens === RECORD_MAX_TOKENS
const finished = () => job({ run_id: RUN, state: 'done', outcome: 'ok' })

// A follow of the run, whose steps run in the engine `$` it was made with.
async function opened($: any, signal: AbortSignal) {
  const follow = await createFollow($, BIN, RUN, signal)
  return { advance: () => advanceFollow($, follow), finish: () => finishFollow($, follow) }
}

// The world a follow runs in: `timeouts` waits that give up, then a wait that
// finds the run done, and the progress entries each read answers in turn.
function followed(w: ReturnType<typeof world>, { timeouts, entries = [] }: { timeouts: number; entries?: number[] }) {
  let waits = 0
  let reads = 0
  let total = 0
  w.respond = (argv) => {
    if (argv[1] === 'wait') return ++waits <= timeouts ? WAIT_TIMEOUT : ok(JSON.stringify(finished()))
    if (argv[1] === 'progress') {
      const count = entries[reads++] ?? 0
      const listed = Array.from({ length: count }, (_, i) => ({ seq: total + i, at: 't', kind: 'message', text: 'entry ' + (total + i + 1) }))
      total += count
      return ok(JSON.stringify({ run_id: RUN, state: 'running', next: total, entries: listed }))
    }
    return ok('')
  }
}

// The engine's `$` for a follow: the world's process and a clock the test moves.
function engine(w: ReturnType<typeof world>, { surfaces = [] as string[] } = {}) {
  const clock = { at: 0 }
  const $ = {
    ...engineOf(w),
    plugin: { name: 'msgate', root: '/plugin' },
    clock: { now: async () => clock.at, after: () => ({ cancel: () => {} }) },
    env: { get: async () => undefined },
    session: { id: async () => 'sess-1', surfaces: async () => surfaces },
    model: { complete: async (request: any) => (isRecordRequest(request) ? answered('Covered the window.') : answered('Reading auth.go')) },
    ui: { log: async (text: string) => void w.debugLogs.push(text), invalidate: () => {} },
  }
  return { $, clock }
}

test('advance is not done while the wait times out, and done once the run ends', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 2 })
  const follow = await opened(engine(w).$, new AbortController().signal)

  const results = [await follow.advance(), await follow.advance(), await follow.advance()]

  expect(results.map((r) => r.isDone)).toEqual([false, false, true])
  expect(w.runs.filter((r) => r.argv[1] === 'wait')).toHaveLength(3)
  await follow.finish()
})

test('advance is done as soon as the signal aborts, though the wait timed out', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 5 })
  const abort = new AbortController()
  const follow = await opened(engine(w).$, abort.signal)

  const before = await follow.advance()
  abort.abort()
  const after = await follow.advance()

  expect([before.isDone, after.isDone]).toEqual([false, true])
  await follow.finish()
})

test('advance reads the progress from where the last read stopped', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 2, entries: [2, 1] })
  const follow = await opened(engine(w).$, new AbortController().signal)

  await follow.advance()
  await follow.advance()

  expect(w.runs.filter((r) => r.argv[1] === 'progress').map((r) => r.argv[4])).toEqual(['0', '2'])
  await follow.finish()
})

test('advance returns the lines of the progress it read, and a heartbeat once a run has stayed quiet', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 3, entries: [1] })
  const { $: engineAt, clock } = engine(w)
  const follow = await opened(engineAt, new AbortController().signal)

  const first = await follow.advance()
  const quiet = await follow.advance()
  clock.at = 31_000
  const heartbeat = await follow.advance()

  expect(first.lines).toEqual(['entry 1'])
  expect(quiet.lines).toEqual([])
  expect(heartbeat.lines).toEqual(['still working · 31s'])
  await follow.finish()
})

test('finish returns the records of the windows when the turn was not interrupted', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 1, entries: [8] })
  const follow = await opened(engine(w, { surfaces: ['terminal'] }).$, new AbortController().signal)

  while (!(await follow.advance()).isDone);
  const records = await follow.finish()

  expect(records.map((record: any) => record.text)).toEqual(['» 1–8: Covered the window.\n'])
})

test('finish returns no records when the turn was interrupted', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 1, entries: [8] })
  const abort = new AbortController()
  const follow = await opened(engine(w, { surfaces: ['terminal'] }).$, abort.signal)

  await follow.advance()
  abort.abort()
  await follow.advance()
  const records = await follow.finish()

  expect(records).toEqual([])
})

test('a turn stopped while the lines of a slice are shown starts no further wait', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 5, entries: [1] })
  const abort = new AbortController()
  const watching = watchRun(engine(w).$, BIN, RUN, abort.signal)

  const shown = await watching.next()
  abort.abort()
  const ended = await watching.next()

  expect(shown.done).toBe(false)
  expect(ended).toEqual({ done: true, value: [] })
  expect(w.runs.filter((r) => r.argv[1] === 'wait')).toHaveLength(1)
})

test('advance waits through the engine of the step it runs in, not the one the follow was made with', async ($, on) => {
  const w = world(on)
  followed(w, { timeouts: 2 })
  const made = engine(w).$
  const later = engine(w).$
  const waitedThrough: string[] = []
  later.process = { run: async (argv: string[], init?: any) => (waitedThrough.push(argv[1]!), w.processRun(argv, init)) }
  const follow = await createFollow(made, BIN, RUN, new AbortController().signal)

  await advanceFollow(later, follow)
  await finishFollow(later, follow)

  expect(waitedThrough).toEqual(['wait', 'progress'])
})
