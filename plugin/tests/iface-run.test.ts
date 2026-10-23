import { expect, test } from 'claude-code/testing'
import { OTHER_CALLER, SESSION, isShim, msgateAs, ok, world } from './kit'

const ADMITTED = { conversation_id: 'conv-1', run_id: 'run-1', state: 'running', run_dir: '/s/runs/run-1', output_path: '/s/runs/run-1/output.md' }

// The fake engine the interface runs on has no store, clock or prompt noun: a
// run that touched the delivery queue or submitted a notice would throw there.
test('run admits a job for the calling plugin and resolves its ids', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))

  const out = await msgateAs(w, 'p2').run({ prompt: 'q' })

  expect(out).toEqual({ value: { conversation_id: 'conv-1', run_id: 'run-1', continued: false } })
  expect(isShim(w.runs[0]!.argv[0]!)).toBe(true)
  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', '--background', '--json', '--source', 'plugin:p2', '--session-id', SESSION, '-'])
  expect(w.runs[0]!.stdin).toBe('q')
  expect(w.runs.length).toBe(1)
})

test('a plugin\'s run is not marked to open a row, on either path', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))

  await msgateAs(w).run({ prompt: 'q' })
  await msgateAs(w).run({ prompt: 'q2', conversation_id: 'conv-1' })

  expect(w.runs.map((r) => r.argv.includes('--agent-feedback'))).toEqual([false, false])
  expect(w.runs.map((r) => r.argv[1])).toEqual(['exec', 'send'])
})

test('the source of the job names the plugin the host stamped on the call', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))

  await msgateAs(w, OTHER_CALLER).run({ prompt: 'q' })

  expect(w.runs[0]!.argv).toContain('plugin:p3')
})

test('run with a conversation sends the next turn and passes the settings as flags', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-2' }))

  const out = await msgateAs(w).run({ prompt: 'q2', conversation_id: 'conv-1', purpose: 'followup', model: 'm1', effort: 'low', sandbox: 'read-only', cwd: '/repo' })

  expect(out).toEqual({ value: { conversation_id: 'conv-1', run_id: 'run-2', continued: true } })
  expect(w.runs[0]!.argv.slice(1)).toEqual([
    'send', 'conv-1', '--background', '--json', '--source', 'plugin:p2', '--session-id', SESSION,
    '--purpose', 'followup', '--model', 'm1', '--effort', 'low', '--sandbox', 'read-only', '--cwd', '/repo', '-',
  ])
})

const HARNESS_CASES: [string, Record<string, string>, string, string[]][] = [
  ['a new conversation', { harness: 'lean' }, 'exec', ['--harness', 'lean', '-']],
  ['the next turn of a conversation', { conversation_id: 'conv-1', harness: 'bare' }, 'send', ['--harness', 'bare', '-']],
  ['a turn that names no harness', { conversation_id: 'conv-1' }, 'send', ['-']],
]

for (const [name, args, command, tail] of HARNESS_CASES) {
  test(`run on ${name} ends its argv with ${JSON.stringify(tail)}`, async ($, on) => {
    const w = world(on)
    w.respond = () => ok(JSON.stringify(ADMITTED))

    await msgateAs(w).run({ prompt: 'q', ...args })

    expect(w.runs[0]!.argv[1]).toBe(command)
    expect(w.runs[0]!.argv.slice(-tail.length)).toEqual(tail)
    expect(w.runs[0]!.argv.includes('--harness')).toBe(tail.includes('--harness'))
  })
}

const REFUSED: [string, any, (argv: string[]) => any, string][] = [
  ['an empty prompt', { prompt: '  ' }, () => ok('{}'), 'msgate:run:prompt is required and must be a non-empty string'],
  ['no arguments', undefined, () => ok('{}'), 'msgate:run:prompt is required and must be a non-empty string'],
  ['a conversation id that could read as a flag', { prompt: 'q', conversation_id: '--json' }, () => ok('{}'), 'msgate:run:conversation_id must be an id'],
  ['a setting that is not text', { prompt: 'q', model: 5 }, () => ok('{}'), 'msgate:run:model must be a string'],
  ['a failed admission', { prompt: 'q' }, () => ({ exitCode: 3, stdout: JSON.stringify({ error: 'conversation conv-1 is busy' }), stderr: '' }), 'msgate:run:msgate exited 3: conversation conv-1 is busy'],
  ['an admission that prints no ids', { prompt: 'q' }, () => ok('{"state":"running"}'), 'msgate:run:msgate printed an unexpected admission result: {"state":"running"}'],
  ['a process that cannot be run', { prompt: 'q' }, () => Promise.reject(new Error('spawn failed')), 'msgate:run:msgate could not run: spawn failed'],
]

for (const [name, args, answer, deny] of REFUSED) {
  test(`run is refused as msgate:run:… on ${name}`, async ($, on) => {
    const w = world(on)
    w.respond = answer

    const out: any = await msgateAs(w).run(args)

    expect(out.deny.startsWith(deny)).toBe(true)
    expect(out.value).toBeUndefined()
  })
}

const recorded = (w: ReturnType<typeof world>) => w.state.get('admittedRuns')?.value as Record<string, string[]> | undefined

test('run records the run id for the calling plugin at admission', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-' + ++n }))

  await msgateAs(w, 'p2').run({ prompt: 'a' })
  await msgateAs(w, 'p2').run({ prompt: 'b' })
  await msgateAs(w, OTHER_CALLER).run({ prompt: 'c' })

  expect(recorded(w)).toEqual({ p2: ['run-1', 'run-2'], p3: ['run-3'] })
})

test('a refused admission records nothing', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 3, stdout: '', stderr: 'msgate: busy\n' })

  await msgateAs(w).run({ prompt: 'q' })

  expect(recorded(w)).toBeUndefined()
})

test('runs admitted together are all recorded', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-' + ++n }))
  const msgate = msgateAs(w, 'p2')

  await Promise.all(Array.from({ length: 6 }, (_, i) => msgate.run({ prompt: 'p' + i })))

  expect(recorded(w)!.p2.slice().sort()).toEqual(['run-1', 'run-2', 'run-3', 'run-4', 'run-5', 'run-6'])
})

test('the record keeps the 200 most recent runs of each caller', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-' + ++n }))
  const msgate = msgateAs(w, 'p2')

  for (let i = 0; i < 205; i++) await msgate.run({ prompt: 'p' })

  const ids = recorded(w)!.p2
  expect(ids.length).toBe(200)
  expect(ids[0]).toBe('run-6')
  expect(ids.at(-1)).toBe('run-205')
})

test('a record that cannot be written leaves the admitted run answered, and the failure in the debug log', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  w.stateWriteFailures = Infinity

  const out = await msgateAs(w).run({ prompt: 'q' })

  expect(out).toEqual({ value: { conversation_id: 'conv-1', run_id: 'run-1', continued: false } })
  expect(w.debugLogs).toEqual(['could not record run run-1 for p2: the state is unavailable'])
})
