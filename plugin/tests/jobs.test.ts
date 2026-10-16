import { expect, test } from 'claude-code/testing'
import { SESSION, TOOL, isShim, job, listing, ok, world } from './kit'

async function call($: any, input: Record<string, unknown>) {
  return $.tool.call({ tool: TOOL('jobs'), ...input })
}

test('jobs list asks for the session status as JSON', async ($, on) => {
  const w = world(on)
  w.respond = () => listing(job())
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'list' })

  expect(isShim(w.runs[0]!.argv[0]!)).toBe(true)
  expect(w.runs[0]!.argv.slice(1)).toEqual(['status', '--json', '--session-id', SESSION])
  expect(JSON.parse(out.result).runs[0].run_id).toBe('run-1')
})

test('jobs status, result and cancel map to the matching commands', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => {
    switch (argv[1]) {
      case 'status': return ok(JSON.stringify(job({ state: 'done', outcome: 'ok' })))
      case 'result': return ok('the output\n')
      case 'cancel': return ok('cancellation requested for run-1\n')
    }
    throw new Error('unexpected ' + argv.join(' '))
  }
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const status: any = await call($, { action: 'status', run_id: 'run-1' })
  const result: any = await call($, { action: 'result', run_id: 'run-1' })
  const cancel: any = await call($, { action: 'cancel', run_id: 'run-1' })

  expect(w.runs.map((r) => r.argv.slice(1))).toEqual([
    ['status', 'run-1', '--json'],
    ['result', 'run-1'],
    ['cancel', 'run-1'],
  ])
  expect(JSON.parse(status.result).state).toBe('done')
  expect(result.result).toBe('the output\n')
  expect(cancel.result).toContain('cancellation requested')
})

test('jobs status returns what msgate printed, unchanged', async ($, on) => {
  const w = world(on)
  const printed = JSON.stringify(job({ state: 'done', outcome: 'ok' })) + '\n'
  w.respond = () => ok(printed)
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const status: any = await call($, { action: 'status', run_id: 'run-1' })

  expect(status.result).toBe(printed)
})

test('jobs cancel says the cancellation was requested when msgate prints nothing', async ($, on) => {
  const w = world(on)
  w.respond = () => ok('')
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const cancel: any = await call($, { action: 'cancel', run_id: 'run-1' })

  expect(cancel.result).toBe('cancellation requested for run-1')
})

test('jobs maps a missing run to an error the model can read', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 4, stdout: JSON.stringify({ sdk_status: 'not_found', exit_code: 4, error: 'no run "nope"' }), stderr: '' })
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'status', run_id: 'nope' })

  expect(out.deny).toContain('no run "nope"')
})

test('jobs reads the plain stderr of commands that print no JSON', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 3, stdout: '', stderr: 'msgate: run run-1 is still running; wait for it with: msgate wait run-1\n' })
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'result', run_id: 'run-1' })

  expect(out.deny).toContain('still running')
})

test('jobs rejects bad input without running msgate', async ($, on) => {
  const w = world(on)
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const noId: any = await call($, { action: 'status' })
  const flagLike: any = await call($, { action: 'cancel', run_id: '--all' })
  const unknown: any = await call($, { action: 'purge', run_id: 'run-1' })

  expect(w.runs.length).toBe(0)
  for (const out of [noId, flagLike, unknown]) expect(typeof out.deny).toBe('string')
  expect(noId.deny).toContain('run_id')
  expect(unknown.deny).toContain('action')
})

const KIB64 = 65536

test('jobs result returns output up to 64 KiB whole', async ($, on) => {
  const w = world(on)
  const output = 'q'.repeat(KIB64)
  w.respond = () => ok(output)
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'result', run_id: 'run-1' })

  expect(out.result).toBe(output)
  expect(w.runs.length).toBe(1)
})

test('jobs result cuts longer output at 64 KiB and names the output path', async ($, on) => {
  const w = world(on)
  const output = 'a'.repeat(KIB64) + 'Z'.repeat(5000)
  w.respond = (argv) => (argv[1] === 'result' ? ok(output) : ok(JSON.stringify(job({ output_path: '/state/runs/run-1/output.md' }))))
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'result', run_id: 'run-1' })

  expect(out.result.startsWith('a'.repeat(KIB64))).toBe(true)
  expect(out.result).not.toContain('Z')
  expect(out.result).toContain('/state/runs/run-1/output.md')
  expect(w.runs.map((r) => r.argv.slice(1))).toEqual([['result', 'run-1'], ['status', 'run-1', '--json']])
})

test('jobs result cut never splits a character', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'result' ? ok('€'.repeat(30000)) : ok(JSON.stringify(job())))
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'result', run_id: 'run-1' })

  expect(out.result).toContain('€'.repeat(21845)) // 65535 bytes
  expect(out.result).not.toContain('€'.repeat(21846))
  expect(out.result).not.toContain('\uFFFD')
})

test('jobs result still cuts when the output path cannot be looked up', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (argv[1] === 'result' ? ok('a'.repeat(KIB64 + 10)) : { exitCode: 70, stdout: '', stderr: 'boom' })
  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work' })

  const out: any = await call($, { action: 'result', run_id: 'run-1' })

  expect(out.result.startsWith('a'.repeat(KIB64))).toBe(true)
  expect(out.result).toContain('cut at 64 KiB')
})
