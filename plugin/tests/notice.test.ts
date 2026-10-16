import { expect, test } from 'claude-code/testing'
import { POLL_MS, TOOL, deliveries, job, listing, ok, queueKey, queued, seeded, statusReads, withoutSpawnNotices, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const KIB8 = 8192
const ADMITTED = { conversation_id: 'conv-1', run_id: 'run-1', state: 'running', run_dir: '/s/runs/run-1', output_path: '/s/runs/run-1/output.md' }

function finished(over: Record<string, unknown> = {}) {
  return job({ state: 'done', outcome: 'ok', purpose: 'code-review', ...over })
}

// A world whose store holds a queue with the given runs, each answering its status.
function queuedWorld(on: any, runs: Record<string, unknown>, output = 'out') {
  const w = world(on, { store: seeded(Object.keys(runs).map((id) => queued(id))) })
  w.respond = deliveries(runs, output)
  return w
}

test('run enqueues the admitted job with its purpose and the admission time', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-' + ++n }))
  await $.session.start(START)

  await $.tool.call({ tool: TOOL('run'), prompt: 'review', purpose: 'code-review' })
  await w.clock.advance(2_000)
  await $.tool.call({ tool: TOOL('run'), prompt: 'again' })

  const [first, second] = w.store.get(queueKey()) as any[]
  expect(first).toEqual({ run_id: 'run-1', conversation_id: 'conv-1', purpose: 'code-review', admitted_at: first.admitted_at, attempted_at: null })
  expect(typeof first.admitted_at).toBe('number')
  expect(second.admitted_at - first.admitted_at).toBe(2_000)
})

test('run with a conversation enqueues the admitted job, with a null purpose when it names no purpose', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-2' }))
  await $.session.start(START)

  await $.tool.call({ tool: TOOL('run'), conversation_id: 'conv-1', prompt: 'next' })

  const queue = w.store.get(queueKey()) as any[]
  expect(queue.map((entry) => [entry.run_id, entry.purpose, entry.attempted_at])).toEqual([['run-2', null, null]])
})

test('a refused admission enqueues nothing', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 2, stdout: '', stderr: 'msgate: unknown provider\n' })
  await $.session.start(START)

  await $.tool.call({ tool: TOOL('run'), prompt: 'hi', provider: 'x' })

  expect(w.store.has(queueKey())).toBe(false)
})

test('25 jobs that end before the next tick get 25 notices, none of them read from the session listing', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-' + String(++n).padStart(2, '0') }))
  await $.session.start(START)
  for (let i = 0; i < 25; i++) await $.tool.call({ tool: TOOL('run'), prompt: 'p' + i })
  const runs = Object.fromEntries(Array.from({ length: 25 }, (_, i) => ['run-' + String(i + 1).padStart(2, '0'), finished({ run_id: 'run-' + String(i + 1).padStart(2, '0') })]))
  w.respond = deliveries(runs)

  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(25)
  expect(withoutSpawnNotices(w.toasts).length).toBe(25)
  expect(w.store.has(queueKey())).toBe(false)
})

test('a finished job shows a toast and submits a notice with its output inline', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() }, 'Two findings.\n')
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.toasts.length).toBe(1)
  expect(w.toasts[0]).toContain('run-1')
  expect(w.submits.length).toBe(1)
  expect(w.submits[0]!.startsWith('msgate job run-1 (code-review, conversation conv-1) finished: ok\n')).toBe(true)
  expect(w.submits[0]).toContain('Two findings.')
  const read = w.runs.find((r) => r.argv[1] === 'result')!
  expect(read.argv.slice(1)).toEqual(['result', 'run-1'])
  // The delivered entry leaves the queue, and an empty queue leaves no key.
  expect(w.store.has(queueKey())).toBe(false)
})

test('the notice of a failed job names its state when there is no outcome', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished({ state: 'failed', outcome: null }) }, '')
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.submits[0]!.startsWith('msgate job run-1 (code-review, conversation conv-1) finished: failed')).toBe(true)
})

test('a running job raises no notice and stays queued', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': job({ state: 'running' }) })
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(withoutSpawnNotices(w.toasts)).toEqual([])
  expect(w.submits).toEqual([])
  expect((w.store.get(queueKey()) as any[]).map((entry) => entry.run_id)).toEqual(['run-1'])
})

test('foreground runs and CLI jobs in the session listing raise no notice and are never read by id', async ($, on) => {
  const w = world(on)
  w.respond = (argv) =>
    argv[1] === 'status' && argv[2] === '--json'
      ? listing(finished({ run_id: 'fg', background: false }), finished({ run_id: 'cli-job', source: 'cli' }), finished({ run_id: 'mod-job' }))
      : ok('')
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 2)

  expect(w.submits).toEqual([])
  expect(withoutSpawnNotices(w.toasts)).toEqual([])
  expect(statusReads(w)).toEqual([])
  expect(w.store.has('notified')).toBe(false)
})

test('a job is announced once however many polls see it finished', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() }, 'x')
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 4)

  expect(w.submits.length).toBe(1)
  expect(statusReads(w)).toEqual(['run-1'])
})

test('output over 8 KiB is cut to its first 8 KiB and the notice names the output path', async ($, on) => {
  const output = 'a'.repeat(KIB8) + 'Z'.repeat(12 * 1024)
  const w = queuedWorld(on, { 'run-1': finished({ output_path: '/state/runs/run-1/output.md' }) }, output)
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  const text = w.submits[0]!
  expect(text).toContain('a'.repeat(KIB8))
  expect(text).not.toContain('Z')
  expect(text).toContain('/state/runs/run-1/output.md')
})

test('output of exactly 8 KiB is delivered whole, without the path note', async ($, on) => {
  const output = 'c'.repeat(KIB8)
  const w = queuedWorld(on, { 'run-1': finished() }, output)
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.submits[0]).toContain(output)
  expect(w.submits[0]).not.toContain('/state/runs/run-1/output.md')
})

test('the cut counts bytes and never splits a character', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() }, '€'.repeat(4000)) // 3 bytes each: 12000 bytes
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  const text = w.submits[0]!
  expect(text).toContain('€'.repeat(2730)) // 8190 bytes
  expect(text).not.toContain('€'.repeat(2731))
  expect(text).not.toContain('�')
})

test('a notice is still sent when the output cannot be read', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() })
  const answer = w.respond
  w.respond = (argv, init) => (argv[1] === 'result' ? { exitCode: 70, stdout: '', stderr: 'opening the output: denied' } : answer(argv, init))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(1)
  expect(w.submits[0]).toContain('finished: ok')
  expect(w.submits[0]).toContain('/state/runs/run-1/output.md')
})

test('a notice is read oldest admission first and at most 50 runs are read in one tick', async ($, on) => {
  const ids = Array.from({ length: 60 }, (_, i) => 'r-' + String(i).padStart(2, '0'))
  // Stored newest first, as a queue appended in a different order would be.
  const entries = [...ids].reverse().map((id, i) => queued(id, { admitted_at: 1_000 + (59 - i) }))
  const w = world(on, { store: seeded(entries) })
  w.respond = deliveries(Object.fromEntries(ids.map((id) => [id, job({ run_id: id, state: 'running' })])))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(statusReads(w)).toEqual(ids.slice(0, 50))
})

test('a notice the session refuses is tried again on the next tick and is not marked as repeated', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() })
  const outcomes = ['drop', 'accept'] as const
  w.submitOutcome = () => outcomes[Math.min(w.submits.length - 1, 1)]!
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.toasts).toEqual([])
  expect((w.store.get(queueKey()) as any[])[0].attempted_at).toBeNull()

  await w.clock.advance(POLL_MS)
  expect(w.submits.length).toBe(2)
  expect(w.submits[1]).not.toContain('possibly repeated')
  expect(w.toasts.length).toBe(1)
  expect(w.store.has(queueKey())).toBe(false)
})

test('a submit that throws is tried again on the next tick and is not marked as repeated', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() })
  w.submitOutcome = () => (w.submits.length === 1 ? 'throw' : 'accept')
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.toasts).toEqual([])
  expect((w.store.get(queueKey()) as any[])[0].attempted_at).toBeNull()

  await w.clock.advance(POLL_MS)
  expect(w.submits.length).toBe(2)
  expect(w.submits[1]).not.toContain('possibly repeated')
  expect(w.toasts.length).toBe(1)
})

test('an attempt in flight is written to the stored entry before the notice is submitted', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() })
  let stored: any
  w.submitOutcome = () => {
    stored = (w.store.get(queueKey()) as any[])[0]
    return 'accept'
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(typeof stored.attempted_at).toBe('number')
})

test('a run msgate no longer knows leaves the queue with one debug line', async ($, on) => {
  const w = world(on, { store: seeded([queued('gone')]) })
  w.respond = deliveries({})
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 3)

  expect(w.store.has(queueKey())).toBe(false)
  expect(w.submits).toEqual([])
  expect(w.debugLogs.filter((line) => line.includes('gone')).length).toBe(1)
})

test('a status read that fails keeps the entry and is logged once per load', async ($, on) => {
  const w = world(on, { store: seeded([queued('run-1')]) })
  w.respond = (argv) => {
    if (argv[1] === 'status' && argv[2] === 'run-1') return { exitCode: 70, stdout: '', stderr: 'msgate: boom\n' }
    return listing()
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 3)

  expect((w.store.get(queueKey()) as any[]).map((entry) => entry.run_id)).toEqual(['run-1'])
  expect(w.debugLogs.filter((line) => line.includes('boom')).length).toBe(1)

  await $.session.start(START)
  await w.clock.advance(POLL_MS * 2)

  expect(w.debugLogs.filter((line) => line.includes('boom')).length).toBe(2)
  expect(w.submits).toEqual([])
})

// The session takes a prompt only when Claude's turn ends: a submit made
// mid-turn stays pending until then.
function pendingSubmit(w: ReturnType<typeof world>) {
  let settle: (outcome: 'accept' | 'drop' | 'throw') => void = () => {}
  const pending = new Promise<'accept' | 'drop' | 'throw'>((resolve) => (settle = resolve))
  w.submitOutcome = () => (w.submits.length === 1 ? pending : 'accept')
  return (outcome: 'accept' | 'drop' | 'throw') => settle(outcome)
}

test('a pending submit does not hold the poll, is not submitted again, and ends the entry when it resolves', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() })
  const resolve = pendingSubmit(w)
  const answer = w.respond
  let isRunning = false
  w.respond = (argv, init) => (argv[1] === 'status' && argv[2] === '--json' && isRunning ? listing(job({ run_id: 'other', state: 'running' })) : answer(argv, init))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  isRunning = true
  await w.clock.advance(POLL_MS)

  // The next tick ran its listing, which only the poll turns into the status
  // line, and left the run alone.
  expect(w.statuses.at(-1)).toContain('1 job running')
  expect(w.submits.length).toBe(1)
  expect(statusReads(w)).toEqual(['run-1'])
  expect(typeof (w.store.get(queueKey()) as any[])[0].attempted_at).toBe('number')
  expect(withoutSpawnNotices(w.toasts)).toEqual([])

  resolve('accept')
  await w.clock.advance(1)

  expect(w.store.has(queueKey())).toBe(false)
  expect(withoutSpawnNotices(w.toasts).length).toBe(1)
  expect(withoutSpawnNotices(w.toasts)[0]).toContain('run-1')
})

test('a pending submit that resolves refused puts the entry back and is tried again unmarked', async ($, on) => {
  const w = queuedWorld(on, { 'run-1': finished() })
  const resolve = pendingSubmit(w)
  await $.session.start(START)
  await w.clock.advance(POLL_MS * 2)
  expect(w.submits.length).toBe(1)

  resolve('drop')
  await w.clock.advance(1)

  expect(w.toasts).toEqual([])
  expect((w.store.get(queueKey()) as any[])[0].attempted_at).toBeNull()

  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(2)
  expect(w.submits[1]).not.toContain('possibly repeated')
  expect(w.store.has(queueKey())).toBe(false)
  expect(w.toasts.length).toBe(1)
})

test('a queue longer than one batch is read in turns, so a finished job behind 50 running ones is still delivered', async ($, on) => {
  const ids = Array.from({ length: 60 }, (_, i) => 'r-' + String(i).padStart(2, '0'))
  const entries = ids.map((id, i) => queued(id, { admitted_at: 1_000 + i }))
  const w = world(on, { store: seeded(entries) })
  w.respond = deliveries(Object.fromEntries(ids.map((id, i) => [id, job({ run_id: id, state: i < 50 ? 'running' : 'done', outcome: 'ok' })])))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.submits).toEqual([])
  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(10)
  expect((w.store.get(queueKey()) as any[]).length).toBe(50)
})

test('a queue of at most 50 entries is read whole, oldest first, on every tick', async ($, on) => {
  const ids = Array.from({ length: 50 }, (_, i) => 'r-' + String(i).padStart(2, '0'))
  const w = world(on, { store: seeded(ids.map((id, i) => queued(id, { admitted_at: 1_000 + i }))) })
  w.respond = deliveries(Object.fromEntries(ids.map((id) => [id, job({ run_id: id, state: 'running' })])))
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 2)

  expect(statusReads(w)).toEqual([...ids, ...ids])
})

const NOT_QUEUED = 'could not be queued'

test('a queue write that fails at admission is retried on the next tick, with one toast, and the notice follows', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)
  w.storeSetFailures = 1

  await $.tool.call({ tool: TOOL('run'), prompt: 'p' })

  expect(w.store.has(queueKey())).toBe(false)
  expect(w.toasts.filter((text) => text.includes(NOT_QUEUED) && text.includes('run-1')).length).toBe(1)
  expect(w.debugLogs.some((line) => line.includes('run-1') && line.includes(NOT_QUEUED))).toBe(true)

  let state = 'running'
  w.respond = (argv, init) => deliveries({ 'run-1': job({ run_id: 'run-1', state, outcome: 'ok' }) })(argv, init)
  await w.clock.advance(POLL_MS)

  expect((w.store.get(queueKey()) as any[]).map((entry) => entry.run_id)).toEqual(['run-1'])

  state = 'done'
  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(1)
  expect(w.store.has(queueKey())).toBe(false)
  expect(w.toasts.filter((text) => text.includes(NOT_QUEUED)).length).toBe(1)
})

test('an entry the store never accepts is delivered from memory once, and not again', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)
  w.storeSetFailures = Infinity

  await $.tool.call({ tool: TOOL('run'), prompt: 'p' })
  w.respond = deliveries({ 'run-1': finished({ run_id: 'run-1' }) })
  await w.clock.advance(POLL_MS * 3)

  expect(w.submits.length).toBe(1)
  expect(w.submits[0]).toContain('msgate job run-1')
  expect(w.toasts.filter((text) => text.includes(NOT_QUEUED)).length).toBe(1)
  expect(w.toasts.filter((text) => text.startsWith('job run-1 finished')).length).toBe(1)
})

test('several entries the store refuses raise one toast between them, and each is logged', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-' + ++n }))
  await $.session.start(START)
  w.storeSetFailures = Infinity

  await $.tool.call({ tool: TOOL('run'), prompt: 'a' })
  await $.tool.call({ tool: TOOL('run'), prompt: 'b' })

  expect(w.toasts.filter((text) => text.includes(NOT_QUEUED)).length).toBe(1)
  expect(w.debugLogs.filter((line) => line.includes(NOT_QUEUED)).length).toBe(2)
})

test('a submit that settles while its held entry is being written leaves no copy behind once accepted', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)
  w.storeSetFailures = 1
  await $.tool.call({ tool: TOOL('run'), prompt: 'p' })
  w.respond = deliveries({ 'run-1': finished({ run_id: 'run-1' }) })

  // The store refuses while the run ends: the notice is delivered from memory,
  // and its submit stays pending.
  w.storeSetFailures = Infinity
  let settle: (outcome: 'accept' | 'drop' | 'throw') => void = () => {}
  const pending = new Promise<'accept' | 'drop' | 'throw'>((resolve) => (settle = resolve))
  let submits = 0
  w.submitOutcome = () => (++submits === 1 ? pending : 'accept')
  await w.clock.advance(POLL_MS)
  expect(submits).toBe(1)

  // The store takes writes again, but the write of the held entry stays open
  // while the pending submit is refused.
  w.storeSetFailures = 0
  let release: () => void = () => {}
  w.storeSetHold = new Promise<void>((resolve) => (release = resolve))
  await w.clock.advance(POLL_MS)
  settle('drop')
  await w.clock.advance(1)
  w.storeSetHold = null
  release()
  await w.clock.advance(POLL_MS * 4)

  // One retry, accepted once, and no copy of the entry is left to announce the run again.
  expect(submits).toBe(2)
  expect([...w.store.keys()].filter((key) => key.startsWith('delivery:'))).toEqual([])
})

test('a held entry is delivered and toasted while the store refuses writes next to other queued entries', async ($, on) => {
  const w = world(on, { store: seeded([queued('other')]) })
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)
  w.storeSetFailures = Infinity
  await $.tool.call({ tool: TOOL('run'), prompt: 'p' })
  w.respond = deliveries({ 'run-1': finished({ run_id: 'run-1' }), other: job({ run_id: 'other', state: 'running' }) })

  await w.clock.advance(POLL_MS * 2)

  expect(w.submits.length).toBe(1)
  expect(w.toasts.filter((text) => text.startsWith('job run-1 finished')).length).toBe(1)
  expect((w.store.get(queueKey()) as any[]).map((entry) => entry.run_id)).toEqual(['other'])
})

test('the notice names the run by its scenario when the binary reports no purpose', async ($, on) => {
  const { purpose, ...older } = finished()
  const w = queuedWorld(on, { 'run-1': { ...older, scenario: 'code-review' } })
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.submits[0]!.startsWith('msgate job run-1 (code-review, conversation conv-1) finished: ok')).toBe(true)
})
