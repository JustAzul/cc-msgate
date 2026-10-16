import { expect, test } from 'claude-code/testing'
import { POLL_MS, TOOL, isShim, job, listing, ok, step, world } from './kit'
import { BAND_REFRESH_MS } from '../hooks/band.js'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const

function eta(over: Record<string, unknown>) {
  return ok(JSON.stringify({ sdk_status: 'ok', exit_code: 0, purpose: 'code-review', repo: '/work/.git', ...over }))
}

const REPO_ETA = { basis: 'repo', eta_ms: 156000, samples: 12, repos: 1 }

const HOOK_RUN = { run_id: 'run-h', state: 'running', source: 'hook-post-commit', purpose: 'code-review', background: false }

const etaRuns = (w: { runs: { argv: string[] }[] }) => w.runs.filter((r) => r.argv[1] === 'eta')

test('a hook run seen running gets one toast and one transcript line with its repo ETA', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job(HOOK_RUN)))
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  const text = 'msgate · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)'
  expect(w.toasts).toEqual([text])
  expect(w.logs).toEqual([text])
  expect(etaRuns(w).map((r) => r.argv.slice(1))).toEqual([['eta', '--purpose', 'code-review', '--cwd', '/work', '--json']])
  expect(isShim(etaRuns(w)[0]!.argv[0]!)).toBe(true)
})

for (const field of ['purpose', 'source', 'state']) {
  test(`a listed run with no ${field} is skipped and logged, then announced once the listing has it`, async ($, on) => {
    const w = world(on)
    let runs = [job({ ...HOOK_RUN, [field]: undefined })]
    w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(...runs))
    await $.session.start(START)

    await w.clock.advance(BAND_REFRESH_MS)

    expect(w.toasts).toEqual([])
    expect(w.debugLogs).toEqual(['skipped run run-h in the spawn scan: the listing gives it no purpose, source or state'])

    runs = [job(HOOK_RUN)]
    await w.clock.advance(BAND_REFRESH_MS)

    expect(w.toasts).toEqual(['msgate · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)'])
  })
}

const ETA_TEXTS: [string, Record<string, unknown>, string][] = [
  ['the global average', { basis: 'global', eta_ms: 143000, samples: 20, repos: 8 }, 'ETA ~2m 23s (global average, 8 repos)'],
  ['no history', { basis: 'none', eta_ms: null, samples: 0, repos: 0 }, 'ETA unknown (no history)'],
  ['a single repo run', { basis: 'repo', eta_ms: 59999, samples: 1, repos: 1 }, 'ETA ~59s (repo average, 1 run)'],
  ['a single repo in the global average', { basis: 'global', eta_ms: 61000, samples: 3, repos: 1 }, 'ETA ~1m 1s (global average, 1 repo)'],
]

for (const [name, reply, tail] of ETA_TEXTS) {
  test(`the notice for ${name} reads: ${tail}`, async ($, on) => {
    const w = world(on)
    w.respond = (argv) => (argv[1] === 'eta' ? eta(reply) : listing(job(HOOK_RUN)))
    await $.session.start(START)

    await w.clock.advance(BAND_REFRESH_MS)

    expect(w.toasts).toEqual(['msgate · spawned code-review · trigger hook-post-commit · ' + tail])
  })
}

const ETA_FAILURES: [string, (argv: string[]) => any][] = [
  ['exits non-zero', () => ({ exitCode: 2, stdout: '', stderr: 'msgate: usage' })],
  ['prints something unreadable', () => ok('ETA ~2m')],
  ['prints an object with no known basis', () => ok(JSON.stringify({ sdk_status: 'ok', exit_code: 0 }))],
  ['prints a repo average with no sample count', () => eta({ basis: 'repo', eta_ms: 1000 })],
  ['prints a global average with no repo count', () => eta({ basis: 'global', eta_ms: 1000, samples: 4 })],
  ['prints a negative count', () => eta({ basis: 'repo', eta_ms: 1000, samples: -1 })],
  ['prints a fractional count', () => eta({ basis: 'global', eta_ms: 1000, repos: 1.5 })],
  ['prints an average with no time', () => eta({ basis: 'repo', eta_ms: null, samples: 3, repos: 1 })],
  ['is rejected', () => Promise.reject(new Error('spawn failed'))],
]

for (const [name, answer] of ETA_FAILURES) {
  test(`when eta ${name} the notice says the ETA is unavailable`, async ($, on) => {
    const w = world(on)
    w.respond = (argv) => (argv[1] === 'eta' ? answer(argv) : listing(job(HOOK_RUN)))
    await $.session.start(START)

    await w.clock.advance(BAND_REFRESH_MS)

    const text = 'msgate · spawned code-review · trigger hook-post-commit · ETA unavailable'
    expect(w.toasts).toEqual([text])
    expect(w.logs.filter((l) => l === text)).toEqual([text])
  })
}

test('an eta that fails is logged to the debug log once per load, whatever the run', async ($, on) => {
  const w = world(on)
  let runs = [job(HOOK_RUN)]
  w.respond = (argv) => (argv[1] === 'eta' ? { exitCode: 1, stdout: '', stderr: 'msgate: no git' } : listing(...runs))
  await $.session.start(START)
  await w.clock.advance(BAND_REFRESH_MS)
  runs = [job({ ...HOOK_RUN, run_id: 'run-h2' }), ...runs]
  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts.length).toBe(2)
  expect(w.debugLogs).toEqual(['could not read the ETA of code-review: msgate exited 1: no git'])

  await $.session.start(START)
  runs = [job({ ...HOOK_RUN, run_id: 'run-h3' }), ...runs]
  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.debugLogs.length).toBe(2)
})

const ENDED = { run_id: 'run-e', state: 'done', outcome: 'ok', source: 'hook-stop', purpose: 'code-review', background: false }

test('the first listing announces its running runs and leaves its finished ones silent', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job(HOOK_RUN), job(ENDED)))
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts).toEqual(['msgate · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)'])
  expect(etaRuns(w).length).toBe(1)
})

const FINISHED_RUNS: [string, Record<string, unknown>, string][] = [
  ['from started_at to ended_at', { started_at: '2026-01-01T00:00:01Z', ended_at: '2026-01-01T00:00:04Z' }, 'finished in 3s'],
  ['from admitted_at when there is no started_at', { ended_at: '2026-01-01T00:01:35Z' }, 'finished in 1m 35s'],
  ['with no duration when ended_at is missing', { started_at: '2026-01-01T00:00:01Z' }, 'finished'],
  ['with no duration when a timestamp is unreadable', { started_at: 'yesterday', ended_at: '2026-01-01T00:00:04Z' }, 'finished'],
]

for (const [name, times, tail] of FINISHED_RUNS) {
  test(`a run that shows up already finished is announced ${name}`, async ($, on) => {
    const w = world(on)
    let runs: any[] = []
    w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(...runs))
    await $.session.start(START)
    await w.clock.advance(BAND_REFRESH_MS)
    runs = [job({ ...ENDED, ...times })]

    await w.clock.advance(BAND_REFRESH_MS)

    const text = 'msgate · spawned code-review · trigger hook-stop · ' + tail
    expect(w.toasts).toEqual([text])
    expect(w.logs).toEqual([text])
    expect(etaRuns(w)).toEqual([])
  })
}

// What the plugin last wrote to its spawnNotified state, the write itself
// passing on to the session's state.
function watchNotified(on: any) {
  const writes: string[][] = []
  on('state.set', { plugin: 'msgate', key: 'spawnNotified' }, (_$: any, e: any, next: any) => {
    writes.push(e.value)
    return next(e)
  })
  return () => writes.at(-1)
}

test('an announced run stays silent on later listings, polls and a reload', async ($, on) => {
  const w = world(on)
  const notified = watchNotified(on)
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : argv[1] === 'status' ? listing(job(HOOK_RUN)) : ok(JSON.stringify({ usage_totals: {} })))
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS * 3)
  await w.clock.advance(POLL_MS)
  expect(w.toasts.length).toBe(1)
  expect(notified()).toEqual(['run-h'])

  await $.session.start(START)
  await w.clock.advance(BAND_REFRESH_MS * 2)

  expect(w.toasts.length).toBe(1)
  expect(etaRuns(w).length).toBe(1)
})

test('a run an earlier load announced is not announced again by a later one, even when it shows up finished', async ($, on) => {
  const w = world(on)
  let runs = [job(HOOK_RUN)]
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(...runs))
  await $.session.start(START)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(w.toasts.length).toBe(1)

  runs = []
  await $.session.start(START)
  await w.clock.advance(BAND_REFRESH_MS)
  runs = [job({ ...HOOK_RUN, state: 'done', outcome: 'ok' })]
  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts.length).toBe(1)
})

test('a session start within a load reads a new baseline: a finished run it never saw stays silent', async ($, on) => {
  const w = world(on)
  let runs: any[] = []
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(...runs))
  await $.session.start(START)
  await w.clock.advance(BAND_REFRESH_MS)

  await $.session.start(START)
  runs = [job(ENDED)]
  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts).toEqual([])
})

test('the notified runs keep the 500 most recent ids, finished ones the first listing marked included', async ($, on) => {
  const w = world(on)
  const notified = watchNotified(on)
  // The listing is newest first, as msgate prints it.
  const runs = Array.from({ length: 501 }, (_, i) => job({ run_id: `r-${500 - i}`, state: 'done', outcome: 'ok' }))
  w.respond = () => listing(...runs)
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  const set = notified()!
  expect(set.length).toBe(500)
  expect(set).toContain('r-500')
  expect(set).toContain('r-1')
  expect(set).not.toContain('r-0')
  expect(w.toasts).toEqual([])
})

function gate() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => {
    release = resolve
  })
  return { promise, release }
}

// eventually waits, for a bounded time, for something only a faulty mod would
// make happen; it tells whether it did.
async function eventually(condition: () => boolean) {
  for (let i = 0; i < 50 && !condition(); i++) await new Promise((resolve) => setTimeout(resolve, 1))
  return condition()
}

// until lets the pending work of the mod settle: the condition is checked
// between turns of the event loop.
async function until(condition: () => boolean) {
  for (let i = 0; i < 500 && !condition(); i++) await new Promise((resolve) => setTimeout(resolve, 1))
  expect(condition()).toBe(true)
}

const FINISHED_AGENT_RUN = { run_id: 'run-1', conversation_id: 'conv-1', state: 'done', outcome: 'ok', output_path: '/o' }

test('an agent type turn announces its run right after admission without waiting for the ETA', async ($, on) => {
  const w = world(on)
  const etaHeld = gate()
  w.respond = async (argv) => {
    switch (argv[1]) {
      case 'exec':
        return ok(JSON.stringify({ conversation_id: 'conv-1', run_id: 'run-1' }))
      case 'eta':
        await etaHeld.promise
        return eta({ ...REPO_ETA, purpose: 'second-opinion' })
      case 'wait':
        return ok(JSON.stringify(FINISHED_AGENT_RUN))
      case 'progress':
        return ok(JSON.stringify({ run_id: 'run-1', state: 'done', next: 0, entries: [] }))
      case 'result':
        return ok('Codex: done.')
      default:
        return argv[2] === '--json'
          ? listing(job({ run_id: 'run-1', state: 'running', source: 'agent', purpose: 'second-opinion', background: true }))
          : ok(JSON.stringify(FINISHED_AGENT_RUN))
    }
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)

  let answer: Awaited<ReturnType<typeof step>> | undefined
  void step($, 'ag-1').then((out) => {
    answer = out
  })
  await until(() => answer !== undefined)

  expect(answer!.text).toContain('Codex: done.')
  expect(etaRuns(w)).toEqual([])

  await w.clock.advance(0)
  await until(() => etaRuns(w).length === 1)
  expect(etaRuns(w).map((r) => r.argv.slice(1))).toEqual([['eta', '--purpose', 'second-opinion', '--cwd', '/work', '--json']])
  expect(w.toasts).toEqual([])

  etaHeld.release()
  await until(() => w.toasts.length === 1)
  expect(w.toasts).toEqual(['msgate · spawned second-opinion · trigger agent · ETA ~2m 36s (repo average, 12 runs)'])

  // The listing that now lists the same run says nothing more.
  await w.clock.advance(BAND_REFRESH_MS * 2)
  expect(w.toasts.length).toBe(1)
  expect(etaRuns(w).length).toBe(1)
})

const LISTING_FAILED = 'could not list the runs of the session for spawn notices: '

// The reason of a rejection is worded by the engine, so only the line's head is held.
const LIST_FAILURES: [string, () => any, string][] = [
  ['exits non-zero', () => ({ exitCode: 1, stdout: '', stderr: 'msgate: store locked' }), LISTING_FAILED + 'msgate exited 1: store locked'],
  ['is rejected', () => Promise.reject(new Error('spawn failed')), LISTING_FAILED],
]

for (const [name, failing, head] of LIST_FAILURES) {
  test(`a listing that ${name} is logged once until a listing succeeds`, async ($, on) => {
    const w = world(on)
    let isFailing = true
    w.respond = (argv) => (argv[1] === 'status' && isFailing ? failing() : listing())
    await $.session.start(START)

    await w.clock.advance(BAND_REFRESH_MS * 3)
    expect(w.debugLogs.length).toBe(1)

    isFailing = false
    await w.clock.advance(BAND_REFRESH_MS)
    isFailing = true
    await w.clock.advance(BAND_REFRESH_MS * 2)

    expect(w.debugLogs.length).toBe(2)
    expect(w.debugLogs.every((l) => l.startsWith(head))).toBe(true)
  })
}

const ADMITTED = { conversation_id: 'conv-1', run_id: 'run-1', state: 'running' }
const OWN_RUN = { run_id: 'run-1', state: 'running', source: 'mod', purpose: 'delegation', background: true }
const OWN_NOTICE = 'msgate · spawned delegation · trigger mod · '

const TURN_TOOLS: [string, Record<string, unknown>, boolean][] = [
  ['run', { prompt: 'review this' }, false],
  ['run with a conversation', { conversation_id: 'conv-1', prompt: 'and now the tests' }, true],
]

for (const [tool, input, continued] of TURN_TOOLS) {
  test(`${tool} announces its run with the purpose and source the run reports, and its result does not wait`, async ($, on) => {
    const w = world(on)
    const statusHeld = gate()
    const etaHeld = gate()
    w.respond = async (argv) => {
      if (argv[1] === 'eta') {
        await etaHeld.promise
        return eta({ ...REPO_ETA, purpose: 'delegation' })
      }
      if (argv[1] === 'status') {
        await statusHeld.promise
        return ok(JSON.stringify(OWN_RUN))
      }
      return ok(JSON.stringify(ADMITTED))
    }
    await $.session.start(START)

    let out: any
    void $.tool.call({ tool: TOOL('run'), ...input }).then((result: any) => {
      out = result
    })
    await until(() => out !== undefined)
    expect(JSON.parse(out.result)).toEqual({ conversation_id: 'conv-1', run_id: 'run-1', continued })
    expect(w.toasts).toEqual([])

    await w.clock.advance(0)
    statusHeld.release()
    await until(() => etaRuns(w).length === 1)
    expect(w.runs.filter((r) => r.argv[1] === 'status').map((r) => r.argv.slice(1))).toEqual([['status', 'run-1', '--json']])
    expect(etaRuns(w).map((r) => r.argv.slice(1))).toEqual([['eta', '--purpose', 'delegation', '--cwd', '/work', '--json']])

    etaHeld.release()
    await until(() => w.toasts.length === 1)
    expect(w.toasts).toEqual([OWN_NOTICE + 'ETA ~2m 36s (repo average, 12 runs)'])

    // The listing that now lists the same run says nothing more.
    w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job(OWN_RUN)))
    await w.clock.advance(BAND_REFRESH_MS * 2)
    expect(w.toasts.length).toBe(1)
  })
}

const STATUS_FAILURES: [string, () => any][] = [
  ['exits non-zero', () => ({ exitCode: 1, stdout: '', stderr: 'msgate: no such run' })],
  ['is rejected', () => Promise.reject(new Error('spawn failed'))],
]

for (const [name, failing] of STATUS_FAILURES) {
  test(`when the status read of an admitted run ${name} the notice waits for the listing`, async ($, on) => {
    const w = world(on)
    w.respond = (argv) => {
      if (argv[1] === 'eta') return eta({ ...REPO_ETA, purpose: 'delegation' })
      if (argv[1] === 'exec') return ok(JSON.stringify(ADMITTED))
      return argv[2] === 'run-1' ? failing() : listing(job(OWN_RUN))
    }
    await $.session.start(START)

    const out: any = await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })
    await w.clock.advance(0)
    await until(() => w.runs.filter((r) => r.argv[1] === 'status').length === 1)

    expect(JSON.parse(out.result)).toEqual({ conversation_id: 'conv-1', run_id: 'run-1', continued: false })
    expect(w.toasts).toEqual([])
    expect(etaRuns(w)).toEqual([])

    await w.clock.advance(BAND_REFRESH_MS)

    expect(w.toasts).toEqual([OWN_NOTICE + 'ETA ~2m 36s (repo average, 12 runs)'])
  })
}

test('the ETA lookup is given ten seconds', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job(HOOK_RUN)))
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  expect(etaRuns(w).map((r) => r.timeoutMs)).toEqual([10000])
})

test('a listing that cannot be read is a failed scan: it is logged and sets no baseline', async ($, on) => {
  const w = world(on)
  let answer: () => any = () => ok('not json')
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : answer())
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)
  answer = () => ok(JSON.stringify({ sdk_status: 'ok' }))
  await w.clock.advance(BAND_REFRESH_MS)
  expect(w.debugLogs).toEqual([LISTING_FAILED + 'the listing could not be read'])

  answer = () => listing(job(ENDED))
  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts).toEqual([])
})

test('the first scan claims every run it lists before it announces any', async ($, on) => {
  const w = world(on)
  const notified = watchNotified(on)
  const etaHeld = gate()
  w.respond = async (argv) => {
    if (argv[1] === 'eta') {
      await etaHeld.promise
      return eta(REPO_ETA)
    }
    return listing(
      job({ ...ENDED, admitted_at: '2026-01-01T00:00:10Z' }),
      job({ ...HOOK_RUN, admitted_at: '2026-01-01T00:00:00Z' }),
    )
  }
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)
  await until(() => etaRuns(w).length === 1)

  // The older running run waits for its ETA; the finished one is already marked.
  expect([...notified()!].sort()).toEqual(['run-e', 'run-h'])
  etaHeld.release()
  await until(() => w.toasts.length === 1)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(w.toasts).toEqual(['msgate · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)'])
})

const STATUS_READ_FAILED = 'could not read run run-1 to announce it: '

const UNREADABLE_STATUS: [string, () => any][] = [
  ['exits non-zero', () => ({ exitCode: 1, stdout: '', stderr: 'msgate: no such run' })],
  ['is rejected', () => Promise.reject(new Error('spawn failed'))],
  ['prints something unreadable', () => ok('not json')],
  ['prints a run with no purpose', () => ok(JSON.stringify({ run_id: 'run-1', source: 'mod' }))],
]

for (const [name, failing] of UNREADABLE_STATUS) {
  test(`a status read at admission that ${name} is logged once per load`, async ($, on) => {
    const w = world(on)
    w.respond = (argv) => (argv[1] === 'exec' ? ok(JSON.stringify(ADMITTED)) : failing())
    await $.session.start(START)

    await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })
    await w.clock.advance(0)
    await $.tool.call({ tool: TOOL('run'), prompt: 'and again' })
    await w.clock.advance(0)
    await until(() => w.runs.filter((r) => r.argv[1] === 'status').length === 2)

    expect(w.debugLogs.length).toBe(1)
    expect(w.debugLogs[0]!.startsWith(STATUS_READ_FAILED)).toBe(true)
  })
}

test('the admission notice of run starts from a timer, after the tool has answered', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'exec' ? ok(JSON.stringify(ADMITTED)) : argv[1] === 'eta' ? eta(REPO_ETA) : ok(JSON.stringify(OWN_RUN)))
  await $.session.start(START)

  await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })
  expect(w.runs.filter((r) => r.argv[1] === 'status')).toEqual([])

  await w.clock.advance(0)
  await until(() => w.toasts.length === 1)
})

test('a scan that lists a run while its admission ETA is held announces it once', async ($, on) => {
  const w = world(on)
  const etaHeld = gate()
  const listings = () => w.runs.filter((r) => r.argv[2] === '--json').length
  w.respond = async (argv) => {
    if (argv[1] === 'exec') return ok(JSON.stringify(ADMITTED))
    if (argv[1] === 'eta') {
      await etaHeld.promise
      return eta({ ...REPO_ETA, purpose: 'delegation' })
    }
    return argv[2] === 'run-1' ? ok(JSON.stringify(OWN_RUN)) : listing(job(OWN_RUN))
  }
  await $.session.start(START)
  await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })
  await w.clock.advance(0)
  await until(() => etaRuns(w).length === 1)

  await w.clock.advance(BAND_REFRESH_MS)
  await until(() => listings() === 1)
  // A scan that took the run would look its ETA up too.
  expect(await eventually(() => etaRuns(w).length === 2)).toBe(false)
  etaHeld.release()
  await until(() => w.toasts.length >= 1)

  expect(await eventually(() => w.toasts.length === 2)).toBe(false)
  expect(w.toasts).toEqual([OWN_NOTICE + 'ETA ~2m 36s (repo average, 12 runs)'])
})

test('two claims made together both reach the notified runs', async ($, on) => {
  const w = world(on)
  const notified = watchNotified(on)
  const reads = gate()
  let pending = 0
  on('state.get', { plugin: 'msgate', key: 'spawnNotified' }, async (_$: any, e: any, next: any) => {
    pending += 1
    await reads.promise
    return next(e)
  })
  w.respond = (argv) => {
    if (argv[1] === 'exec') return ok(JSON.stringify(ADMITTED))
    if (argv[1] === 'eta') return eta(REPO_ETA)
    return argv[2] === 'run-1' ? ok(JSON.stringify(OWN_RUN)) : listing(job(HOOK_RUN))
  }
  await $.session.start(START)
  await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })
  await w.clock.advance(0)
  await until(() => pending === 1)
  await w.clock.advance(BAND_REFRESH_MS)
  // Claims made one after the other read the record one after the other.
  expect(await eventually(() => pending === 2)).toBe(false)

  reads.release()
  await until(() => w.toasts.length === 2)

  expect([...notified()!].sort()).toEqual(['run-1', 'run-h'])
})

const SINCE_LOAD = [
  ['ended after the load', { started_at: '1970-01-01T00:00:01Z', ended_at: '1970-01-01T00:00:04Z' }, ['msgate · spawned code-review · trigger hook-stop · finished in 3s']],
  ['ended at the load', { started_at: '1970-01-01T00:00:00Z', ended_at: '1970-01-01T00:00:00Z' }, ['msgate · spawned code-review · trigger hook-stop · finished in 0s']],
  ['ended before the load', { started_at: '1969-12-31T23:59:50Z', ended_at: '1969-12-31T23:59:59Z' }, []],
  ['has no end time', { started_at: '1970-01-01T00:00:01Z' }, []],
  ['has an unreadable end time', { ended_at: 'yesterday' }, []],
] as const

for (const [name, times, toasts] of SINCE_LOAD) {
  test(`a run in the first listing that ${name} is ${toasts.length === 0 ? 'left silent' : 'announced as finished'}`, async ($, on) => {
    const w = world(on)
    w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job({ ...ENDED, ...times })))
    await $.session.start(START)

    await w.clock.advance(BAND_REFRESH_MS)

    expect(w.toasts).toEqual([...toasts])
    expect(etaRuns(w)).toEqual([])
  })
}

test('a run that ended in the second the load started in is announced as finished, one from the second before is not', async ($, on) => {
  const w = world(on)
  w.respond = (argv) =>
    argv[1] === 'eta'
      ? eta(REPO_ETA)
      : listing(
          job({ ...ENDED, run_id: 'run-same', started_at: '1970-01-01T00:00:00Z', ended_at: '1970-01-01T00:00:00Z' }),
          job({ ...ENDED, run_id: 'run-before', started_at: '1969-12-31T23:59:50Z', ended_at: '1969-12-31T23:59:59Z' }),
        )
  await w.clock.advance(100)
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts).toEqual(['msgate · spawned code-review · trigger hook-stop · finished in 0s'])
})

test('a listed run that names its purpose as scenario is announced under that label, and its ETA is asked for it', async ($, on) => {
  const w = world(on)
  const { purpose, ...older } = HOOK_RUN
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job({ ...older, purpose: undefined, scenario: 'code-review' })))
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts).toEqual(['msgate · spawned code-review · trigger hook-post-commit · ETA ~2m 36s (repo average, 12 runs)'])
  expect(etaRuns(w).map((r) => r.argv.slice(1))).toEqual([['eta', '--purpose', 'code-review', '--cwd', '/work', '--json']])
})

test('the purpose of a run wins over its scenario when it reports both', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'eta' ? eta(REPO_ETA) : listing(job({ ...HOOK_RUN, purpose: 'code-review', scenario: 'adhoc' })))
  await $.session.start(START)

  await w.clock.advance(BAND_REFRESH_MS)

  expect(w.toasts[0]).toContain('spawned code-review ')
})

test('the status read of an admitted run that names its purpose as scenario announces it', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => {
    if (argv[1] === 'eta') return eta(REPO_ETA)
    if (argv[1] === 'status') return ok(JSON.stringify({ run_id: 'run-1', source: 'mod', scenario: 'delegation' }))
    return ok(JSON.stringify(ADMITTED))
  }
  await $.session.start(START)

  await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })
  await w.clock.advance(0)
  await until(() => w.toasts.length === 1)

  expect(w.toasts).toEqual([OWN_NOTICE + 'ETA ~2m 36s (repo average, 12 runs)'])
})
