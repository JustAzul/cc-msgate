import { expect, test } from 'claude-code/testing'
import { OTHER_CALLER, job, listing, msgateAs, ok, world } from './kit'

const MINE = job({ run_id: 'run-mine', source: 'plugin:p2', state: 'done', outcome: 'ok' })
const THEIRS = job({ run_id: 'run-theirs', source: 'plugin:p3' })
const CLAUDES = job({ run_id: 'run-mod', source: 'mod' })
const RUNS: Record<string, any> = { 'run-mine': MINE, 'run-theirs': THEIRS, 'run-mod': CLAUDES }

// The binary as the interface meets it: a run's status, its output, and a
// cancellation. The session's listing is not served: the interface never asks.
function binary(output = 'the output') {
  return (argv: string[]) => {
    if (argv[1] === 'status') return RUNS[argv[2]!] ? ok(JSON.stringify(RUNS[argv[2]!])) : { exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' }
    if (argv[1] === 'result') return ok(output)
    if (argv[1] === 'cancel') return ok('')
    throw new Error('unexpected msgate call: ' + argv.join(' '))
  }
}

const commands = (w: { runs: { argv: string[] }[] }) => w.runs.map((r) => r.argv.slice(1))

// The ids the engine recorded for each caller at admission, oldest first.
const recorded = (w: ReturnType<typeof world>) => w.state.get('admittedRuns')?.value as Record<string, string[]> | undefined
function record(w: ReturnType<typeof world>, ids: Record<string, string[]>) {
  w.state.set('admittedRuns', { value: ids, version: 1 })
}

test('list reads the runs the engine recorded for the caller, by id and newest first', async ($, on) => {
  const w = world(on)
  record(w, { p2: ['run-old', 'run-mine'], p3: ['run-theirs'] })
  w.respond = binary()
  RUNS['run-old'] = job({ run_id: 'run-old', source: 'plugin:p2' })

  const out: any = await msgateAs(w, 'p2').jobs({ action: 'list' })

  expect(commands(w)).toEqual([['status', 'run-mine', '--json'], ['status', 'run-old', '--json']])
  expect(out.value.runs.map((run: any) => run.run_id)).toEqual(['run-mine', 'run-old'])
  expect(out.value.runs[0]).toMatchObject({ state: 'done', outcome: 'ok', purpose: 'adhoc' })
  expect((await msgateAs(w, OTHER_CALLER).jobs({ action: 'list' }) as any).value.runs.map((run: any) => run.run_id)).toEqual(['run-theirs'])
})

test('list still returns the caller\'s run when the session has 20 newer runs of others', async ($, on) => {
  const w = world(on)
  record(w, { p2: ['run-mine'] })
  const newer = Array.from({ length: 20 }, (_, i) => job({ run_id: 'run-mod-' + i, source: 'mod' }))
  w.respond = (argv) => (argv[1] === 'status' && argv[2] === '--json' ? listing(...newer) : binary()(argv))

  const out: any = await msgateAs(w, 'p2').jobs({ action: 'list' })

  expect(out.value.runs.map((run: any) => run.run_id)).toEqual(['run-mine'])
  expect(commands(w)).toEqual([['status', 'run-mine', '--json']])
})

test('a caller that has admitted nothing lists no runs and reads none', async ($, on) => {
  const w = world(on)
  w.respond = binary()

  expect(await msgateAs(w, 'p2').jobs({ action: 'list' })).toEqual({ value: { runs: [] } })
  expect(w.runs).toEqual([])
})

test('list drops an id the binary no longer knows from the record, and keeps the others', async ($, on) => {
  const w = world(on)
  record(w, { p2: ['run-pruned', 'run-mine'], p3: ['run-pruned'] })
  w.respond = binary()

  const out: any = await msgateAs(w, 'p2').jobs({ action: 'list' })

  expect(out.value.runs.map((run: any) => run.run_id)).toEqual(['run-mine'])
  expect(recorded(w)).toEqual({ p2: ['run-mine'], p3: ['run-pruned'] })
})

test('a caller whose every run was pruned leaves no entry in the record', async ($, on) => {
  const w = world(on)
  record(w, { p2: ['run-pruned'], p3: ['run-theirs'] })
  w.respond = binary()

  expect(await msgateAs(w, 'p2').jobs({ action: 'list' })).toEqual({ value: { runs: [] } })
  expect(recorded(w)).toEqual({ p3: ['run-theirs'] })
})

test('list is refused when a run cannot be read, and the record stays as it was', async ($, on) => {
  const w = world(on)
  record(w, { p2: ['run-mine', 'run-pruned'] })
  w.respond = (argv) => (argv[2] === 'run-mine' ? { exitCode: 70, stdout: '', stderr: 'msgate: cannot resolve the home\n' } : binary()(argv))

  expect(await msgateAs(w).jobs({ action: 'list' })).toEqual({ deny: 'msgate:jobs:msgate exited 70: cannot resolve the home' })
  expect(recorded(w)).toEqual({ p2: ['run-mine', 'run-pruned'] })
})

test('the record is read again by a load that starts after the engine reloads', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'exec' ? ok(JSON.stringify({ conversation_id: 'c', run_id: 'run-mine' })) : binary()(argv))
  await msgateAs(w, 'p2').run({ prompt: 'q' })

  const reloaded: any = await msgateAs(w, 'p2').jobs({ action: 'list' })

  expect(reloaded.value.runs.map((run: any) => run.run_id)).toEqual(['run-mine'])
})

test('status returns the run of the calling plugin', async ($, on) => {
  const w = world(on)
  w.respond = binary()

  const out: any = await msgateAs(w).jobs({ action: 'status', run_id: 'run-mine' })

  expect(out.value).toMatchObject({ run_id: 'run-mine', state: 'done', outcome: 'ok', purpose: 'adhoc' })
})

test('result returns the output of the run of the calling plugin', async ($, on) => {
  const w = world(on)
  w.respond = binary('Two findings.\n')

  const out = await msgateAs(w).jobs({ action: 'result', run_id: 'run-mine' })

  expect(out).toEqual({ value: 'Two findings.\n' })
  expect(commands(w)).toEqual([['status', 'run-mine', '--json'], ['result', 'run-mine']])
})

test('a result over 64 KiB is cut and names the output file', async ($, on) => {
  const w = world(on)
  w.respond = binary('x'.repeat(70000))

  const out: any = await msgateAs(w).jobs({ action: 'result', run_id: 'run-mine' })

  expect(out.value).toBe('x'.repeat(65536) + '\n[output cut at 64 KiB; full output at /state/runs/run-1/output.md]')
})

test('cancel asks for the cancellation of the run of the calling plugin', async ($, on) => {
  const w = world(on)
  w.respond = binary()

  const out = await msgateAs(w).jobs({ action: 'cancel', run_id: 'run-mine' })

  expect(out).toEqual({ value: 'cancellation requested for run-mine' })
  expect(commands(w)).toEqual([['status', 'run-mine', '--json'], ['cancel', 'run-mine']])
})

for (const action of ['status', 'result', 'cancel']) {
  for (const [owner, run_id] of [['another plugin', 'run-theirs'], ['Claude\'s mod tool', 'run-mod']]) {
    test(`${action} of a run started by ${owner} is refused as msgate:jobs:not-yours and does nothing`, async ($, on) => {
      const w = world(on)
      w.respond = binary()

      const out = await msgateAs(w, 'p2').jobs({ action, run_id })

      expect(out).toEqual({ deny: 'msgate:jobs:not-yours' })
      expect(commands(w)).toEqual([['status', run_id, '--json']])
    })
  }
}

const REFUSED: [string, any, string][] = [
  ['an action that does not exist', { action: 'purge' }, 'msgate:jobs:action must be one of: list, status, result, cancel'],
  ['no arguments', undefined, 'msgate:jobs:action must be one of: list, status, result, cancel'],
  ['a status with no run id', { action: 'status' }, 'msgate:jobs:run_id is required for status'],
  ['a run id that could read as a flag', { action: 'cancel', run_id: '--all' }, 'msgate:jobs:run_id is required for cancel'],
  ['a run the binary does not know', { action: 'status', run_id: 'run-gone' }, 'msgate:jobs:msgate exited 4: run not found'],
]

for (const [name, args, deny] of REFUSED) {
  test(`jobs is refused on ${name}`, async ($, on) => {
    const w = world(on)
    w.respond = binary()

    const out: any = await msgateAs(w).jobs(args)

    expect(out.deny.startsWith(deny)).toBe(true)
  })
}

test('jobs is refused when the process cannot be run', async ($, on) => {
  const w = world(on)
  w.respond = () => Promise.reject(new Error('spawn failed'))

  expect(await msgateAs(w).jobs({ action: 'result', run_id: 'run-mine' })).toEqual({ deny: 'msgate:jobs:msgate could not run: spawn failed' })
})
