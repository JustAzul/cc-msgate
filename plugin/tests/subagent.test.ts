import { expect, test } from 'claude-code/testing'
import { SPAWN_WAIT_MS } from '../hooks/efforts.js'
import { AGENTS, PROFILES, SESSION, agentCall, isShim, msgateAs, ok, startTurn, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const HANDOFF_FAILED = 'msgate: the hand-off to the agent failed, so this is not its answer.'
const JOB_FLAGS = ['--background', '--json', '--source', 'agent', '--session-id', SESSION]
// The admission of a row's first turn: the type's own name as the purpose, the
// settings the type is backed with passed as flags of the profile source.
const ADMISSION = (purpose: string) => [...JOB_FLAGS.slice(0, 2), '--purpose', purpose, ...JOB_FLAGS.slice(2), '--settings-source', 'profile']
const READ_ONLY_HIGH = ['--model', 'gpt-6.1-sol', '--effort', 'high', '--sandbox', 'read-only']

function admitted(runId = 'run-1', conversationId = 'conv-1') {
  return ok(JSON.stringify({ conversation_id: conversationId, run_id: runId, state: 'running', sdk_status: 'ok', exit_code: 0 }))
}

function finished(over: Record<string, unknown> = {}) {
  return ok(JSON.stringify({
    run_id: 'run-1', conversation_id: 'conv-1', state: 'done', outcome: 'ok', error_excerpt: null,
    output_path: '/state/runs/run-1/output.md', ...over,
  }))
}

// codex answers every msgate call of one successful turn.
function codex(output: string, over: Record<string, unknown> = {}) {
  return (argv: string[]) => {
    const command = argv[1]
    if (command === 'exec' || command === 'send' || command === 'review') return admitted()
    if (command === 'wait') return finished(over)
    if (command === 'status') return finished(over)
    if (command === 'result') return ok(output)
    if (command === 'progress') return ok(JSON.stringify({ run_id: 'run-1', state: 'done', next: Number(argv[4]), entries: [] }))
    throw new Error('unexpected msgate call: ' + argv.join(' '))
  }
}

function subagent(w: any, id: string, type: string, rows: { role: 'user' | 'assistant'; text: string }[]) {
  w.agents.push({ id, type })
  w.messages[id] = rows
}

test('a subagent of an agent type is answered by an msgate job, never by the model', async ($, on) => {
  const w = world(on)
  w.respond = codex('Codex says the cache is safe.\n')
  subagent(w, 'ag-1', 'msgate-agents:second-opinion', [{ role: 'user', text: 'Is a TTL cache safe here?' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(w.modelSteps).toEqual([])
  const admit = w.runs[0]!
  expect(isShim(admit.argv[0]!)).toBe(true)
  expect(admit.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), ...READ_ONLY_HIGH, '-'])
  // The prompt travels on standard input, never inside an argument.
  expect(admit.stdin).toBe('Is a TTL cache safe here?')
  // The spawn notice's ETA lookup runs beside the turn.
  expect(w.runs.map((r) => r.argv[1]).filter((command) => command !== 'eta')).toEqual(['exec', 'wait', 'progress', 'status', 'result'])
  expect(delivered(out)).toContain('Codex says the cache is safe.')
  expect(delivered(out)).toContain('[msgate run run-1 · conversation conv-1 · ok]')
  expect(out.result.stopReason).toBe('tool_use')
  expect(out.chunks.at(-1)).toMatchObject({ kind: 'stop', stopReason: 'tool_use' })
})

// The message a step hands back to the agent's caller.
const delivered = (out: any): string => out.result.toolUses[0].input.message

test('every answer of an msgate subagent is handed back with SubagentHandback, the failed hand-offs included', async ($, on) => {
  const w = world(on)
  w.respond = codex('Codex says the cache is safe.\n')
  subagent(w, 'ag-1', 'msgate-agents:second-opinion', [{ role: 'user', text: 'Is a TTL cache safe here?' }])
  subagent(w, 'ag-2', 'msgate-agents:code-review', [{ role: 'user', text: 'please review my changes' }])
  await $.session.start(START)

  const done = await step($, 'ag-1')
  const refused = await step($, 'ag-2')

  expect(done.result.stopReason).toBe('tool_use')
  expect(done.result.answer).toBe('')
  expect(done.result.toolUses).toEqual([{ name: 'SubagentHandback', input: { message: delivered(done) } }])
  expect(delivered(done)).toContain('Codex says the cache is safe.')
  expect(done.chunks.filter((c) => c.kind === 'text')).toEqual([])
  expect(done.chunks.find((c) => c.kind === 'tool')).toMatchObject({ name: 'SubagentHandback' })
  expect(done.chunks.find((c) => c.kind === 'input')!.json).toBe(JSON.stringify({ message: delivered(done) }))
  expect(done.chunks.at(-1)).toMatchObject({ kind: 'stop', stopReason: 'tool_use' })

  expect(refused.result.stopReason).toBe('tool_use')
  expect(refused.result.toolUses[0].name).toBe('SubagentHandback')
  expect(delivered(refused).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(refused.chunks.filter((c) => c.kind === 'text')).toEqual([])
})

test('model requests of the main loop and of other agents reach the model untouched', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-x', 'Explore', [{ role: 'user', text: 'find it' }])
  await $.session.start(START)

  const main = await step($)
  const other = await step($, 'ag-x')

  expect(w.modelSteps).toEqual([undefined, 'ag-x'])
  expect(main.text).toBe('a Claude model answered')
  expect(other.text).toBe('a Claude model answered')
  expect(w.runs).toEqual([])
})

test('reminders the engine adds to the prompt are not sent to the agent', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [
    { role: 'user', text: '<system-reminder>\nsession context\n</system-reminder>\nSummarize README.md' },
  ])
  await $.session.start(START)

  await step($, 'ag-1')

  expect(w.runs[0]!.stdin).toBe('Summarize README.md')
})

test('a later step of the same turn repeats the answer without calling msgate again', async ($, on) => {
  const w = world(on)
  w.respond = codex('Codex says the cache is safe.\n')
  subagent(w, 'ag-1', 'msgate-agents:second-opinion', [{ role: 'user', text: 'Is a TTL cache safe here?' }])
  subagent(w, 'ag-2', 'msgate-agents:code-review', [{ role: 'user', text: 'please review my changes' }])
  await $.session.start(START)
  const first = await step($, 'ag-1', { turnId: 'turn-1', index: 0 })
  const failed = await step($, 'ag-2', { turnId: 'turn-1', index: 0 })
  w.runs.length = 0

  const again = await step($, 'ag-1', { turnId: 'turn-1', index: 1 })
  const failedAgain = await step($, 'ag-2', { turnId: 'turn-1', index: 1 })

  expect(w.runs).toEqual([])
  expect(w.modelSteps).toEqual([])
  expect(again.text).toBe(delivered(first))
  expect(again.chunks.filter((c) => c.kind === 'text').map((c) => c.text)).toEqual([delivered(first)])
  expect(again.result).toMatchObject({ turnId: 'turn-1', index: 1, answer: delivered(first), toolUses: [], stopReason: 'end_turn' })
  expect(again.chunks.at(-1)).toMatchObject({ kind: 'stop', stopReason: 'end_turn' })
  expect(failedAgain.result).toMatchObject({ answer: delivered(failed), toolUses: [], stopReason: 'end_turn' })
})

test('a new turn of the agent replaces what the earlier turn remembered', async ($, on) => {
  const w = world(on)
  w.respond = codex('First answer.')
  subagent(w, 'ag-1', 'msgate-agents:cross-check', [{ role: 'user', text: 'Check auth.go' }])
  await $.session.start(START)
  await step($, 'ag-1', { turnId: 'turn-1' })

  w.messages['ag-1'] = [
    { role: 'user', text: 'Check auth.go' },
    { role: 'assistant', text: 'First answer.' },
    { role: 'user', text: 'And store.go?' },
  ]
  w.respond = codex('Second answer.')
  const second = await step($, 'ag-1', { turnId: 'turn-2' })
  w.runs.length = 0
  const again = await step($, 'ag-1', { turnId: 'turn-2', index: 1 })

  expect(delivered(second)).toContain('Second answer.')
  expect(again.text).toBe(delivered(second))
  expect(w.runs).toEqual([])
})

test('a SendMessage to the subagent continues its msgate conversation', async ($, on) => {
  const w = world(on)
  w.respond = codex('First answer.')
  subagent(w, 'ag-1', 'msgate-agents:cross-check', [{ role: 'user', text: 'Check auth.go' }])
  await $.session.start(START)
  await step($, 'ag-1')

  w.messages['ag-1'] = [
    { role: 'user', text: 'Check auth.go' },
    { role: 'assistant', text: 'First answer.' },
    { role: 'user', text: 'Which finding is cheapest to fix?' },
  ]
  w.runs.length = 0
  const out = await step($, 'ag-1', { turnId: 'turn-2' })

  expect(w.runs[0]!.argv.slice(1)).toEqual(['send', 'conv-1', ...JOB_FLAGS, '-'])
  expect(w.runs[0]!.stdin).toBe('Which finding is cheapest to fix?')
  expect(delivered(out)).toContain('First answer.')
  expect(w.modelSteps).toEqual([])
})

test('a later turn whose conversation is not recorded is refused, not started afresh', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [
    { role: 'user', text: 'hi' },
    { role: 'assistant', text: 'hello' },
    { role: 'user', text: 'and now?' },
  ])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(delivered(out).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(delivered(out)).toContain('run tool')
  expect(w.runs).toEqual([])
  expect(w.modelSteps).toEqual([])
})

test('a subagent whose session saved no transcript is answered from the prompt its turn started with', async ($, on) => {
  const w = world(on)
  w.respond = codex('Codex says the cache is safe.\n')
  subagent(w, 'ag-1', 'msgate-agents:second-opinion', [])
  await $.session.start(START)
  await startTurn($, 'ag-1', 'Is a TTL cache safe here?')

  const out = await step($, 'ag-1')

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), ...READ_ONLY_HIGH, '-'])
  expect(w.runs[0]!.stdin).toBe('Is a TTL cache safe here?')
  expect(delivered(out)).toContain('Codex says the cache is safe.')
  expect(w.modelSteps).toEqual([])
})

test('reminders in the prompt a turn started with are not sent to the agent', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [])
  await $.session.start(START)
  await startTurn($, 'ag-1', '<system-reminder>\nsession context\n</system-reminder>\nSummarize README.md')

  await step($, 'ag-1')

  expect(w.runs[0]!.stdin).toBe('Summarize README.md')
})

test('a SendMessage is sent as the saved message, without the framing its turn started with', async ($, on) => {
  const w = world(on, { store: { 'agent-conversations': [{ agent_id: 'ag-1', conversation_id: 'conv-1' }] } })
  w.respond = codex('Second answer.')
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [
    { role: 'user', text: 'First question' },
    { role: 'assistant', text: 'First answer.' },
    { role: 'user', text: 'Which finding is cheapest to fix?' },
  ])
  await $.session.start(START)
  await startTurn($, 'ag-1', 'The coordinator sent a message while you were working:\nWhich finding is cheapest to fix?\n\nAddress this before completing your current task.', 'turn-2')

  await step($, 'ag-1', { turnId: 'turn-2' })

  expect(w.runs[0]!.argv.slice(1)).toEqual(['send', 'conv-1', ...JOB_FLAGS, '-'])
  expect(w.runs[0]!.stdin).toBe('Which finding is cheapest to fix?')
})

test('a turn that started with no text is answered from the saved prompt', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'Summarize README.md' }])
  await $.session.start(START)
  await startTurn($, 'ag-1', '')

  await step($, 'ag-1')

  expect(w.runs[0]!.stdin).toBe('Summarize README.md')
})

test('the prompt a turn started with is never used for a later turn', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [])
  await $.session.start(START)
  await startTurn($, 'ag-1', 'first question', 'turn-1')

  const out = await step($, 'ag-1', { turnId: 'turn-2' })

  expect(delivered(out).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(delivered(out)).toContain('the prompt is empty')
  expect(w.runs).toEqual([])
})

test('a turn with no prompt anywhere is a hand-off failure and starts no msgate process', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [])
  await $.session.start(START)
  await startTurn($, 'ag-1', '')

  const out = await step($, 'ag-1')

  expect(delivered(out).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(delivered(out).endsWith('the prompt is empty')).toBe(true)
  expect(w.runs).toEqual([])
  expect(w.modelSteps).toEqual([])
})

test('a SendMessage to a subagent whose session saved no transcript continues its recorded conversation', async ($, on) => {
  const w = world(on, { store: { 'agent-conversations': [{ agent_id: 'ag-1', conversation_id: 'conv-1' }] } })
  w.respond = codex('Second answer.')
  subagent(w, 'ag-1', 'msgate-agents:cross-check', [])
  await $.session.start(START)
  await startTurn($, 'ag-1', 'Which finding is cheapest to fix?', 'turn-2')

  const out = await step($, 'ag-1', { turnId: 'turn-2' })

  expect(w.runs[0]!.argv.slice(1)).toEqual(['send', 'conv-1', ...JOB_FLAGS, '-'])
  expect(w.runs[0]!.stdin).toBe('Which finding is cheapest to fix?')
  expect(delivered(out)).toContain('Second answer.')
  expect(w.modelSteps).toEqual([])
})

test('a code-review subagent reads its prompt as the review target', async ($, on) => {
  const w = world(on)
  w.respond = codex('[P2] auth.go:12 token not checked')
  subagent(w, 'ag-1', 'msgate-agents:code-review', [{ role: 'user', text: 'base feature/login' }])
  subagent(w, 'ag-2', 'msgate-agents:code-review', [{ role: 'user', text: ' uncommitted ' }])
  await $.session.start(START)

  await step($, 'ag-1')
  await step($, 'ag-2')

  const reviews = w.runs.filter((r) => r.argv[1] === 'review')
  expect(reviews.map((r) => r.argv.slice(1))).toEqual([
    ['review', '--base', 'feature/login', ...ADMISSION('code-review'), ...READ_ONLY_HIGH],
    ['review', '--uncommitted', ...ADMISSION('code-review'), ...READ_ONLY_HIGH],
  ])
  expect(reviews[0]!.stdin).toBeUndefined()
})

test('a code-review prompt that is not exactly one target is refused before msgate runs', async ($, on) => {
  const w = world(on)
  for (const [id, text] of [['a', 'please review my changes'], ['b', 'base -rf'], ['c', 'commit']]) {
    subagent(w, id, 'msgate-agents:code-review', [{ role: 'user', text }])
  }
  await $.session.start(START)

  for (const id of ['a', 'b', 'c']) {
    const out = await step($, id)
    expect(delivered(out).startsWith(HANDOFF_FAILED)).toBe(true)
    expect(delivered(out)).toContain("'uncommitted', 'base <branch>' or 'commit <sha>'")
  }
  expect(w.runs).toEqual([])
  expect(w.modelSteps).toEqual([])
})

test('an msgate failure becomes an answer that says the hand-off failed', async ($, on) => {
  const w = world(on)
  w.respond = () => ({ exitCode: 3, stdout: JSON.stringify({ sdk_status: 'conversation_busy', exit_code: 3, error: 'conversation c-1 has an active turn' }), stderr: '' })
  subagent(w, 'ag-1', 'msgate-agents:delegation', [{ role: 'user', text: 'Rename foo to bar in util.go' }])
  subagent(w, 'ag-2', 'msgate-agents:delegation', [{ role: 'user', text: 'Rename foo to bar in util.go' }])
  await $.session.start(START)

  const refused = await step($, 'ag-1')
  w.respond = () => {
    throw new Error('process timed out')
  }
  const thrown = await step($, 'ag-2')

  expect(delivered(refused).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(delivered(refused)).toContain('conversation c-1 has an active turn')
  // The harness reports a throwing process.run in its own words; what holds is
  // that the rejection still ends in a hand-off failure.
  expect(delivered(thrown).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(w.modelSteps).toEqual([])
})

test('a run is waited for in short slices until it finishes', async ($, on) => {
  const w = world(on)
  const answers = codex('late answer')
  let waits = 0
  w.respond = (argv) => {
    if (argv[1] !== 'wait') return answers(argv)
    waits += 1
    return waits < 3 ? { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5, error: 'run run-1 is still running after 9m0s' }), stderr: '' } : finished()
  }
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'slow one' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(waits).toBe(3)
  expect(w.runs.find((r) => r.argv[1] === 'wait')!.argv.slice(1)).toEqual(['wait', 'run-1', '--timeout', '5', '--json'])
  expect(delivered(out)).toContain('late answer')
})

test('a run that did not deliver leads its answer with how it ended', async ($, on) => {
  const w = world(on)
  w.respond = codex('', { state: 'timeout', outcome: 'timeout', error_excerpt: 'stream disconnected' })
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(delivered(out).startsWith('msgate run run-1 ended timeout: stream disconnected')).toBe(true)
  expect(delivered(out)).toContain('· timeout]')
})

test('output over 64 KiB is cut and the answer names the output file', async ($, on) => {
  const w = world(on)
  w.respond = codex('x'.repeat(70000))
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(delivered(out)).toContain('x'.repeat(65536) + '\n[output cut at 64 KiB; full output at /state/runs/run-1/output.md]')
  expect(delivered(out)).not.toContain('x'.repeat(65537))
})

test('the conversations of two subagents are both recorded', async ($, on) => {
  const w = world(on)
  let n = 0
  w.respond = (argv) => {
    if (argv[1] === 'exec') {
      n += 1
      return admitted('run-' + n, 'conv-' + n)
    }
    return codex('ok')(argv)
  }
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'one' }])
  subagent(w, 'ag-2', 'msgate-agents:adhoc', [{ role: 'user', text: 'two' }])
  await $.session.start(START)

  await Promise.all([step($, 'ag-1'), step($, 'ag-2')])

  expect(w.store.get('agent-conversations')).toEqual([
    { agent_id: 'ag-1', conversation_id: 'conv-1' },
    { agent_id: 'ag-2', conversation_id: 'conv-2' },
  ])
})

test('tool calls made inside an msgate subagent are refused', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-1', 'msgate-agents:delegation', [{ role: 'user', text: 'q' }])
  subagent(w, 'ag-x', 'Explore', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const inside: any = await $.tool.call({ tool: 'Bash', command: 'rm -rf build', agentId: 'ag-1' } as any)
  const other: any = await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'ag-x' } as any)
  const main: any = await $.tool.call({ tool: 'Bash', command: 'ls' } as any)

  expect(inside.deny).toContain('msgate agents run no tools')
  expect(other.result).toBe('unanswered')
  expect(main.result).toBe('unanswered')
})

test('an msgate subagent may call SubagentHandback and no other tool', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-1', 'msgate-agents:delegation', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const handback: any = await $.tool.call({ tool: 'SubagentHandback', message: 'report', agentId: 'ag-1' } as any)
  const bash: any = await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'ag-1' } as any)
  const read: any = await $.tool.call({ tool: 'Read', file_path: '/etc/passwd', agentId: 'ag-1' } as any)

  expect(handback.result).toBe('unanswered')
  expect(bash.deny).toContain('msgate agents run no tools')
  expect(read.deny).toContain('msgate agents run no tools')
})

test('a run that ended with exit code 5 is not waited for again', async ($, on) => {
  const w = world(on)
  const answers = codex('', { state: 'failed', outcome: 'error', error_excerpt: 'provider exited 5' })
  w.respond = (argv) => (argv[1] === 'wait' ? { ...finished({ state: 'failed', outcome: 'error' }), exitCode: 5 } : answers(argv))
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(w.runs.filter((r) => r.argv[1] === 'wait').length).toBe(1)
  expect(delivered(out).startsWith('msgate run run-1 ended error: provider exited 5')).toBe(true)
})

test('an output that cannot be read is a hand-off failure, not an empty answer', async ($, on) => {
  const w = world(on)
  const answers = codex('')
  w.respond = (argv) => (argv[1] === 'result' ? { exitCode: 70, stdout: '', stderr: 'msgate: reading output: permission denied\n' } : answers(argv))
  subagent(w, 'ag-1', 'msgate-agents:adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(delivered(out).startsWith(HANDOFF_FAILED)).toBe(true)
  expect(delivered(out)).toContain('permission denied')
  expect(delivered(out)).not.toContain('produced no output')
})


test('an agent of a type that is not backed reaches the model, whoever registered it', async ($, on) => {
  const w = world(on, { backed: {} })
  subagent(w, 'ag-1', AGENTS + ':second-opinion', [{ role: 'user', text: 'q' }])
  subagent(w, 'ag-2', 'other:helper', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const first = await step($, 'ag-1')
  const second = await step($, 'ag-2')

  expect(w.modelSteps).toEqual(['ag-1', 'ag-2'])
  expect(first.text).toBe('a Claude model answered')
  expect(second.text).toBe('a Claude model answered')
  expect(w.runs).toEqual([])
})

test('the rows of any plugin\'s backed type are answered, with the type\'s own name as the purpose', async ($, on) => {
  const w = world(on, { backed: { 'p2:custom': { command: 'exec', promptMode: 'free' } } })
  w.respond = codex('done')
  subagent(w, 'ag-1', 'p2:custom', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const out = await step($, 'ag-1')

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('custom'), '-'])
  expect(delivered(out)).toContain('done')
  expect(w.modelSteps).toEqual([])
})

test('a row of a type backed with a purpose records that purpose, not the type\'s name', async ($, on) => {
  const w = world(on, { backed: { 'p2:second-opinion-gpt-6-astra': { command: 'exec', promptMode: 'free', purpose: 'second-opinion' } } })
  w.respond = codex('done')
  subagent(w, 'ag-1', 'p2:second-opinion-gpt-6-astra', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  await step($, 'ag-1')

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), '-'])
})

const SETTINGS: [string, string[]][] = [
  ['adhoc', []],
  ['delegation', ['--model', 'gpt-6.1-sol', '--effort', 'medium', '--sandbox', 'workspace-write']],
  ['expert-persona', ['--model', 'gpt-6.1-sol', '--effort', 'xhigh', '--sandbox', 'read-only']],
]

for (const [name, settings] of SETTINGS) {
  test(`a ${name} row passes the settings its type is backed with, and only those`, async ($, on) => {
    const w = world(on)
    w.respond = codex('done')
    subagent(w, 'ag-1', AGENTS + ':' + name, [{ role: 'user', text: 'q' }])
    await $.session.start(START)

    await step($, 'ag-1')

    expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION(name), ...settings, '-'])
  })
}

// An Agent call for a row of `type`, whose spawn starts the row's agent. The row
// has the call's prompt. Resolves the agent the spawn started.
async function callAgent($: any, w: ReturnType<typeof world>, type: string, over: Record<string, unknown> = {}, spawn: Record<string, unknown> = {}): Promise<string> {
  const call = await agentCall($, w, type, over)
  const prompt = String(over.prompt ?? 'q')
  const spawned = await call.spawn(prompt, spawn)
  await call.end()
  w.messages[spawned.agentId] = [{ role: 'user', text: prompt }]
  return spawned.agentId
}

const FLAGGED = (effort: string) => ['--effort', effort, '--effort-source', 'flag']

test('an Agent call with an explicit effort starts the row\'s run with that effort in place of the type\'s', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const agentId = await callAgent($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })

  await step($, agentId)

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), '--model', 'gpt-6.1-sol', ...FLAGGED('xhigh'), '--sandbox', 'read-only', '-'])
})

test('an Agent call with no effort starts the row\'s run with the type\'s own', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const agentId = await callAgent($, w, AGENTS + ':second-opinion')

  await step($, agentId)

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), ...READ_ONLY_HIGH, '-'])
})

test('an Agent call with an effort starts a review with that effort in its flags', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const agentId = await callAgent($, w, AGENTS + ':code-review', { effort: 'xhigh', prompt: 'uncommitted' })

  await step($, agentId)

  expect(w.runs[0]!.argv.slice(1)).toEqual(['review', '--uncommitted', ...ADMISSION('code-review'), '--model', 'gpt-6.1-sol', ...FLAGGED('xhigh'), '--sandbox', 'read-only'])
})

test('an effort is passed in its place among the settings of a type that names no effort', async ($, on) => {
  const w = world(on, { backed: { 'p2:scratch': { command: 'exec', promptMode: 'free', sandbox: 'read-only' }, [AGENTS + ':adhoc']: PROFILES[AGENTS + ':adhoc']! } })
  w.respond = codex('done')
  await $.session.start(START)
  const scratch = await callAgent($, w, 'p2:scratch', { effort: 'low' })
  const adhoc = await callAgent($, w, AGENTS + ':adhoc', { effort: 'max' })

  await step($, scratch)
  await step($, adhoc)

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('scratch'), ...FLAGGED('low'), '--sandbox', 'read-only', '-'])
  expect(w.runs.find((r) => r.argv[1] === 'exec' && r.argv.includes('adhoc'))!.argv.slice(1)).toEqual(['exec', ...ADMISSION('adhoc'), ...FLAGGED('max'), '-'])
})

test('an Agent call of a type nobody backed records no effort and its agent is answered by the model', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  const agentId = await callAgent($, w, 'other:thing', { effort: 'xhigh' })

  await step($, agentId)

  expect([...w.agentEfforts]).toEqual([])
  expect(w.modelSteps).toEqual([agentId])
})

test('an Agent call of a hidden row\'s type records no effort', async ($, on) => {
  const w = world(on, { backed: { 'p2:follow': { command: 'follow', promptMode: 'run-id' } } })
  await $.session.start(START)

  await callAgent($, w, 'p2:follow', { effort: 'xhigh', prompt: 'msgate run run-1' })

  expect([...w.agentEfforts]).toEqual([])
})

const NOT_ROWS: [string, Record<string, unknown>, Record<string, unknown>][] = [
  ['a fork call', { subagent_type: 'fork' }, {}],
  ['a forked spawn', {}, { fork: true }],
  ['a teammate\'s spawn', {}, { isTeammate: true }],
  ['a resumed run', {}, { resume: { agentId: 'ag-old' } }],
  ['a workflow\'s agent', {}, { workflow: { runId: 'wf-1', agentIndex: 0 } }],
]

for (const [name, call, spawn] of NOT_ROWS) {
  test(`${name} carrying an effort does not change the effort of its row`, async ($, on) => {
    const w = world(on)
    w.respond = codex('done')
    await $.session.start(START)
    const agentId = await callAgent($, w, AGENTS + ':second-opinion', { effort: 'xhigh', ...call }, spawn)

    await step($, agentId)

    expect([...w.agentEfforts]).toEqual([])
    expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), ...READ_ONLY_HIGH, '-'])
  })
}

test('two Agent calls with different efforts give each row its own', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const first = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'low' })
  const second = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'max' })
  const [one, two] = [await first.spawn(), await second.spawn()]
  await first.end()
  await second.end()
  w.messages[one.agentId] = [{ role: 'user', text: 'q' }]
  w.messages[two.agentId] = [{ role: 'user', text: 'q' }]

  await step($, two.agentId)
  await step($, one.agentId)

  const efforts = w.runs.filter((r) => r.argv[1] === 'exec').map((r) => r.argv[r.argv.indexOf('--effort') + 1])
  expect(efforts).toEqual(['max', 'low'])
})

test('a spawn that starts no agent records no effort and leaves the call\'s effort unused', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  w.spawnAnswer = () => ({ deny: 'no agent today' })
  const call = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })

  const denied = await call.spawn()
  w.spawnAnswer = null
  const later = await call.spawn()
  await call.end()
  w.messages[later.agentId] = [{ role: 'user', text: 'q' }]
  await step($, later.agentId)

  expect(denied).toEqual({ deny: 'no agent today' })
  expect(w.debugLogs.filter((text) => text.includes('effort'))).toEqual([expect.stringContaining('xhigh')])
  expect([...w.agentEfforts]).toEqual([])
  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), ...READ_ONLY_HIGH, '-'])
})

test('the effort of an Agent call is kept in the state, so a first turn after a reload still admits with it', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', AGENTS + ':second-opinion', [{ role: 'user', text: 'q' }])
  // The load that saw the call is gone: only the state remembers its effort.
  w.agentEfforts.set('ag-1', 'xhigh')
  await $.session.start(START)

  await step($, 'ag-1')

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), '--model', 'gpt-6.1-sol', ...FLAGGED('xhigh'), '--sandbox', 'read-only', '-'])
})

test('a first turn consumes the effort, so a later turn of the row carries none', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const agentId = await callAgent($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })
  expect(w.agentEfforts.get(agentId)).toBe('xhigh')

  await step($, agentId)
  w.messages[agentId] = [...w.messages[agentId]!, { role: 'assistant', text: 'done' }, { role: 'user', text: 'and then?' }]
  await step($, agentId, { turnId: 'turn-2' })

  expect(w.agentEfforts.get(agentId)).toBeNull()
  const sends = w.runs.filter((r) => r.argv[1] === 'send')
  expect(sends.map((r) => r.argv.slice(1))).toEqual([['send', 'conv-1', ...JOB_FLAGS, '-']])
})

// A spawn that has started its agent and is held before it answers, as the
// engine's is until the agent is up. `release` lets it answer.
function slowSpawn(w: ReturnType<typeof world>, agentId: string) {
  let release = () => {}
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  w.spawnAnswer = async () => {
    w.agents.push({ id: agentId, type: AGENTS + ':second-opinion' })
    w.messages[agentId] = [{ role: 'user', text: 'q' }]
    await held
    return { model: 'claude-haiku-4-5', agentId }
  }
  return release
}

// The runs the mod admitted for first turns.
const admissions = (w: ReturnType<typeof world>) => w.runs.filter((r) => r.argv[1] === 'exec')

// Lets the work in flight run until `done` holds.
async function until(done: () => boolean) {
  for (let i = 0; i < 100 && !done(); i += 1) await new Promise((resolve) => setTimeout(resolve, 1))
}

test('a first turn that starts while its spawn is still in flight waits for the spawn and admits with its effort', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const release = slowSpawn(w, 'ag-slow')
  const call = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })
  const spawning = call.spawn()
  await until(() => w.agents.length > 0)

  const turn = step($, 'ag-slow')
  await until(() => w.effortReads.length > 0)
  expect(w.runs).toEqual([])
  release()
  await spawning
  await call.end()
  await turn

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), '--model', 'gpt-6.1-sol', ...FLAGGED('xhigh'), '--sandbox', 'read-only', '-'])
})

test('a first turn whose state read returns after the spawn answered still admits with the spawn\'s effort', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const release = slowSpawn(w, 'ag-slow')
  let answerRead = () => {}
  w.effortReadHold = new Promise<void>((resolve) => {
    answerRead = resolve
  })
  const call = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })
  const spawning = call.spawn()
  await until(() => w.agents.length > 0)

  const turn = step($, 'ag-slow')
  await until(() => w.effortReads.length > 0)
  release()
  await spawning
  await call.end()
  answerRead()
  await turn

  expect(admissions(w)[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), '--model', 'gpt-6.1-sol', ...FLAGGED('xhigh'), '--sandbox', 'read-only', '-'])
})

test('a first turn gives up waiting for a spawn after the bound and admits with the type\'s own effort', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  slowSpawn(w, 'ag-slow')
  const call = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })
  void call.spawn()
  await until(() => w.agents.length > 0)

  const turn = step($, 'ag-slow')
  await until(() => w.effortReads.length > 0)
  await w.clock.advance(SPAWN_WAIT_MS - 1)
  expect(admissions(w)).toEqual([])
  await w.clock.advance(1)
  await turn

  expect(admissions(w)[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), ...READ_ONLY_HIGH, '-'])
})

test('a first turn that gives up waiting for a spawn says so in the debug log', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  slowSpawn(w, 'ag-slow')
  const call = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })
  void call.spawn()
  await until(() => w.agents.length > 0)

  const turn = step($, 'ag-slow')
  await until(() => w.effortReads.length > 0)
  await w.clock.advance(SPAWN_WAIT_MS)
  await turn

  expect(w.debugLogs.filter((line) => line.includes('ag-slow') && line.includes(SPAWN_WAIT_MS + ' ms'))).toHaveLength(1)
})

test('a first turn whose spawn answers in time logs no wait', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  await $.session.start(START)
  const release = slowSpawn(w, 'ag-slow')
  const call = await agentCall($, w, AGENTS + ':second-opinion', { effort: 'xhigh' })
  const spawning = call.spawn()
  await until(() => w.agents.length > 0)

  const turn = step($, 'ag-slow')
  await until(() => w.effortReads.length > 0)
  release()
  await spawning
  await call.end()
  await turn

  expect(w.debugLogs.filter((line) => line.includes(SPAWN_WAIT_MS + ' ms'))).toEqual([])
})

test('a type backed again is run with its new data from the next row on', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', AGENTS + ':second-opinion', [{ role: 'user', text: 'q' }])
  await $.session.start(START)
  await msgateAs(w, AGENTS).backType({ type: 'second-opinion', command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'low' })

  await step($, 'ag-1')

  expect(w.runs[0]!.argv.slice(1)).toEqual(['exec', ...ADMISSION('second-opinion'), '--model', 'gpt-6.1-sol', '--effort', 'low', '-'])
})

test('a type backed after the session started is answered by its next row', async ($, on) => {
  const w = world(on, { backed: {} })
  w.respond = codex('done')
  subagent(w, 'ag-1', 'p2:late', [{ role: 'user', text: 'q' }])
  await $.session.start(START)
  await msgateAs(w, 'p2').backType({ type: 'late', command: 'exec', promptMode: 'free' })

  const out = await step($, 'ag-1')

  expect(delivered(out)).toContain('done')
  expect(w.modelSteps).toEqual([])
})

test('tool calls inside a row of a backed type are refused, and inside an unbacked type\'s agent are not', async ($, on) => {
  const w = world(on, { backed: { 'p2:custom': { command: 'exec', promptMode: 'free' } } })
  subagent(w, 'ag-1', 'p2:custom', [{ role: 'user', text: 'q' }])
  subagent(w, 'ag-2', AGENTS + ':adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  const row: any = await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'ag-1' } as any)
  const unbacked: any = await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'ag-2' } as any)

  expect(row.deny).toContain('msgate agents run no tools')
  expect(unbacked.result).toBe('unanswered')
})

const STEP_LIMIT_LOG = (turnId: string) => 'agent ag-1 reached the step limit of 8 in turn ' + turnId + ': the model beneath the plugin answers the rest of the turn'

test('steps 1 to 8 of a turn are answered by the engine and the ninth goes to the model beneath, with one log line', async ($, on) => {
  const w = world(on)
  w.respond = codex('Codex says so.')
  subagent(w, 'ag-1', AGENTS + ':adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  for (let index = 0; index < 8; index++) await step($, 'ag-1', { turnId: 'turn-1', index })
  expect(w.modelSteps).toEqual([])
  expect(w.debugLogs.filter((line) => line.includes('step limit'))).toEqual([])

  const ninth = await step($, 'ag-1', { turnId: 'turn-1', index: 8 })
  const tenth = await step($, 'ag-1', { turnId: 'turn-1', index: 9 })

  expect(w.modelSteps).toEqual(['ag-1', 'ag-1'])
  expect(ninth.text).toBe('a Claude model answered')
  expect(tenth.text).toBe('a Claude model answered')
  expect(w.debugLogs.filter((line) => line.includes('step limit'))).toEqual([STEP_LIMIT_LOG('turn-1')])
})

test('a new turn of the row counts its steps from one again', async ($, on) => {
  const w = world(on)
  w.respond = codex('First answer.')
  subagent(w, 'ag-1', AGENTS + ':adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)
  for (let index = 0; index < 9; index++) await step($, 'ag-1', { turnId: 'turn-1', index })
  expect(w.modelSteps).toEqual(['ag-1'])

  w.messages['ag-1'] = [
    { role: 'user', text: 'q' },
    { role: 'assistant', text: 'First answer.' },
    { role: 'user', text: 'and now?' },
  ]
  w.respond = codex('Second answer.')
  const second = await step($, 'ag-1', { turnId: 'turn-2', index: 0 })

  expect(delivered(second)).toContain('Second answer.')
  expect(w.modelSteps).toEqual(['ag-1'])
})

test('the steps of a turn are counted for each row on its own', async ($, on) => {
  const w = world(on)
  w.respond = codex('done')
  subagent(w, 'ag-1', AGENTS + ':adhoc', [{ role: 'user', text: 'q' }])
  subagent(w, 'ag-2', AGENTS + ':adhoc', [{ role: 'user', text: 'q' }])
  await $.session.start(START)

  for (let index = 0; index < 8; index++) await step($, 'ag-1', { turnId: 'turn-1', index })
  await step($, 'ag-2', { turnId: 'turn-1', index: 0 })

  expect(w.modelSteps).toEqual([])
})
