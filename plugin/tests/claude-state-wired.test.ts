import { expect, test } from 'claude-code/testing'
import { answered, apiError, cmd, codex, isShim, msg, ok, step, world } from './kit'
import type { Reply } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const LIVE_ONLY = { options: { recordedSummaryEvery: 0 } }
const isMark = (argv: string[]) => isShim(argv[0]!) && argv[1] === 'providers' && argv[2] === 'mark'
const marks = (w: ReturnType<typeof world>) => w.runs.filter((r) => isMark(r.argv))
const RESET = '2099-10-23T18:00:00.000Z'

// summarise runs the agent of an msgate type over one entry, so that one live
// summary is asked for, with `providers mark` answered by `marking`.
async function summarise($: any, on: any, marking: (argv: string[]) => Reply = () => ok('')) {
  const w = world(on)
  const run = codex([[msg('I will read auth.go'), cmd('rg -n token auth.go')], []])
  w.surfaces = ['terminal']
  w.respond = (argv) => (isMark(argv) ? marking(argv) : run(argv))
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  return w
}

// The windows the session reports.
function reportWindows(on: any, rateLimits: unknown[]) {
  on('session.usage', () => ({ value: { startedAt: 0, context: {}, rateLimits, cost: undefined } }))
}

test('a summary refused with a 429 marks anthropic rate-limited with no time', LIVE_ONLY, async ($, on) => {
  reportWindows(on, [])
  const w = await summarise($, on)
  w.respondModel = () => apiError(429)

  await step($, 'ag-1')

  expect(marks(w).map((r) => r.argv.slice(1))).toEqual([['providers', 'mark', 'anthropic', '--reason', 'rate-limited']])
})

test('a summary refused for another reason marks nothing', LIVE_ONLY, async ($, on) => {
  reportWindows(on, [])
  const w = await summarise($, on)
  w.respondModel = () => apiError(500)

  await step($, 'ag-1')

  expect(w.modelRequests).toHaveLength(1)
  expect(marks(w)).toEqual([])
})

test('a window at 100 percent is marked before the summary request goes out, with its reset', LIVE_ONLY, async ($, on) => {
  reportWindows(on, [{ kind: 'five_hour', percentUsed: 100, resetsAt: RESET }])
  const w = await summarise($, on)
  const order: string[] = []
  const answer = w.respond
  w.respond = (argv) => {
    if (isMark(argv)) order.push('mark')
    return answer(argv, {})
  }
  w.respondModel = () => {
    order.push('model')
    return answered('Reading auth.go')
  }

  await step($, 'ag-1')

  expect(marks(w).map((r) => r.argv.slice(1))).toEqual([['providers', 'mark', 'anthropic', '--reason', 'rate-limited', '--until', RESET]])
  expect(order).toEqual(['mark', 'model'])
})

test('windows below 100 percent mark nothing', LIVE_ONLY, async ($, on) => {
  reportWindows(on, [{ kind: 'five_hour', percentUsed: 99, resetsAt: RESET }])
  const w = await summarise($, on)

  await step($, 'ag-1')

  expect(w.modelRequests).toHaveLength(1)
  expect(marks(w)).toEqual([])
})

test('a mark that cannot be written is reported once at debug level and the label still settles', LIVE_ONLY, async ($, on) => {
  reportWindows(on, [{ kind: 'five_hour', percentUsed: 100, resetsAt: RESET }])
  const w = await summarise($, on, () => ({ exitCode: 7, stdout: '', stderr: 'msgate: cannot write the cache' }))
  w.respondModel = () => answered('Reading auth.go')

  const out = await step($, 'ag-1')

  expect(marks(w)).toHaveLength(1)
  // The mark is the only thing in this session that reports at debug level.
  expect(w.debugLogs).toHaveLength(1)
  expect(out.chunks.filter((c: any) => c.kind === 'thinking').map((c: any) => c.text.trim())).toContain('» Reading auth.go')
})

test('the mark command carries its own timeout', LIVE_ONLY, async ($, on) => {
  reportWindows(on, [])
  const w = await summarise($, on)
  w.respondModel = () => apiError(429)

  await step($, 'ag-1')

  expect(marks(w).map((r) => r.timeoutMs)).toEqual([5000])
})
