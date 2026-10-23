import { expect, test } from 'claude-code/testing'
import { SESSION, TOOL, isShim, ok, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ADMITTED = { conversation_id: 'conv-1', run_id: 'run-1', state: 'running', run_dir: '/s/runs/run-1', output_path: '/s/runs/run-1/output.md' }

test('run without a conversation admits a background exec and returns its ids', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), prompt: 'review this' })

  // The status read that follows the admission is the spawn notice's.
  expect(w.runs.filter((r) => r.argv[1] === 'exec').length).toBe(1)
  const run = w.runs[0]!
  expect(isShim(run.argv[0]!)).toBe(true)
  expect(run.argv.slice(1)).toEqual(['exec', '--background', '--json', '--source', 'mod', '--session-id', SESSION, '--agent-feedback', '-'])
  // The prompt travels on standard input, never inside an argument.
  expect(run.stdin).toBe('review this')
  expect(JSON.parse(out.result)).toEqual({ conversation_id: 'conv-1', run_id: 'run-1', continued: false })
})

test('run with a conversation id sends the next turn of that conversation', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, run_id: 'run-2' }))
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), conversation_id: 'conv-1', prompt: 'and now the tests', effort: 'low', cwd: '/repo' })

  const run = w.runs[0]!
  expect(isShim(run.argv[0]!)).toBe(true)
  expect(run.argv.slice(1)).toEqual([
    'send', 'conv-1', '--background', '--json', '--source', 'mod', '--session-id', SESSION, '--agent-feedback', '--effort', 'low', '--cwd', '/repo', '-',
  ])
  expect(run.stdin).toBe('and now the tests')
  expect(JSON.parse(out.result)).toEqual({ conversation_id: 'conv-1', run_id: 'run-2', continued: true })
})

test('run passes a Codex thread id through to send unchanged and returns the conversation id the engine reports', async ($, on) => {
  const thread = '00000000-0000-4000-8000-000000000003'
  const w = world(on)
  w.respond = () => ok(JSON.stringify({ ...ADMITTED, conversation_id: 'c-20261022T000000Z-abcdef01', run_id: 'run-3' }))
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), conversation_id: thread, prompt: 'pick this thread back up' })

  const run = w.runs[0]!
  expect(isShim(run.argv[0]!)).toBe(true)
  expect(run.argv.slice(1)).toEqual(['send', thread, '--background', '--json', '--source', 'mod', '--session-id', SESSION, '--agent-feedback', '-'])
  expect(run.stdin).toBe('pick this thread back up')
  expect(JSON.parse(out.result)).toEqual({ conversation_id: 'c-20261022T000000Z-abcdef01', run_id: 'run-3', continued: true })
})

test('run passes the optional settings as flags, the purpose as --purpose', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)

  await $.tool.call({
    tool: TOOL('run'), prompt: '--not-a-flag; $(rm -rf x)', provider: 'codex', purpose: 'delegation',
    model: 'm1', effort: 'high', sandbox: 'read-only', cwd: '/repo',
  })

  expect(w.runs[0]!.argv.slice(1)).toEqual([
    'exec', '--background', '--json', '--source', 'mod', '--session-id', SESSION, '--agent-feedback',
    '--provider', 'codex', '--purpose', 'delegation', '--model', 'm1', '--effort', 'high',
    '--sandbox', 'read-only', '--cwd', '/repo', '-',
  ])
  expect(w.runs[0]!.stdin).toBe('--not-a-flag; $(rm -rf x)')
})

test('run turns a msgate failure into an error the model can read, without the stderr prefix', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 2, stdout: '', stderr: 'msgate: unknown provider "x"\n' })
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), prompt: 'hi', provider: 'x' })

  expect(out.deny).toBe('msgate exited 2: unknown provider "x"')
})

test('run reports a busy conversation as an error', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 3, stdout: JSON.stringify({ sdk_status: 'busy', exit_code: 3, error: 'conversation conv-1 is busy: run run-1 is still queued or running' }), stderr: '' })
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), conversation_id: 'conv-1', prompt: 'again' })

  expect(out.deny).toContain('is busy')
})

test('run rejects an empty prompt without running msgate', async ($, on) => {
  const w = world(on)
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), prompt: '   ' })

  expect(w.runs.length).toBe(0)
  expect(out.deny).toContain('prompt')
})

test('run refuses a conversation id that could read as a flag', async ($, on) => {
  const w = world(on)
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), conversation_id: '--json', prompt: 'x' })

  expect(w.runs.length).toBe(0)
  expect(out.deny).toContain('conversation_id')
})

test('run with an empty conversation id starts a new conversation', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(ADMITTED))
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: TOOL('run'), conversation_id: '', prompt: 'x' })

  expect(w.runs[0]!.argv[1]).toBe('exec')
  expect(JSON.parse(out.result).continued).toBe(false)
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
    await $.session.start(START)

    await $.tool.call({ tool: TOOL('run'), prompt: 'q', ...args })

    const run = w.runs.find((r) => r.argv[1] === command)!
    expect(run.argv.slice(-tail.length)).toEqual(tail)
    expect(run.argv.includes('--harness')).toBe(tail.includes('--harness'))
  })
}
