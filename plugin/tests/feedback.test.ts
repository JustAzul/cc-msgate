import { expect, test } from 'claude-code/testing'
import { POLL_MS, SESSION, job, listing, ok, step, world } from './kit'
import { compactCount, heartbeatLine } from '../hooks/subagent.js'
import { BAND_REFRESH_MS, bandLine, withProgress } from '../hooks/band.js'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const

function progress(next: number, entries: { kind: string; text: string }[]) {
  return ok(JSON.stringify({ run_id: 'run-1', state: 'running', next, entries: entries.map((e, i) => ({ seq: next - entries.length + i, at: 't', ...e })) }))
}

function finishedRun(over: Record<string, unknown> = {}) {
  return ok(JSON.stringify({ run_id: 'run-1', conversation_id: 'conv-1', state: 'done', outcome: 'ok', output_path: '/o', ...over }))
}

const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }

function thinkingOf(chunks: any[]) {
  return chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())
}

test('what the run does streams into the agent row as thinking, then the answer', async ($, on) => {
  const w = world(on)
  let waits = 0
  const reads: string[] = []
  w.respond = (argv) => {
    if (argv[1] === 'exec') return ok(JSON.stringify({ conversation_id: 'conv-1', run_id: 'run-1' }))
    if (argv[1] === 'wait') return ++waits < 2 ? WAIT_TIMEOUT : finishedRun()
    if (argv[1] === 'progress') {
      reads.push(argv[4]!)
      return reads.length === 1 ? progress(2, [{ kind: 'message', text: 'I will read auth.go' }, { kind: 'command', text: 'rg -n token auth.go' }]) : progress(2, [])
    }
    if (argv[1] === 'status') return finishedRun()
    return ok('Codex: the token is never checked.')
  }
  w.agents.push({ id: 'ag-1', type: 'msgate:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', 'I will read auth.go', '$ rg -n token auth.go'])
  // Each read starts where the last one ended.
  expect(reads).toEqual(['0', '2'])
  expect(out.chunks.filter((c) => c.kind === 'thinking').every((c) => c.index === 0)).toBe(true)
  expect(out.chunks.find((c) => c.kind === 'tool')).toMatchObject({ index: 1, name: 'SubagentHandback' })
  expect(out.text).toContain('Codex: the token is never checked.')
})

test('a quiet run shows a heartbeat with the time it has been working', () => {
  expect(heartbeatLine(5000)).toBe('still working · 5s')
  expect(heartbeatLine(95000)).toBe('still working · 1m 35s')
  expect(compactCount(1408204)).toBe('1.4M')
  expect(compactCount(8900)).toBe('8.9k')
  expect(compactCount(999)).toBe('999')
})

// What the engine hands the band: a terminal 100 cells wide, no survey.
const BAND_PROPS = { hasSurvey: false, isWorking: false, maxRows: 10, bodyColumns: 100, scroll: { offset: 0, bodyRows: 9 } } as any

test('a run another caller flagged shows in the band with its latest step until it ends', async ($, on) => {
  const w = world(on)
  let hookState = 'running'
  const reads: string[] = []
  w.respond = (argv) => {
    if (argv[1] === 'progress') {
      reads.push(argv[4]!)
      const entries = argv[4] === '0' ? [{ seq: 0, at: 't', kind: 'message', text: 'I will read\n  auth.go' }, { seq: 1, at: 't', kind: 'command', text: 'rg -n token auth.go' }] : []
      return ok(JSON.stringify({ run_id: 'run-h', state: hookState, next: 2, entries }))
    }
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(
      job({ run_id: 'run-h', state: 'running', source: 'hook-post-commit', purpose: 'code-review', background: false, agent_feedback: true, started_at: '1970-01-01T00:00:00Z' }),
      job({ run_id: 'run-own', state: 'running', source: 'agent', agent_feedback: true }),
      job({ run_id: 'run-plain', state: 'running', source: 'cli' }),
    )
  }
  await $.session.start(START)
  await w.clock.advance(POLL_MS)

  // The agent types' own runs show as their agents, and an unflagged run not at all.
  for (const surface of ['desktop', 'vscode', 'mobile'] as const) {
    const elsewhere = await $.ui.mount({ plugin: 'msgate', surface, component: 'AbovePrompt', props: BAND_PROPS })
    expect((await elsewhere.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-post-commit · 15s · $ rg -n token auth.go'])
  }
  const band = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: BAND_PROPS })
  expect((await band.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-post-commit · 15s · $ rg -n token auth.go'])
  // A survey holding the band stays, under the line.
  const withSurvey = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: { ...BAND_PROPS, hasSurvey: true } })
  expect((await withSurvey.findAll({ type: 'Text' })).map((t) => t.text)).toEqual([
    'msgate · code-review · hook-post-commit · 15s · $ rg -n token auth.go',
    'How is Claude doing this session?',
  ])

  // Between polls the band reads on from where it stopped, and lets the run
  // go as soon as a read finds it ended.
  hookState = 'done'
  await w.clock.advance(BAND_REFRESH_MS)
  expect(reads[0]).toBe('0')
  expect(reads.slice(1).every((from) => from === '2')).toBe(true)
  expect(await band.findAll({ type: 'Text' })).toEqual([])
  // Only the person sees the band: nothing about the run reaches Claude.
  expect(w.submits).toEqual([])
  expect(w.spawns).toEqual([])
})

test('after a reload the band keeps what it showed until the first poll has read the runs', async ($, on) => {
  const w = world(on)
  const writes: unknown[] = []
  on('state.set', { plugin: 'msgate', key: 'hookRuns' }, (_$: any, e: any) => {
    writes.push(e.value)
    return { value: { isSet: true, version: writes.length } }
  })
  w.respond = (argv) => (argv[1] === 'stats' ? ok(JSON.stringify({ usage_totals: {} })) : listing())
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS * 2)
  expect(writes).toEqual([])

  await w.clock.advance(POLL_MS - BAND_REFRESH_MS * 2)
  expect(writes).toEqual([[]])
})

test('a band that cannot be written is logged once and written again on the next refresh', async ($, on) => {
  const w = world(on)
  let attempts = 0
  on('state.set', { plugin: 'msgate', key: 'hookRuns' }, () => {
    attempts += 1
    return { deny: 'state host unavailable' }
  })
  w.respond = (argv) => {
    if (argv[1] === 'progress') return ok(JSON.stringify({ run_id: 'run-h', state: 'running', next: 0, entries: [] }))
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(job({ run_id: 'run-h', state: 'running', source: 'hook-stop', agent_feedback: true }))
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS + BAND_REFRESH_MS * 3)

  expect(attempts).toBeGreaterThanOrEqual(4)
  expect(w.logs.filter((l) => l.includes('could not update the band')).length).toBe(1)
})

test('a band line names the run and shows its newest step on one line', () => {
  const run = { run_id: 'r', purpose: 'code-review', source: 'hook-stop', startedAt: null, from: 0, line: '' }
  expect(bandLine(run, 0)).toBe('msgate · code-review · hook-stop')
  expect(bandLine({ ...run, startedAt: 1000, line: 'Reading\n\n  the diff  ' }, 96000)).toBe('msgate · code-review · hook-stop · 1m 35s · Reading the diff')
})

test('a band line shows the run cost right after the elapsed time', () => {
  const run = { run_id: 'r', purpose: 'code-review', source: 'hook-stop', startedAt: 1000, from: 0, line: 'Reading the diff', cost: '0.123456' }
  expect(bandLine(run, 96000)).toBe('msgate · code-review · hook-stop · 1m 35s · $0.12 · Reading the diff')
  expect(bandLine({ ...run, cost: '0.004000' }, 96000)).toBe('msgate · code-review · hook-stop · 1m 35s · <$0.01 · Reading the diff')
  expect(bandLine({ ...run, cost: '0.000000' }, 96000)).toBe('msgate · code-review · hook-stop · 1m 35s · $0.00 · Reading the diff')
  expect(bandLine({ ...run, cost: null }, 96000)).toBe('msgate · code-review · hook-stop · 1m 35s · Reading the diff')
  expect(bandLine({ ...run, startedAt: null, line: '' }, 96000)).toBe('msgate · code-review · hook-stop · $0.12')
})

test("a refresh takes the newest read's cost, and a read that failed keeps the last one", () => {
  const run = { run_id: 'a', purpose: 's', source: 'hook', startedAt: null, from: 0, line: '', cost: '0.500000' }
  const read = (cost: string | null | undefined) => new Map([['a', { next: 1, text: [], state: 'running', cost }]])
  expect(withProgress([run], read('0.700000'))[0]!.cost).toBe('0.700000')
  expect(withProgress([run], read(null))[0]!.cost).toBeNull()
  expect(withProgress([run], read(undefined))[0]!.cost).toBe('0.500000')
})

test('a flagged run shows the cost its progress reports, and none while it reports null', async ($, on) => {
  const w = world(on)
  let cost: string | null = '0.123456'
  w.respond = (argv) => {
    if (argv[1] === 'progress') {
      const entries = argv[4] === '0' ? [{ seq: 0, at: 't', kind: 'message', text: 'Reading the diff' }] : []
      return ok(JSON.stringify({ run_id: 'run-h', state: 'running', next: 1, entries, usage: null, cost_usd: cost }))
    }
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(job({ run_id: 'run-h', state: 'running', source: 'hook-stop', purpose: 'code-review', background: false, agent_feedback: true, started_at: '1970-01-01T00:00:00Z' }))
  }
  await $.session.start(START)
  await w.clock.advance(POLL_MS)

  const band = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: BAND_PROPS })
  expect((await band.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-stop · 15s · $0.12 · Reading the diff'])

  cost = '0.004000'
  await w.clock.advance(BAND_REFRESH_MS)
  expect((await band.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-stop · 20s · <$0.01 · Reading the diff'])

  cost = null
  await w.clock.advance(BAND_REFRESH_MS)
  expect((await band.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-stop · 25s · Reading the diff'])
})

test('a refresh changes only the runs it read, so a run a poll added meanwhile stays', () => {
  const run = (id: string, line = '') => ({ run_id: id, purpose: 's', source: 'hook', startedAt: null, from: 0, line })
  const read = new Map([
    ['a', { next: 3, text: ['first', 'newest'], state: 'running' }],
    ['b', { next: 1, text: [], state: 'done' }],
    ['c', { next: 0, text: [], state: null }],
  ])
  expect(withProgress([run('a'), run('b'), run('c', 'kept'), run('new')], read)).toEqual([
    { ...run('a'), from: 3, line: 'newest' },
    { ...run('c', 'kept'), from: 0 },
    run('new'),
  ])
})

function stats(cost: string | null, complete = true) {
  return ok(JSON.stringify({ usage_totals: { input_tokens: 1408204, output_tokens: 7156, runs_with_usage: 1, cost_usd: cost, cost_complete: complete } }))
}

test("the status line adds the session's Codex cost, read again when a run ends", async ($, on) => {
  const w = world(on)
  let runs: any[] = [job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' }), job({ run_id: 'b', state: 'running' })]
  let cost = '0.680000'
  w.respond = (argv) => {
    if (argv[1] === 'stats') return stats(cost)
    return listing(...runs)
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.statuses.at(-1)).toBe('💸 1 job running · Codex $0.68')
  const read = w.runs.find((r) => r.argv[1] === 'stats')!
  expect(read.argv.slice(1)).toEqual(['stats', '--session-id', SESSION, '--all', '--json'])

  // Read once more on the next poll, in case the run's record landed late;
  // then not again until another run ends.
  await w.clock.advance(POLL_MS)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(2)
  await w.clock.advance(POLL_MS)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(2)

  runs = [job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' }), job({ run_id: 'b', state: 'done', outcome: 'ok' })]
  cost = '2.500000'
  w.respond = (argv) => {
    if (argv[1] === 'stats') return stats(cost)
    if (argv[1] === 'result') return ok('')
    return listing(...runs)
  }
  await w.clock.advance(POLL_MS)
  expect(w.runs.filter((r) => r.argv[1] === 'stats').length).toBe(3)
  expect(w.statuses.at(-1)).toBe('💸 Codex $2.50')
})

test('a session with nothing priced has no Codex part in the status line', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => {
    if (argv[1] === 'stats') return stats(null)
    return listing(job({ run_id: 'a', state: 'running' }), job({ run_id: 'b', state: 'done', outcome: 'ok', source: 'hook-stop' }))
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.statuses.at(-1)).toBe('💸 1 job running')
})

test('a cost that is only a lower bound shows as one in the status line', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'stats' ? stats('52.949999', false) : listing(job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' })))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.statuses.at(-1)).toBe('💸 Codex ≥$52.94')
})

test('a cost that cannot be read is read again on the next poll', async ($, on) => {
  const w = world(on)
  let fails = true
  w.respond = (argv) => {
    if (argv[1] === 'stats') return fails ? { exitCode: 70, stdout: '', stderr: 'msgate: telemetry locked' } : stats('0.004999')
    if (argv[1] === 'result') return ok('')
    return listing(job({ run_id: 'a', state: 'done', outcome: 'ok', source: 'hook-stop' }))
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  fails = false
  await w.clock.advance(POLL_MS)

  expect(w.statuses.at(-1)).toBe('💸 Codex <$0.01')
  expect(w.logs.filter((l) => l.includes('session cost')).length).toBe(1)
})

test('a progress read that fails leaves the run followed to its answer', async ($, on) => {
  const w = world(on)
  let waits = 0
  w.respond = (argv) => {
    if (argv[1] === 'exec') return ok(JSON.stringify({ conversation_id: 'conv-1', run_id: 'run-1' }))
    if (argv[1] === 'wait') return ++waits < 3 ? WAIT_TIMEOUT : finishedRun()
    if (argv[1] === 'progress') throw new Error('progress unavailable')
    if (argv[1] === 'status') return finishedRun()
    return ok('Codex answered anyway.')
  }
  w.agents.push({ id: 'ag-1', type: 'msgate:adhoc' })
  w.messages['ag-1'] = [{ role: 'user', text: 'q' }]
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(waits).toBe(3)
  expect(out.text).toContain('Codex answered anyway.')
  expect(out.text).not.toContain('hand-off to the agent failed')
  expect(w.runs.some((r) => r.argv[1] === 'cancel')).toBe(false)
  expect(w.logs.filter((l) => l.includes('could not read the progress of run run-1')).length).toBe(1)
})

test('a flagged run that reports its purpose as scenario shows under that label in the band', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => {
    if (argv[1] === 'progress') return ok(JSON.stringify({ run_id: 'run-h', state: 'running', next: 0, entries: [], usage: null, cost_usd: null }))
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(job({ run_id: 'run-h', state: 'running', source: 'hook-stop', purpose: undefined, scenario: 'code-review', background: false, agent_feedback: true, started_at: '1970-01-01T00:00:00Z' }))
  }
  await $.session.start(START)
  await w.clock.advance(POLL_MS)

  const band = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: BAND_PROPS })
  expect((await band.findAll({ type: 'Text' })).map((t) => t.text)).toEqual(['msgate · code-review · hook-stop · 15s'])
})
