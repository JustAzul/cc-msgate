import { expect, test } from 'claude-code/testing'
import { OTHER_CALLER, SESSION, job, listing, msgateAs, ok, world } from './kit'

const MINE = job({ run_id: 'run-mine', source: 'plugin:p2', state: 'done', outcome: 'ok' })
const THEIRS = job({ run_id: 'run-theirs', source: 'plugin:p3' })
const CLAUDES = job({ run_id: 'run-mod', source: 'mod' })
const RUNS: Record<string, any> = { 'run-mine': MINE, 'run-theirs': THEIRS, 'run-mod': CLAUDES }

// The binary as the interface meets it: the session's listing, a run's status,
// its output, and a cancellation.
function binary(output = 'the output') {
  return (argv: string[]) => {
    if (argv[1] === 'status' && argv[2] === '--json') return listing(THEIRS, MINE, CLAUDES)
    if (argv[1] === 'status') return RUNS[argv[2]!] ? ok(JSON.stringify(RUNS[argv[2]!])) : { exitCode: 4, stdout: '', stderr: 'msgate: run not found\n' }
    if (argv[1] === 'result') return ok(output)
    if (argv[1] === 'cancel') return ok('')
    throw new Error('unexpected msgate call: ' + argv.join(' '))
  }
}

const commands = (w: { runs: { argv: string[] }[] }) => w.runs.map((r) => r.argv.slice(1))

test('list returns the session\'s runs the calling plugin started, and no other', async ($, on) => {
  const w = world(on)
  w.respond = binary()

  const out: any = await msgateAs(w, 'p2').jobs({ action: 'list' })

  expect(commands(w)).toEqual([['status', '--json', '--session-id', SESSION]])
  expect(out.value.runs.map((run: any) => run.run_id)).toEqual(['run-mine'])
  expect((await msgateAs(w, OTHER_CALLER).jobs({ action: 'list' }) as any).value.runs.map((run: any) => run.run_id)).toEqual(['run-theirs'])
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

test('jobs is refused when the listing cannot be read', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 70, stdout: '', stderr: 'msgate: cannot resolve the home\n' })

  expect(await msgateAs(w).jobs({ action: 'list' })).toEqual({ deny: 'msgate:jobs:msgate exited 70: cannot resolve the home' })
})

test('jobs is refused when the process cannot be run', async ($, on) => {
  const w = world(on)
  w.respond = () => Promise.reject(new Error('spawn failed'))

  expect(await msgateAs(w).jobs({ action: 'result', run_id: 'run-mine' })).toEqual({ deny: 'msgate:jobs:msgate could not run: spawn failed' })
})
