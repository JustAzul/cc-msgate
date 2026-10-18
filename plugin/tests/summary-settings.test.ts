import { expect, test } from 'claude-code/testing'
import { codex, msg, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const

// row starts the agent of an msgate type over a run of `slices` and reads its row.
async function row($: any, w: ReturnType<typeof world>, slices: { kind: string; text: string }[][]) {
  w.respond = codex(slices)
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  return step($, 'ag-1')
}

test('the live label is asked for again after the number of entries the option gives', { options: { liveSummaryEvery: 2, recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  await row($, w, [[msg('e1')], [msg('e2')], [msg('e3')], [msg('e4')], []])

  expect(w.modelRequests.map((r) => r.prompt)).toEqual([
    'Newest activity of the agent, oldest first:\n- e1',
    'Newest activity of the agent, oldest first:\n- e2\n- e3\n\nPrevious label: Reading auth.go (say something NEW).',
  ])
})

test('an option that is not a whole number in range falls back to its default, with one note in the debug log', { options: { liveSummaryEvery: 0, recordedSummaryEvery: 2.5 } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  await row($, w, [[msg('e1'), msg('e2'), msg('e3'), msg('e4')], []])
  w.agents.push({ id: 'ag-2', type: 'msgate-agents:second-opinion' })
  w.messages['ag-2'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  w.respond = codex([[msg('f1')], []])
  await step($, 'ag-2')

  expect(w.debugLogs.filter((text) => text.includes('must be an integer'))).toEqual([
    'liveSummaryEvery must be an integer from 1 to 10; 3 applies',
    'recordedSummaryEvery must be an integer from 0 to 24; 8 applies',
  ])
})

// The flag in the environment against the summaries option: `labelled` is what
// the row shows of a run with one entry.
async function labelled($: any, w: ReturnType<typeof world>) {
  w.surfaces = ['terminal']
  const out = await row($, w, [[msg('I will read auth.go')], []])
  return { requests: w.modelRequests.length, lines: out.chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim()) }
}

test('MSGATE_SUMMARIES=0 turns the summaries off although the option has them on', { options: { summaries: true } }, async ($, on) => {
  const w = world(on)
  w.env.set('MSGATE_SUMMARIES', '0')

  const shown = await labelled($, w)

  expect(shown.requests).toBe(0)
  expect(shown.lines).toContain('I will read auth.go')
})

test('MSGATE_SUMMARIES=1 turns the summaries on although the option has them off', { options: { summaries: false, recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.env.set('MSGATE_SUMMARIES', '1')

  const shown = await labelled($, w)

  expect(shown.requests).toBe(1)
  expect(shown.lines).toContain('» Reading auth.go')
})

test('without the variable the option decides', { options: { summaries: false } }, async ($, on) => {
  const w = world(on)

  const shown = await labelled($, w)

  expect(shown.requests).toBe(0)
  expect(shown.lines).toContain('I will read auth.go')
})

test('a value that is not 0 or 1 leaves the option in force and is noted once', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.env.set('MSGATE_SUMMARIES', 'abc')

  const shown = await labelled($, w)

  expect(shown.requests).toBe(1)
  expect(w.debugLogs.filter((text) => text.includes('MSGATE_SUMMARIES'))).toEqual(['MSGATE_SUMMARIES=abc is not 0 or 1; the summaries option applies'])
})

test('an environment that cannot be read leaves the option in force', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.envFails = true

  const shown = await labelled($, w)

  expect(shown.requests).toBe(1)
  expect(shown.lines).toContain('» Reading auth.go')
})

// With the summaries off the row shows what the provider emitted and nothing of
// the mod's own: no opening line and no heartbeat.

const thinkingOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())

test('with the summaries off a quiet row shows the entries of the run and no line of the mod', { options: { summaries: false } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = codex([[msg('e1')], [], [], []])
  w.respond = async (argv) => {
    // Each slice takes thirty seconds on the clock.
    if (argv[1] === 'wait') await w.clock.advance(30000)
    return run(argv)
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  const out = await step($, 'ag-1')

  expect(thinkingOf(out.chunks)).toEqual(['e1'])
  expect(w.modelRequests).toEqual([])
})

test('with the summaries off a run without entries streams no thinking at all', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.env.set('MSGATE_SUMMARIES', '0')
  const out = await row($, w, [[], []])

  expect(thinkingOf(out.chunks)).toEqual([])
  expect(out.text).toContain('Codex: done.')
})

test('with the summaries on and no surface a quiet row keeps its opening line and its heartbeat, and asks for nothing', async ($, on) => {
  const w = world(on)
  const run = codex([[msg('e1')], [], [], []])
  w.respond = async (argv) => {
    // Each slice takes thirty seconds on the clock.
    if (argv[1] === 'wait') await w.clock.advance(30000)
    return run(argv)
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  const out = await step($, 'ag-1')

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', 'e1', 'still working · 1m 0s', 'still working · 1m 30s', 'still working · 2m 0s', 'still working · 2m 30s'])
  expect(w.modelRequests).toEqual([])
})
