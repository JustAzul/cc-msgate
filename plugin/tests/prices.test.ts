import { expect, test } from 'claude-code/testing'
import { POLL_MS, job, listing, ok, pricesRuns, world } from './kit'
import type { Reply } from './kit'
import { costText } from '../hooks/subagent.js'
import { formatCost, formatLowerBound, refreshOutcome } from '../hooks/prices.js'

const stats = (cost: string | null, complete = true) => JSON.stringify({ usage_totals: { cost_usd: cost, cost_complete: complete, runs_with_usage: 1 } })

test('a cost rounds half up to cents on the decimal string, and a lower bound truncates', () => {
  const table: [string, boolean, string][] = [
    ['52.940726', true, 'Codex $52.94'],
    ['1.005000', true, 'Codex $1.01'],
    ['0.005000', true, 'Codex $0.01'],
    ['0.004999', true, 'Codex <$0.01'],
    ['0.000000', true, 'Codex $0.00'],
    ['52.949999', false, 'Codex ≥$52.94'],
    ['0.004999', false, 'Codex ≥$0.00'],
  ]
  for (const [cost, complete, line] of table) expect(costText(stats(cost, complete))).toBe(line)
  // No price for any run: nothing to show for Codex.
  expect(costText(stats(null))).toBeUndefined()
})

const provider = (cost: string | null, complete = true) => ({ cost_usd: cost, cost_complete: complete, runs_with_usage: 1 })
const byProvider = (usage: Record<string, unknown>) => JSON.stringify({ usage_by_provider: usage })

test('the Codex and Claude costs show side by side, Codex first', () => {
  expect(costText(byProvider({ anthropic: provider('0.030000'), codex: provider('5.820000') }))).toBe('Codex $5.82 | Claude $0.03')
})

test('Claude shows alone when Codex has no cost', () => {
  expect(costText(byProvider({ codex: provider(null), anthropic: provider('0.030000') }))).toBe('Claude $0.03')
  expect(costText(byProvider({ anthropic: provider('0.030000') }))).toBe('Claude $0.03')
})

test('Claude is left out until it has a cost', () => {
  expect(costText(byProvider({ codex: provider('5.820000') }))).toBe('Codex $5.82')
  expect(costText(byProvider({ codex: provider('5.820000'), anthropic: provider(null) }))).toBe('Codex $5.82')
  expect(costText(byProvider({ codex: provider(null), anthropic: provider(null) }))).toBeUndefined()
})

test('each provider cost follows the same rounding and lower-bound rules', () => {
  expect(costText(byProvider({ codex: provider('0.004000'), anthropic: provider('0.000000') }))).toBe('Codex <$0.01 | Claude $0.00')
  expect(costText(byProvider({ codex: provider('5.829999', false), anthropic: provider('0.039999', false) }))).toBe('Codex ≥$5.82 | Claude ≥$0.03')
})

test('an answer without usage_by_provider takes the Codex cost from usage_totals', () => {
  expect(costText(JSON.stringify({ usage_totals: provider('0.680000') }))).toBe('Codex $0.68')
  // Present, it decides: usage_totals does not stand in for a missing codex entry.
  expect(costText(JSON.stringify({ usage_totals: provider('0.680000'), usage_by_provider: { anthropic: provider('0.030000') } }))).toBe('Claude $0.03')
})

test('a cost too large for a float still rounds exactly', () => {
  expect(formatCost('9007199254740993.995000')).toBe('$9007199254740994.00')
  expect(formatLowerBound('9007199254740993.999999')).toBe('≥$9007199254740993.99')
})

test('a cost that is not a decimal string shows nothing', () => {
  for (const bad of [null, undefined, '', 'abc', '-1.000000', '1e-7', 5, '1.2.3']) {
    expect(formatCost(bad as any)).toBeUndefined()
    expect(formatLowerBound(bad as any)).toBeUndefined()
  }
})

test('a refresh report that is not the expected object counts as a failure', () => {
  for (const stdout of ['{}', '[]', '"ok"', 'null', '{"ran":"true","changed":false,"reason":"updated"}', '{"ran":true,"changed":1,"reason":"updated"}', '{"ran":true,"changed":false}']) {
    expect(refreshOutcome({ exitCode: 0, stdout, stderr: '' }).failure).not.toBeNull()
  }
  expect(refreshOutcome({ exitCode: 0, stdout: '{"ran":true,"changed":true,"reason":"updated"}', stderr: '' })).toEqual({ failure: null, hasChanged: true })
  expect(refreshOutcome({ exitCode: 0, stdout: '{"ran":false,"changed":false,"reason":"fresh"}', stderr: '' })).toEqual({ failure: null, hasChanged: false })
})

// The refresh tests below use the world's fake msgate (kit.ts).

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const SHIM = (argv: string[]) => argv[0]!.endsWith('/bin/msgate')

function report(over: Record<string, unknown> = {}) {
  return ok(JSON.stringify({ sdk_status: 'ok', exit_code: 0, ran: true, changed: false, reason: 'refreshed', ...over }))
}

const UNAVAILABLE = { exitCode: 7, stdout: JSON.stringify({ sdk_status: 'prices_unavailable', exit_code: 7, ran: false, reason: 'unavailable', error: 'the price list answered 500' }), stderr: '' }

// held makes every refresh wait until the test answers it: pending[i](reply)
// ends the i-th refresh the mod started.
function held(w: ReturnType<typeof world>) {
  const pending: ((reply: Reply) => void)[] = []
  w.respondPrices = () => new Promise<Reply>((resolve) => pending.push(resolve))
  return pending
}

const refreshArgs = (w: ReturnType<typeof world>) => pricesRuns(w).map((r) => r.argv.slice(1))
const COMPACT = { messages: [{ role: 'user', text: 'hello', toolUses: [] }] } as any

test('session start schedules one refresh with a day of max age, from a timer and not inside the hook', async ($, on) => {
  const w = world(on)
  await $.session.start(START)

  expect(pricesRuns(w)).toEqual([])
  await w.clock.advance(0)

  expect(refreshArgs(w)).toEqual([['prices', 'refresh', '--max-age', '24h', '--json']])
  expect(SHIM(pricesRuns(w)[0]!.argv)).toBe(true)
})

test('a manual or automatic compaction of the main conversation schedules a refresh without max age and does not wait for it', async ($, on) => {
  const w = world(on)
  const pending = held(w)
  await $.session.start(START)
  await w.clock.advance(0)
  pending[0]!(report())
  await w.clock.advance(0)

  for (const trigger of ['manual', 'auto'] as const) {
    const before = pricesRuns(w).length
    // The compaction is answered by what is beneath the plugin, at once.
    const done = await $.session.compact({ ...COMPACT, trigger })
    expect(done).toMatchObject({ messages: [{ role: 'user', text: 'a summary' }] })
    expect(pricesRuns(w).length).toBe(before)
    await w.clock.advance(0)
    expect(pricesRuns(w).length).toBe(before + 1)
    expect(pricesRuns(w).at(-1)!.argv.slice(1)).toEqual(['prices', 'refresh', '--json'])
    pending.at(-1)!(report())
    await w.clock.advance(0)
  }
})

test('a compaction by a plugin or ahead of time, and any compaction of a subagent, schedules no refresh', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.clock.advance(0)
  const before = pricesRuns(w).length

  await $.session.compact({ ...COMPACT, trigger: 'precompute' })
  await $.session.compact({ ...COMPACT, trigger: 'plugin' })
  await $.session.compact({ ...COMPACT, trigger: 'manual', agentId: 'ag-1' })
  await $.session.compact({ ...COMPACT, trigger: 'auto', agentId: 'ag-1' })
  await w.clock.advance(0)

  expect(pricesRuns(w).length).toBe(before)
})

test('a compaction while a refresh runs starts no second process; one queued refresh without max age starts when it ends, even if it found nothing to do', async ($, on) => {
  const w = world(on)
  const pending = held(w)
  await $.session.start(START)
  await w.clock.advance(0)
  expect(refreshArgs(w)).toEqual([['prices', 'refresh', '--max-age', '24h', '--json']])

  await $.session.compact({ ...COMPACT, trigger: 'manual' })
  await w.clock.advance(60000)
  expect(pricesRuns(w).length).toBe(1)

  pending[0]!(report({ ran: false, reason: 'fresh' }))
  await w.clock.advance(0)

  expect(refreshArgs(w)).toEqual([
    ['prices', 'refresh', '--max-age', '24h', '--json'],
    ['prices', 'refresh', '--json'],
  ])
})

test('requests that arrive while a refresh runs coalesce into one queued refresh', async ($, on) => {
  const w = world(on)
  const pending = held(w)
  w.respond = (argv) => {
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: { cost_usd: '0.1', cost_complete: false }, missing_prices: ['m-new'] }))
    return listing(job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' }))
  }
  await $.session.start(START)
  await w.clock.advance(0)

  await $.session.compact({ ...COMPACT, trigger: 'manual' })
  await $.session.compact({ ...COMPACT, trigger: 'auto' })
  // The poll reads the stats, which name a model with no price.
  await w.clock.advance(POLL_MS)
  expect(pricesRuns(w).length).toBe(1)

  pending[0]!(report())
  await w.clock.advance(0)
  expect(pricesRuns(w).length).toBe(2)
  pending[1]!(report())
  await w.clock.advance(POLL_MS * 3)

  expect(refreshArgs(w)).toEqual([
    ['prices', 'refresh', '--max-age', '24h', '--json'],
    ['prices', 'refresh', '--json'],
  ])
})

test('a request with max age merged into a queued one without keeps it without', async ($, on) => {
  const w = world(on)
  const pending = held(w)
  await $.session.start(START)
  await w.clock.advance(0)
  await $.session.compact({ ...COMPACT, trigger: 'manual' })
  // A reload while the refresh runs asks for one with max age again.
  await $.session.start(START)
  await w.clock.advance(0)

  pending[0]!(report())
  await w.clock.advance(0)

  expect(refreshArgs(w).slice(1)).toEqual([['prices', 'refresh', '--json']])
})

function sessionWithMissing(w: ReturnType<typeof world>, missing: () => string[]) {
  w.respond = (argv) => {
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: { cost_usd: '0.5', cost_complete: false }, missing_prices: missing() }))
    return listing(job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' }), job({ run_id: 'b', state: 'done', outcome: 'ok', source: 'hook-stop' }))
  }
}

test('a model with no price asks for one refresh without max age, once per model in this load', async ($, on) => {
  const w = world(on)
  sessionWithMissing(w, () => ['m-new'])
  await $.session.start(START)
  await w.clock.advance(0)

  // The stats keep naming m-new on every read.
  await w.clock.advance(POLL_MS * 4)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBeGreaterThanOrEqual(2)

  expect(refreshArgs(w)).toEqual([
    ['prices', 'refresh', '--max-age', '24h', '--json'],
    ['prices', 'refresh', '--json'],
  ])
})

test('a second model with no price asks again', async ($, on) => {
  const w = world(on)
  let missing = ['m-new']
  sessionWithMissing(w, () => missing)
  await $.session.start(START)
  await w.clock.advance(POLL_MS)
  await w.clock.advance(POLL_MS)
  expect(pricesRuns(w).length).toBe(2)

  missing = ['m-new', 'm-other']
  w.respond = (argv) => {
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: { cost_usd: null, cost_complete: false }, missing_prices: missing }))
    return listing(job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' }), job({ run_id: 'b', state: 'done', outcome: 'ok', source: 'hook-stop' }), job({ run_id: 'c', state: 'done', outcome: 'ok', source: 'hook-stop' }))
  }
  await w.clock.advance(POLL_MS)

  expect(pricesRuns(w).length).toBe(3)
})

test('a refresh that changed the cache makes the next poll read the cost again, though no run ended', async ($, on) => {
  const w = world(on)
  w.respondPrices = () => report({ ran: true, changed: true })
  w.respond = (argv) => (argv[1] === 'stats' ? ok(JSON.stringify({ usage_totals: {} })) : listing())
  await $.session.start(START)
  await w.clock.advance(0)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(0)

  await w.clock.advance(POLL_MS)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(1)

  // Only that one poll: nothing changed since.
  await w.clock.advance(POLL_MS * 2)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(1)
})

test('a refresh that found nothing new does not make the next poll read the cost', async ($, on) => {
  const w = world(on)
  w.respondPrices = () => report({ ran: true, changed: false, reason: 'not_modified' })
  w.respond = (argv) => (argv[1] === 'stats' ? ok(JSON.stringify({ usage_totals: {} })) : listing())
  await $.session.start(START)
  await w.clock.advance(0)

  await w.clock.advance(POLL_MS * 2)

  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(0)
})

test('a queued refresh survives a refresh that changed nothing, and a change it finds is read on the next poll', async ($, on) => {
  const w = world(on)
  const pending = held(w)
  sessionWithMissing(w, () => ['m-new'])
  const statsReads = () => w.runs.filter((r) => r.argv[1] === 'stats').length
  await $.session.start(START)
  await w.clock.advance(0)

  // m-new arrives while the first refresh, which will change nothing, runs.
  await w.clock.advance(POLL_MS)
  expect(statsReads()).toBe(1)
  expect(pricesRuns(w).length).toBe(1)
  pending[0]!(report({ ran: true, changed: false, reason: 'not_modified' }))
  await w.clock.advance(0)
  expect(refreshArgs(w).at(-1)).toEqual(['prices', 'refresh', '--json'])

  // The late read of the run that ended, then quiet.
  await w.clock.advance(POLL_MS * 3)
  expect(statsReads()).toBe(2)

  pending[1]!(report({ ran: true, changed: true }))
  await w.clock.advance(0)
  await w.clock.advance(POLL_MS)
  expect(statsReads()).toBe(3)
  expect(pricesRuns(w).length).toBe(2)
})

test('a refresh that fails is logged once until one succeeds', async ($, on) => {
  const w = world(on)
  const answers: Reply[] = [UNAVAILABLE, UNAVAILABLE, report(), UNAVAILABLE]
  w.respondPrices = () => answers.shift()!
  const logged = () => w.logs.filter((l) => l.includes('could not refresh the price list'))
  await $.session.start(START)
  await w.clock.advance(0)
  expect(logged().length).toBe(1)
  expect(logged()[0]).toContain('the price list answered 500')

  await $.session.compact({ ...COMPACT, trigger: 'manual' })
  await w.clock.advance(0)
  expect(pricesRuns(w).length).toBe(2)
  expect(logged().length).toBe(1)

  await $.session.compact({ ...COMPACT, trigger: 'manual' })
  await w.clock.advance(0)
  await $.session.compact({ ...COMPACT, trigger: 'manual' })
  await w.clock.advance(0)
  expect(pricesRuns(w).length).toBe(4)
  expect(logged().length).toBe(2)
})

test('a refresh that cannot run is logged and does not stop the next one', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respondPrices = () => {
    n += 1
    if (n === 1) throw new Error('process timed out')
    return report()
  }
  await $.session.start(START)
  await w.clock.advance(0)
  expect(w.logs.filter((l) => l.includes('could not refresh the price list')).length).toBe(1)

  await $.session.compact({ ...COMPACT, trigger: 'auto' })
  await w.clock.advance(0)

  expect(pricesRuns(w).length).toBe(2)
})

test('each load of the mod refreshes at its start with the same max age', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.clock.advance(0)
  await $.session.start(START)
  await w.clock.advance(0)
  await $.session.start(START)
  await w.clock.advance(0)

  expect(refreshArgs(w)).toEqual([
    ['prices', 'refresh', '--max-age', '24h', '--json'],
    ['prices', 'refresh', '--max-age', '24h', '--json'],
    ['prices', 'refresh', '--max-age', '24h', '--json'],
  ])
})

test('a cost read that throws keeps the price change for the next poll', async ($, on) => {
  const w = world(on)
  w.respondPrices = () => report({ changed: true })
  let statsCalls = 0
  w.respond = (argv) => {
    if (argv[1] === 'stats') {
      statsCalls += 1
      if (statsCalls === 1) throw new Error('msgate vanished')
      return ok(stats('2.000000'))
    }
    return listing()
  }
  await $.session.start(START)
  await w.clock.advance(0)
  await w.clock.advance(POLL_MS)
  await w.clock.advance(POLL_MS)

  expect(statsCalls).toBe(2)
  expect(w.statuses.at(-1)).toBe('💸 Codex $2.00')
})
