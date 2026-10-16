import { expect, test } from 'claude-code/testing'
import { BAND_REFRESH_MS } from '../hooks/band.js'
import { POLL_MS, answered, apiError, emptyReply, job, listing, ok, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }

const SYSTEM = [
  'You label what a coding agent is doing right now, for a one-line status row that truncates around 40 characters.',
  'Describe its most recent action in 3-5 words using present tense (-ing). Name the file, command or function, not the branch.',
  'Reply with the label only: one line, no quotes, no markdown, no final period.',
  '',
  'Good: "Reading workerlog.go"',
  'Good: "Running store tests"',
  'Good: "Reviewing the uncommitted diff"',
  'Bad (past tense): "Read workerlog.go"',
  'Bad (too vague): "Investigating the issue"',
  'Bad (too long): "Reviewing the full branch diff and the store package integration"',
].join('\n')

const msg = (text: string) => ({ kind: 'message', text })
const cmd = (text: string) => ({ kind: 'command', text })

// codex is a run that works through `slices`: each wait times out and the
// progress read after it answers the next slice's entries; the wait after the
// last slice finds the run done.
function codex(slices: { kind: string; text: string }[][]) {
  let waits = 0
  let reads = 0
  let next = 0
  return (argv: string[]) => {
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

function thinkingOf(chunks: any[]) {
  return chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())
}

// row starts the agent of an msgate type over a run of `slices` and reads its row.
async function row($: any, w: any, slices: { kind: string; text: string }[][]) {
  w.respond = codex(slices)
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  return step($, 'ag-1')
}

test('a headless session asks for no label and the row shows the raw lines', async ($, on) => {
  const w = world(on)
  const out = await row($, w, [[msg('I will read auth.go'), cmd('rg -n token auth.go')], []])

  expect(w.modelRequests).toEqual([])
  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', 'I will read auth.go', '$ rg -n token auth.go'])
})

test('with summaries turned off in the config the row shows the raw lines', { options: { summaries: false } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const out = await row($, w, [[msg('I will read auth.go')], []])

  expect(w.modelRequests).toEqual([])
  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', 'I will read auth.go'])
})

test('the first entry of a run asks for one label with the measured request', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  await row($, w, [[msg('I will read auth.go')], []])

  expect(w.modelRequests).toHaveLength(1)
  expect(w.modelRequests[0]).toMatchObject({
    model: 'claude-haiku-5-5',
    effort: 'low',
    system: SYSTEM,
    prompt: 'Newest activity of the agent, oldest first:\n- I will read auth.go',
    maxTokens: 40,
    timeoutMs: 15000,
  })
})

test('the row streams the label as thinking and no raw entry', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => answered('Reading auth.go')
  const out = await row($, w, [[msg('I will read auth.go'), cmd('rg -n token auth.go')], []])

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', '» Reading auth.go'])
  expect(out.text).toContain('Codex: done.')
})

// The window a request carries.

test('the next label is asked for only after three more entries, with the previous label in the prompt', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => answered('Reading auth.go')
  const requestsBeforeRead: number[] = []
  const run = codex([[msg('e1')], [msg('e2')], [msg('e3')], [msg('e4')], []])
  w.respond = (argv) => {
    if (argv[1] === 'progress') requestsBeforeRead.push(w.modelRequests.length)
    return run(argv)
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  await step($, 'ag-1')

  // One request for the first entry, one more once e2, e3 and e4 have arrived.
  expect(requestsBeforeRead).toEqual([0, 1, 1, 1, 2, 2])
  expect(w.modelRequests.map((r) => r.prompt)).toEqual([
    'Newest activity of the agent, oldest first:\n- e1',
    'Newest activity of the agent, oldest first:\n- e2\n- e3\n- e4\n\nPrevious label: Reading auth.go (say something NEW).',
  ])
})

test('a request carries the newest ten entries, each cut to 300 characters', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const entries = Array.from({ length: 12 }, (_, i) => msg(i === 11 ? 'n'.repeat(400) : 'entry ' + (i + 1)))
  await row($, w, [entries, []])

  const lines = w.modelRequests[0].prompt.split('\n')
  expect(lines.slice(1)).toEqual([...Array.from({ length: 9 }, (_, i) => '- entry ' + (i + 3)), '- ' + 'n'.repeat(300)].slice(0, 10))
})

test('a command entry is sent as the command line', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  await row($, w, [[cmd('rg -n token auth.go')], []])

  expect(w.modelRequests[0].prompt).toBe('Newest activity of the agent, oldest first:\n- $ rg -n token auth.go')
})

// How many requests are out at once.

function deferred() {
  let release!: (value: unknown) => void
  const promise = new Promise((resolve) => {
    release = resolve
  })
  return { promise, release }
}

test('a run has at most one request in flight', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const slow = deferred()
  w.respondModel = () => slow.promise
  await row($, w, [[msg('e1')], [msg('e2'), msg('e3'), msg('e4')], [msg('e5'), msg('e6'), msg('e7')], []])

  expect(w.modelRequests).toHaveLength(1)
  slow.release(answered('Reading auth.go'))
})

test('the session has at most two requests in flight', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const slow = deferred()
  w.respondModel = () => slow.promise
  const runs = new Map<string, ReturnType<typeof codex>>()
  let admitted = 0
  w.respond = (argv) => {
    if (argv[1] === 'exec') return ok(JSON.stringify({ conversation_id: 'conv-' + ++admitted, run_id: 'run-' + admitted }))
    const id = argv[2]!
    if (!runs.has(id)) runs.set(id, codex([[msg('first of ' + id)], [], []]))
    const reply = runs.get(id)!(argv)
    return argv[1] === 'wait' || argv[1] === 'progress' ? ok(reply.stdout.replace(/run-1/g, id)) : reply
  }
  for (const id of ['ag-1', 'ag-2', 'ag-3']) {
    w.agents.push({ id, type: 'msgate-agents:second-opinion' })
    w.messages[id] = [{ role: 'user', text: 'Is auth.go safe?' }]
  }
  await $.session.start(START)
  await Promise.all(['ag-1', 'ag-2', 'ag-3'].map((id) => step($, id)))

  expect(w.modelRequests).toHaveLength(2)
  slow.release(answered('Reading auth.go'))
})

// The band: the same cadence on the clock, read through what the person sees.

const BAND_PROPS = { hasSurvey: false, isWorking: false, maxRows: 10, bodyColumns: 200, scroll: { offset: 0, bodyRows: 9 } } as any

// flagged follows one run another caller flagged, whose progress entries the
// test appends to.
function flagged(w: any) {
  const entries: { kind: string; text: string }[] = []
  w.respond = (argv: string[]) => {
    if (argv[1] === 'progress') {
      const from = Number(argv[4])
      return ok(JSON.stringify({ run_id: 'run-h', state: 'running', next: entries.length, entries: entries.slice(from).map((e, i) => ({ seq: from + i, at: 't', ...e })) }))
    }
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(job({ run_id: 'run-h', state: 'running', source: 'hook-post-commit', purpose: 'code-review', background: false, agent_feedback: true, started_at: '1970-01-01T00:00:00Z' }))
  }
  return entries
}

async function bandText($: any) {
  const band = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: BAND_PROPS })
  return (await band.findAll({ type: 'Text' })).map((t: any) => t.text)
}

test('the band shows no step until the first label, then the label', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const slow = deferred()
  w.respondModel = () => slow.promise
  flagged(w).push(msg('I will read auth.go'), cmd('rg -n token auth.go'))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 15s'])

  slow.release(answered('Reading auth.go'))
  await w.clock.advance(BAND_REFRESH_MS)
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 20s · Reading auth.go'])
  expect(w.modelRequests).toHaveLength(1)
})

test('the band of an unlabelled run ends after its cost, with no step', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => deferred().promise
  const entries = flagged(w)
  const read = w.respond
  w.respond = (argv: string[]) => {
    const reply = read(argv, {})
    return argv[1] === 'progress' ? ok(JSON.stringify({ ...JSON.parse((reply as any).stdout), cost_usd: '0.123456' })) : reply
  }
  entries.push(msg('I will read auth.go'))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  await w.clock.advance(BAND_REFRESH_MS * 16)

  expect(w.modelRequests).toHaveLength(1)
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 1m 35s · $0.12'])
})

test('a summary that fails after a label settled leaves the band with that label', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const answers = [answered('Reading auth.go'), apiError(500)]
  w.respondModel = () => answers.shift() ?? apiError(500)
  const entries = flagged(w)
  entries.push(msg('e1'))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 20s · Reading auth.go'])

  entries.push(msg('e2'), msg('e3'), msg('e4'))
  await w.clock.advance(BAND_REFRESH_MS)
  expect(w.modelRequests).toHaveLength(2)
  await w.clock.advance(BAND_REFRESH_MS)

  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 30s · Reading auth.go'])
})

test('a headless session leaves the band with the raw newest step', async ($, on) => {
  const w = world(on)
  flagged(w).push(msg('I will read auth.go'))
  await $.session.start(START)

  await w.clock.advance(POLL_MS + BAND_REFRESH_MS)

  expect(w.modelRequests).toEqual([])
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 20s · I will read auth.go'])
})

test('a 429 pauses every request for a minute, then they resume', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  let answers = [apiError(429)]
  w.respondModel = () => answers.shift() ?? answered('Reading auth.go')
  const entries = flagged(w)
  entries.push(msg('e1'))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.modelRequests).toHaveLength(1)
  // The failed window leaves the band with no step.
  await w.clock.advance(BAND_REFRESH_MS)
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 20s'])

  entries.push(msg('e2'), msg('e3'), msg('e4'))
  // The pause runs from 15s to 75s.
  await w.clock.advance(50000)
  expect(w.modelRequests).toHaveLength(1)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(w.modelRequests).toHaveLength(2)
  expect(w.modelRequests[1].prompt).toBe('Newest activity of the agent, oldest first:\n- e2\n- e3\n- e4')
})

// What counts as a failure.

const REJECTED = {
  empty: '   ',
  'a second line': 'Reading auth.go\nthen the tests',
  'a heading': '# Reading auth.go',
  'a code span': '`auth.go`',
  'a bullet star': '* Reading auth.go',
  'a bullet dash': '- Reading auth.go',
  'a quote': '> Reading auth.go',
  'over 100 characters': 'r'.repeat(101),
}

for (const [name, reply] of Object.entries(REJECTED)) {
  test('a label that is ' + name + ' leaves the row with no raw entry', async ($, on) => {
    const w = world(on)
    w.surfaces = ['terminal']
    w.respondModel = () => answered(reply)
    const out = await row($, w, [[msg('first'), cmd('second')], []])

    expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion'])
    expect(w.logs.filter((l) => l.includes('could not summarize'))).toHaveLength(1)
  })
}

test('control and ANSI characters are stripped from the label', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => answered('  \u001b[1mReading\u001b[0m auth.go\u0007  ')
  const out = await row($, w, [[msg('first')], []])

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', '» Reading auth.go'])
})

test('a request that rejects leaves the row with no raw entry', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => {
    throw new Error('the engine refused the model')
  }
  const out = await row($, w, [[msg('first'), cmd('second')], []])

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion'])
  expect(w.logs.filter((l) => l.includes('could not summarize'))).toHaveLength(1)
  expect(out.text).toContain('Codex: done.')
})

for (const [name, failure] of [['an api error', apiError(500)], ['an empty reply', emptyReply()]] as const) {
  test(name + ' leaves the row with no raw entry, logged once per run', async ($, on) => {
    const w = world(on)
    w.surfaces = ['terminal']
    w.respondModel = () => failure
    const out = await row($, w, [[msg('a')], [msg('b'), msg('c'), msg('d')], [msg('e'), msg('f'), msg('g')], []])

    expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion'])
    expect(w.logs.filter((l) => l.includes('could not summarize'))).toHaveLength(1)
  })
}

test('a label that comes after a failure is streamed as a label', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const replies = [apiError(500), answered('Running store tests')]
  w.respondModel = () => replies.shift()
  const out = await row($, w, [[msg('a')], [msg('b'), msg('c'), msg('d')], []])

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', '» Running store tests'])
})

test('the row never waits for a request: the label streams on the first slice after it settles', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const slow = deferred()
  w.respondModel = () => slow.promise
  const chunksBySlice: string[][] = []
  const run = codex([[msg('e1')], [], [], []])
  w.respond = (argv) => {
    if (argv[1] === 'wait') chunksBySlice.push([])
    if (argv[1] === 'wait' && chunksBySlice.length === 3) slow.release(answered('Reading auth.go'))
    return run(argv)
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  const out = await step($, 'ag-1')

  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', '» Reading auth.go'])
  expect(out.text).toContain('Codex: done.')
})

test('a quiet row still shows the heartbeat while summaries are active', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => answered('Reading auth.go')
  const run = codex([[msg('e1')], [], [], []])
  let waits = 0
  w.respond = async (argv) => {
    // Each slice takes thirty seconds on the clock.
    if (argv[1] === 'wait') {
      waits += 1
      await w.clock.advance(30000)
    }
    return run(argv)
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  const out = await step($, 'ag-1')

  expect(waits).toBe(5)
  // Nothing is shown in the first slice, the label in the second; a slice that
  // shows nothing 30 seconds after the last line adds a heartbeat.
  expect(thinkingOf(out.chunks)).toEqual([
    'msgate · second-opinion',
    'still working · 30s',
    '» Reading auth.go',
    'still working · 1m 30s',
    'still working · 2m 0s',
    'still working · 2m 30s',
  ])
})

test('a request due while one is in flight is deferred, then made with the window as it stands', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const slow = deferred()
  const replies = [slow.promise, answered('Running store tests')]
  w.respondModel = () => replies.shift()
  let waits = 0
  const run = codex([[msg('e1')], [msg('e2'), msg('e3'), msg('e4')], [msg('e5')], [], []])
  w.respond = (argv) => {
    if (argv[1] === 'wait' && ++waits === 3) slow.release(answered('Reading auth.go'))
    return run(argv)
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  const out = await step($, 'ag-1')

  expect(w.modelRequests.map((r) => r.prompt)).toEqual([
    'Newest activity of the agent, oldest first:\n- e1',
    'Newest activity of the agent, oldest first:\n- e2\n- e3\n- e4\n- e5\n\nPrevious label: Reading auth.go (say something NEW).',
  ])
  expect(thinkingOf(out.chunks)).toEqual(['msgate · second-opinion', '» Reading auth.go', '» Running store tests'])
})
