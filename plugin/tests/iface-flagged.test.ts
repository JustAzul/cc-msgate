import { expect, test } from 'claude-code/testing'
import { SESSION, job, listing, msgateAs, world } from './kit'

const flagged = (over: Record<string, unknown> = {}) =>
  job({ run_id: 'run-hook', source: 'hook-stop', purpose: 'code-review', agent_feedback: true, started_at: '2026-01-01T00:00:05Z', ...over })

// The registry of hidden rows the engine keeps in the session's state.
const hide = (w: ReturnType<typeof world>, rows: Record<string, string>) => w.state.set('hiddenRows', { value: rows, version: 1 })

test('flaggedRuns returns the session\'s running runs that ask to be shown, and no others', async ($, on) => {
  const w = world(on)
  w.respond = () =>
    listing(
      flagged(),
      flagged({ run_id: 'run-agent', source: 'agent' }),
      flagged({ run_id: 'run-done', state: 'done', outcome: 'ok' }),
      flagged({ run_id: 'run-quiet', agent_feedback: false }),
    )

  const out = await msgateAs(w).flaggedRuns()

  expect(out).toEqual({ value: [{ run_id: 'run-hook', purpose: 'code-review', source: 'hook-stop', started_at: '2026-01-01T00:00:05Z' }] })
  expect(w.runs.map((r) => r.argv.slice(1))).toEqual([['status', '--json', '--session-id', SESSION]])
})

test('flaggedRuns leaves out a run that has a hidden row, whatever became of the row', async ($, on) => {
  const w = world(on)
  w.respond = () => listing(flagged(), flagged({ run_id: 'run-other' }))
  hide(w, { 'ag-1': 'run-hook' })

  const out: any = await msgateAs(w).flaggedRuns()

  expect(out.value.map((run: any) => run.run_id)).toEqual(['run-other'])
})

test('flaggedRuns answers the run\'s purpose as text and its start as null before it has one', async ($, on) => {
  const w = world(on)
  w.respond = () => listing(flagged({ purpose: undefined, started_at: null }))

  expect(await msgateAs(w).flaggedRuns()).toEqual({ value: [{ run_id: 'run-hook', purpose: '', source: 'hook-stop', started_at: null }] })
})

test('flaggedRuns is refused as msgate:flaggedRuns:failed when the listing cannot be read, and says why in the debug log', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 1, stdout: '', stderr: 'msgate: store is locked\n' })

  expect(await msgateAs(w).flaggedRuns()).toEqual({ deny: 'msgate:flaggedRuns:failed' })
  expect(w.debugLogs.join('\n')).toContain('store is locked')
})
