import { expect, test } from 'claude-code/testing'
import { DAY_MS, POLL_MS, TOOL, deliveries, job, ok, queueKey, queued, seeded, statusReads, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ADMITTED = { conversation_id: 'conv-1', run_id: 'run-1', state: 'running', run_dir: '/s/runs/run-1', output_path: '/s/runs/run-1/output.md' }
const done = (id: string) => job({ run_id: id, state: 'done', outcome: 'ok' })

// The time of the mock clock, read off an admission.
async function clockNow($: any, w: any) {
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'probe' }))
  await $.tool.call({ tool: TOOL('run'), prompt: 'probe' })
  const now = (w.store.get(queueKey()) as any[])[0].admitted_at as number
  w.store.delete(queueKey())
  return now
}

test('starting the session again, as a reload does, neither repeats a notice nor doubles the poll', async ($, on) => {
  const w = world(on, { store: seeded([queued('run-1'), queued('run-2', { admitted_at: 2_000 })]) })
  w.respond = deliveries({ 'run-1': done('run-1'), 'run-2': job({ run_id: 'run-2', state: 'running' }) })
  await $.session.start(START)
  await w.clock.advance(POLL_MS)
  expect(w.submits.length).toBe(1)

  await $.session.start(START)
  const polls = () => w.runs.filter((r) => r.argv[1] === 'status' && r.argv[2] === '--json').length
  const before = polls()
  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(1)
  // One poll, not two, and the three spawn reads of the period.
  expect(polls() - before).toBe(4)
  expect(statusReads(w).filter((id) => id === 'run-2').length).toBe(2)
})

test('an entry whose delivery was tried before the load is marked as possibly repeated', async ($, on) => {
  const w = world(on, { store: seeded([queued('run-1', { attempted_at: 500 }), queued('run-2', { admitted_at: 2_000 })]) })
  w.respond = deliveries({ 'run-1': done('run-1'), 'run-2': done('run-2') })
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.submits[0]!.split('\n')[0]).toBe('msgate job run-1 (adhoc, conversation conv-1) finished: ok (possibly repeated)')
  expect(w.submits[1]!.split('\n')[0]).toBe('msgate job run-2 (adhoc, conversation conv-1) finished: ok')
  expect(w.store.has(queueKey())).toBe(false)
})

test('an entry tried before the load but still running is still marked when it ends later in the load', async ($, on) => {
  const w = world(on, { store: seeded([queued('run-1', { attempted_at: 500 })]) })
  let state = 'running'
  w.respond = (argv, init) => deliveries({ 'run-1': job({ run_id: 'run-1', state, outcome: 'ok' }) })(argv, init)
  await $.session.start(START)
  await w.clock.advance(POLL_MS)
  expect(w.submits).toEqual([])

  state = 'done'
  await w.clock.advance(POLL_MS)

  expect(w.submits[0]!.split('\n')[0]).toContain('(possibly repeated)')
})

test('the poll neither reads nor writes the set of announced jobs of an earlier version', async ($, on) => {
  const w = world(on, { store: { notified: ['old'] } })
  w.respond = deliveries({})
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 2)

  expect(w.store.get('notified')).toEqual(['old'])
  expect(w.storeWrites.filter((write) => write.key === 'notified')).toEqual([])
})

test('two sessions keep two keys and each delivers only its own jobs', async ($, on) => {
  const w = world(on, { store: { ...seeded([queued('a-1')], 'sess-1'), ...seeded([queued('b-1')], 'sess-2') } })
  w.respond = deliveries({ 'a-1': done('a-1'), 'b-1': done('b-1') })
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(statusReads(w)).toEqual(['a-1'])
  expect(w.submits.length).toBe(1)
  expect(w.store.has(queueKey('sess-1'))).toBe(false)
  expect((w.store.get(queueKey('sess-2')) as any[]).map((entry) => entry.run_id)).toEqual(['b-1'])

  w.sessionId = 'sess-2'
  await w.clock.advance(POLL_MS)

  expect(statusReads(w)).toEqual(['a-1', 'b-1'])
  expect(w.submits.length).toBe(2)
  expect(w.store.has(queueKey('sess-2'))).toBe(false)
})

test('an admission lands in the key of its own session', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  w.sessionId = 'sess-9'
  await $.session.start(START)

  await $.tool.call({ tool: TOOL('run'), prompt: 'p' })

  expect([...w.store.keys()]).toEqual([queueKey('sess-9')])
})

test('a session start removes the queue of another session untouched for over 30 days and logs the count once', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  const now = await clockNow($, w)
  w.store.set(queueKey('old'), [queued('o-1', { admitted_at: now - 31 * DAY_MS }), queued('o-2', { admitted_at: now - 40 * DAY_MS })])
  w.store.set(queueKey('recent'), [queued('r-1', { admitted_at: now - 2 * DAY_MS }), queued('r-0', { admitted_at: now - 90 * DAY_MS })])
  w.store.set(queueKey('old-too'), [queued('o-3', { admitted_at: now - 45 * DAY_MS })])
  w.store.set(queueKey(), [queued('mine', { admitted_at: now - 60 * DAY_MS })])
  w.respond = deliveries({})

  await $.session.start(START)

  expect([...w.store.keys()].sort()).toEqual([queueKey('recent'), queueKey()].sort())
  expect(w.debugLogs.filter((line) => line.includes('abandoned') && line.includes('2'))).toHaveLength(1)
})
