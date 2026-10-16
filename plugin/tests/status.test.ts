import { expect, test } from 'claude-code/testing'
import { POLL_MS, SESSION, job, listing, ok, otherRuns, queued, seeded, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const

test('the status line counts the running jobs of the session and clears at zero', async ($, on) => {
  const w = world(on)
  let runs: any[] = [job({ run_id: 'a', state: 'running' }), job({ run_id: 'b', state: 'queued' })]
  w.respond = () => listing(...runs)
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.statuses.at(-1)).toContain('2')

  runs = [job({ run_id: 'a', state: 'running' }), job({ run_id: 'b', state: 'done', outcome: 'ok' })]
  w.respond = (argv) => (argv[1] === 'result' ? ok('') : listing(...runs))
  await w.clock.advance(POLL_MS)
  expect(w.statuses.at(-1)).toContain('1')

  runs = [job({ run_id: 'a', state: 'done', outcome: 'ok' }), job({ run_id: 'b', state: 'done', outcome: 'ok' })]
  await w.clock.advance(POLL_MS)
  expect(w.statuses.at(-1)).toBeUndefined()
})

test('foreground runs of the session are not counted as jobs', async ($, on) => {
  const w = world(on)
  w.respond = () => listing(job({ run_id: 'fg', state: 'running', background: false }))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.statuses.at(-1)).toBeUndefined()
})

test('the poll lists this session only, every fifteen seconds', async ($, on) => {
  const w = world(on)
  w.respond = () => listing()
  await $.session.start(START)

  // The spawn reads are the same listing, five seconds apart: two before the
  // poll's period ends, and with it a third beside the poll's own.
  await w.clock.advance(POLL_MS - 1)
  expect(otherRuns(w).length).toBe(2)
  await w.clock.advance(1)
  expect(otherRuns(w).length).toBe(4)
  expect(otherRuns(w).every((r) => r.argv.slice(1).join(' ') === ['status', '--json', '--session-id', SESSION].join(' '))).toBe(true)
  await w.clock.advance(POLL_MS)
  expect(otherRuns(w).length).toBe(8)
})

test('a tick is skipped while the previous one is still running', async ($, on) => {
  const w = world(on)
  w.respond = async () => {
    await w.clock.sleep(40000)
    return listing()
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 3)

  // The poll reads once at 15s and the spawn scan at 5s and again at 45s, when
  // its first read ends; the ticks that arrive meanwhile read nothing.
  expect(otherRuns(w).length).toBe(3)
})

test('a failing poll does not stop the next one', async ($, on) => {
  const w = world(on, { store: seeded([queued('run-1')]) })
  let n = 0
  w.respond = (argv) => {
    if (argv[1] === 'result') return ok('done!')
    if (argv[2] === '--json') return listing()
    n += 1
    if (n === 1) throw new Error('process timed out')
    if (n === 2) return { exitCode: 70, stdout: '', stderr: 'boom' }
    return ok(JSON.stringify(job({ state: 'done', outcome: 'ok' })))
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS * 3)

  expect(w.submits.length).toBe(1)
})

test('a failing session listing does not keep a queued job from its notice', async ($, on) => {
  const w = world(on, { store: seeded([queued('run-1')]) })
  w.respond = (argv) => {
    if (argv[1] === 'result') return ok('done!')
    if (argv[2] === '--json') return { exitCode: 70, stdout: '', stderr: 'boom' }
    return ok(JSON.stringify(job({ state: 'done', outcome: 'ok' })))
  }
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.submits.length).toBe(1)
})

test('/msgate-jobs is registered to run mid-turn and prints the recent jobs at once', async ($, on) => {
  const w = world(on)
  w.respond = () =>
    listing(
      job({ run_id: 'run-2', state: 'running', purpose: 'code-review', conversation_id: 'conv-9' }),
      job({ run_id: 'run-1', state: 'done', outcome: 'ok' }),
      job({ run_id: 'fg', state: 'done', background: false }),
    )
  await $.session.start(START)

  const spec = w.commands.find((c) => c.name === 'msgate-jobs')
  expect(spec).toBeDefined()
  expect(spec.immediate).toBe(true)
  expect(spec.description.length).toBeGreaterThan(0)

  const out: any = await $.command.run({ command: 'msgate-jobs', args: '' })

  expect(w.runs[0]!.argv.slice(1)).toEqual(['status', '--json', '--session-id', SESSION])
  for (const part of ['run-2', 'running', 'code-review', 'conv-9', 'run-1', 'done']) expect(out.text).toContain(part)
  expect(out.text).not.toContain('fg')
})

test('/msgate-jobs says so when the session has no jobs, and reports msgate failures', async ($, on) => {
  const w = world(on)
  w.respond = () => listing()
  await $.session.start(START)

  const none: any = await $.command.run({ command: 'msgate-jobs', args: '' })
  expect(none.text).toContain('No')

  w.respond = () => ({ exitCode: 70, stdout: '', stderr: 'msgate: cannot resolve the home\n' })
  const failed: any = await $.command.run({ command: 'msgate-jobs', args: '' })
  expect(failed.text).toContain('cannot resolve the home')
})

test('jobs started from the CLI are not counted in the status line', async ($, on) => {
  const w = world(on)
  w.respond = () =>
    listing(
      job({ run_id: 'mine', state: 'running' }),
      job({ run_id: 'theirs', state: 'running', source: 'cli' }),
      job({ run_id: 'hook', state: 'running', source: 'cr-parallel' }),
    )
  await $.session.start(START)

  await w.clock.advance(POLL_MS)

  expect(w.statuses.at(-1)).toContain('1')
  expect(w.statuses.at(-1)).not.toContain('3')
})

test('the status line joins the jobs and the costs with a dot, and the two costs with a bar', async ($, on) => {
  const w = world(on)
  const costs = (codex: string | null, anthropic: string | null) =>
    JSON.stringify({ usage_by_provider: { codex: { cost_usd: codex, cost_complete: true }, anthropic: { cost_usd: anthropic, cost_complete: true } } })
  let answer = costs('5.820000', '0.030000')
  let runs: any[] = [job({ run_id: 'a', state: 'running' }), job({ run_id: 'b', state: 'done', outcome: 'ok' })]
  w.respond = (argv) => (argv[1] === 'result' ? ok('') : argv[1] === 'stats' ? ok(answer) : listing(...runs))
  await $.session.start(START)

  await w.clock.advance(POLL_MS)
  expect(w.statuses.at(-1)).toBe('💸 1 job running · Codex $5.82 | Claude $0.03')

  runs = [job({ run_id: 'a', state: 'done', outcome: 'ok' }), job({ run_id: 'b', state: 'done', outcome: 'ok' })]
  await w.clock.advance(POLL_MS * 2)
  expect(w.statuses.at(-1)).toBe('💸 Codex $5.82 | Claude $0.03')

  answer = costs(null, '0.030000')
  runs = [job({ run_id: 'a', state: 'running' }), job({ run_id: 'b', state: 'done', outcome: 'ok' }), job({ run_id: 'c', state: 'done', outcome: 'ok' })]
  await w.clock.advance(POLL_MS * 2)
  expect(w.statuses.at(-1)).toBe('💸 1 job running · Claude $0.03')
})

test('/msgate-jobs lists the purpose of a run that reports it as scenario', async ($, on) => {
  const w = world(on)
  w.respond = () => listing(job({ run_id: 'run-2', state: 'running', purpose: undefined, scenario: 'delegation' }))
  await $.session.start(START)

  const out: any = await $.command.run({ command: 'msgate-jobs', args: '' })

  expect(out.text).toContain('delegation')
})
