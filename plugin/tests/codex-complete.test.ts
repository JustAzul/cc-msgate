import { expect, test } from 'claude-code/testing'
import { codexComplete, spawnRun } from '../hooks/codex-complete.js'
import { isBilled } from '../hooks/summary.js'
import { NO_USAGE } from './kit'

const BIN = '/plugin/bin/msgate'
const REQUEST = {
  model: 'gpt-6-luna',
  effort: 'low',
  maxTokens: 16,
  timeoutMs: 30000,
  system: 'Answer with one word.',
  prompt: [{ text: 'Stage one: ', cache: true }, { text: 'answer ok.' }],
}
const USAGE = { input_tokens: 3349, cached_input_tokens: 2432, output_tokens: 10, reasoning_output_tokens: 4 }
const CALL = () => ({
  sessionId: 'sess-1',
  source: 'mod:followups',
  callId: 'followups:chk-1:e1:v0plus',
  attrs: { check_id: 'chk-1' },
  signal: new AbortController().signal,
})

type Reply = { exitCode: number; stdout: string; stderr: string }
const exited = (exitCode: number, body: unknown): Reply => ({ exitCode, stdout: JSON.stringify(body), stderr: '' })
const answerBody = (over: Record<string, unknown> = {}) => ({ outcome: 'answered', text: 'ok', model: 'gpt-6-luna', usage: USAGE, ...over })
const failureBody = (failure: string) => ({ outcome: 'api-error', text: '', model: 'gpt-6-luna', usage: USAGE, failure })

// ports answers what the engine would and keeps what the module asked of it.
function ports(reply: Reply | ((argv: string[], init: any) => Promise<Reply>) = exited(0, answerBody())) {
  const seen = { runs: [] as { argv: string[]; init: any }[] }
  return {
    seen,
    bin: BIN,
    run: async (argv: string[], init: any) => {
      seen.runs.push({ argv, init })
      return typeof reply === 'function' ? reply(argv, init) : reply
    },
  }
}

const ask = (p: ReturnType<typeof ports>, request: unknown = REQUEST, call: ReturnType<typeof CALL> = CALL()) => codexComplete(p, request, call)

test('a codex call runs msgate complete with the model, effort, session, source, call id, attrs and timeout it was given', async () => {
  const p = ports()

  await ask(p)

  expect(p.seen.runs).toHaveLength(1)
  expect(p.seen.runs[0]!.argv).toEqual([
    BIN, 'complete', '--provider', 'codex', '--json',
    '--model', 'gpt-6-luna', '--effort', 'low',
    '--session-id', 'sess-1', '--source', 'mod:followups', '--call-id', 'followups:chk-1:e1:v0plus',
    '--attr', 'check_id=chk-1', '--timeout', '30',
  ])
})

test('a run id given to a codex call reaches msgate complete as --run-id, before the source', async () => {
  const p = ports()

  await ask(p, REQUEST, { ...CALL(), source: 'mod-summary', runId: 'run-7', callId: undefined, attrs: {} })

  expect(p.seen.runs[0]!.argv).toEqual([
    BIN, 'complete', '--provider', 'codex', '--json',
    '--model', 'gpt-6-luna', '--effort', 'low',
    '--session-id', 'sess-1', '--run-id', 'run-7', '--source', 'mod-summary',
    '--timeout', '30',
  ])
})

test('the system text and the prompt, its blocks joined, reach msgate complete on standard input as one user message', async () => {
  const p = ports()

  await ask(p)

  expect(JSON.parse(p.seen.runs[0]!.init.stdin)).toEqual({
    system: 'Answer with one word.',
    messages: [{ role: 'user', text: 'Stage one: answer ok.' }],
  })
})

test('what the call does not give is left out of the argv, bar the default timeout, and the system out of the standard input', async () => {
  const p = ports()

  await ask(p, { model: '', prompt: 'hi' }, { ...CALL(), attrs: {} })

  expect(p.seen.runs[0]!.argv).toEqual([BIN, 'complete', '--provider', 'codex', '--json', '--session-id', 'sess-1', '--source', 'mod:followups', '--call-id', 'followups:chk-1:e1:v0plus', '--timeout', '600'])
  expect(JSON.parse(p.seen.runs[0]!.init.stdin)).toEqual({ messages: [{ role: 'user', text: 'hi' }] })
})

test('a timeout is given to msgate in whole seconds, rounded up, and a call with none gets 600 so a hung call ends', async () => {
  const p = ports()

  await ask(p, { ...REQUEST, timeoutMs: 1500 })
  await ask(p, { model: 'm', prompt: 'hi' })

  expect(p.seen.runs[0]!.argv.slice(-2)).toEqual(['--timeout', '2'])
  expect(p.seen.runs[1]!.argv.slice(-2)).toEqual(['--timeout', '600'])
})

test('the port is given the standard input and the signal, and no timeout of its own', async () => {
  const p = ports()
  const call = CALL()

  await ask(p, REQUEST, call)

  expect(Object.keys(p.seen.runs[0]!.init).sort()).toEqual(['signal', 'stdin'])
  expect(p.seen.runs[0]!.init.signal).toBe(call.signal)
})

test('an answer from Codex is an answered completion', async () => {
  const got = await ask(ports())

  expect(got).toEqual({ isAnswered: true, text: 'ok', usage: { input_tokens: 917, output_tokens: 10, cache_read_input_tokens: 2432, cache_creation_input_tokens: 0 } })
})

test('an exit 0 with no text is an empty reply that still carries its usage', async () => {
  const got = await ask(ports(exited(0, answerBody({ outcome: 'empty-reply', text: '' }))))

  expect(got).toEqual({ isAnswered: false, reason: 'empty-reply', usage: { input_tokens: 917, output_tokens: 10, cache_read_input_tokens: 2432, cache_creation_input_tokens: 0 } })
})

test('a failure Codex classes as a rate limit is an api-error with status 429', async () => {
  const got = await ask(ports(exited(1, failureBody('rate-limit'))))

  expect(got).toMatchObject({ isAnswered: false, reason: 'api-error', status: 429, error: 'rate_limit' })
})

test('any other failure is an api-error with no status', async () => {
  for (const [failure, error] of [['auth', 'authentication_failed'], ['quota', 'billing_error'], ['other', 'unknown']] as const) {
    const got = await ask(ports(exited(1, failureBody(failure))))

    expect(got).toMatchObject({ isAnswered: false, reason: 'api-error', status: null, error })
  }
})

test('an api-error carries the usage the call reported', async () => {
  const got = await ask(ports(exited(1, failureBody('other')))) as any

  expect(got.usage).toEqual({ input_tokens: 917, output_tokens: 10, cache_read_input_tokens: 2432, cache_creation_input_tokens: 0 })
})

test('a timeout error object from msgate is the aborted arm a timed-out completion gives', async () => {
  const got = await ask(ports(exited(124, { sdk_status: 'timeout', exit_code: 124, error: 'timed out after 30s' })))

  expect(got).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
})

test('a cancelled error object from msgate is the aborted arm', async () => {
  const got = await ask(ports(exited(130, { sdk_status: 'cancelled', exit_code: 130, error: 'cancelled' })))

  expect(got).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
})

test('a provider_missing error object from msgate is an api-error with no status', async () => {
  const got = await ask(ports(exited(127, { sdk_status: 'provider_missing', exit_code: 127, error: 'codex is not installed' })))

  expect(got).toEqual({ isAnswered: false, reason: 'api-error', status: null, error: 'unknown', usage: NO_USAGE })
})

test('output that is not a completion is an api-error with no status, whatever the exit code', async () => {
  for (const reply of [{ exitCode: 2, stdout: '', stderr: 'usage: msgate complete' }, { exitCode: 0, stdout: 'not json', stderr: '' }, exited(0, { outcome: 'surprise' })]) {
    const got = await ask(ports(reply))

    expect(got).toEqual({ isAnswered: false, reason: 'api-error', status: null, error: 'unknown', usage: NO_USAGE })
  }
})

test('usage reaches the service in its own keys: uncached input, cache reads, and output with its reasoning', async () => {
  const got = (await ask(ports())) as any

  expect(got.usage).toEqual({ input_tokens: 3349 - 2432, cache_read_input_tokens: 2432, output_tokens: 10, cache_creation_input_tokens: 0 })
})

test('uncached input never reads below zero, and a count that is not a number reads as zero', async () => {
  const odd = answerBody({ usage: { input_tokens: 100, cached_input_tokens: 400, output_tokens: 'x' } })

  const got = (await ask(ports(exited(0, odd)))) as any

  expect(got.usage).toEqual({ input_tokens: 0, cache_read_input_tokens: 400, output_tokens: 0, cache_creation_input_tokens: 0 })
})

test('an answer with usage counts as billed', async () => {
  expect(isBilled(await ask(ports()))).toBe(true)
})

test('a signal already aborted answers aborted and runs nothing', async () => {
  const stop = new AbortController()
  stop.abort()
  const p = ports()

  const got = await ask(p, REQUEST, { ...CALL(), signal: stop.signal })

  expect(got).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
  expect(p.seen.runs).toHaveLength(0)
})

test('the signal of the caller is handed to the process, which is killed when it aborts, and the call answers aborted', async () => {
  const stop = new AbortController()
  let isKilled = false
  const p = ports(
    (_argv, init) =>
      new Promise<Reply>((resolve) => {
        init.signal.addEventListener('abort', () => {
          isKilled = true
          resolve(exited(130, { sdk_status: 'cancelled', exit_code: 130, error: 'cancelled' }))
        })
      }),
  )

  const pending = ask(p, REQUEST, { ...CALL(), signal: stop.signal })
  await Promise.resolve()
  expect(p.seen.runs[0]!.init.signal).toBe(stop.signal)
  stop.abort()

  expect(await pending).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
  expect(isKilled).toBe(true)
})

test('an abort that lands as the process exits with an answer still answers aborted', async () => {
  const stop = new AbortController()
  const p = ports(async () => {
    stop.abort()
    return exited(0, answerBody())
  })

  expect(await ask(p, REQUEST, { ...CALL(), signal: stop.signal })).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
})

test('a process that cannot be run rejects the call, as the engine refusing to send does', async () => {
  const injected = new Error('spawn failed')
  const p = ports(async () => {
    throw injected
  })

  await expect(ask(p)).rejects.toBe(injected)
})

test('a process killed by the abort that rejects answers aborted, not a refusal', async () => {
  const stop = new AbortController()
  const p = ports(async () => {
    stop.abort()
    throw new Error('killed')
  })

  expect(await ask(p, REQUEST, { ...CALL(), signal: stop.signal })).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
})

// A stream as $.process.spawn gives one: its pieces, then either the end with
// the child's { code, signal } or a child that never ends, which only `return`
// stops (it settles the pull waiting on the child, as the engine's stream does).
type Piece = { stream: 'stdout' | 'stderr'; text: string }
function spawned(pieces: Piece[], end: { code: number | null; signal: string | null } | 'hangs') {
  const state = { returned: 0, requests: [] as any[], waiting: null as null | ((step: unknown) => void) }
  let at = 0
  const stream = {
    next: () => {
      if (at < pieces.length) return Promise.resolve({ done: false, value: pieces[at++] })
      if (end !== 'hangs') return Promise.resolve({ done: true, value: end })
      return new Promise((resolve) => {
        state.waiting = resolve
      })
    },
    return: async () => {
      state.returned += 1
      state.waiting?.({ done: true, value: undefined })
      return { done: true, value: undefined }
    },
    [Symbol.asyncIterator]() {
      return this
    },
  }
  const spawn = (request: unknown) => {
    state.requests.push(request)
    return stream
  }
  return { spawn, state }
}

test('a streamed child is run by its argv with the standard input, and its pieces and exit code come back as stdout, stderr and exitCode', async () => {
  const child = spawned([{ stream: 'stdout', text: '{"out' }, { stream: 'stderr', text: 'warn' }, { stream: 'stdout', text: 'put":1}' }], { code: 3, signal: null })

  const got = await spawnRun(child.spawn)(['msgate', 'complete'], { stdin: '{"x":1}', signal: new AbortController().signal })

  expect(got).toEqual({ exitCode: 3, stdout: '{"output":1}', stderr: 'warn' })
  expect(child.state.requests).toEqual([{ argv: ['msgate', 'complete'], input: '{"x":1}' }])
})

test('a child ended by a signal reads as exit code 1', async () => {
  const child = spawned([], { code: null, signal: 'SIGTERM' })

  const got = await spawnRun(child.spawn)(['msgate'], { stdin: '', signal: new AbortController().signal })

  expect(got.exitCode).toBe(1)
})

test('an abort ends the loop of the stream, and the run settles without waiting for the child', async () => {
  const child = spawned([{ stream: 'stdout', text: 'partial' }], 'hangs')
  const stop = new AbortController()

  const pending = spawnRun(child.spawn)(['msgate'], { stdin: '', signal: stop.signal })
  await Promise.resolve()
  expect(child.state.returned).toBe(0)
  stop.abort()
  await pending

  expect(child.state.returned).toBe(1)
})

test('a signal already aborted ends the stream as soon as it is started', async () => {
  const child = spawned([], 'hangs')
  const stop = new AbortController()
  stop.abort()

  await spawnRun(child.spawn)(['msgate'], { stdin: '', signal: stop.signal })

  expect(child.state.returned).toBe(1)
})

test('a stream that has ended is not returned by an abort that comes after it', async () => {
  const child = spawned([], { code: 0, signal: null })
  const stop = new AbortController()

  await spawnRun(child.spawn)(['msgate'], { stdin: '', signal: stop.signal })
  stop.abort()

  expect(child.state.returned).toBe(0)
})

test('a child that cannot be started rejects the run with its own error', async () => {
  const injected = new Error('no such file')
  const spawn = () => ({
    next: () => Promise.reject(injected),
    return: async () => ({ done: true, value: undefined }),
  })

  await expect(spawnRun(spawn as any)(['msgate'], { stdin: '', signal: new AbortController().signal })).rejects.toBe(injected)
})

test('an aborted codex call through the spawn adapter ends the stream and answers aborted', async () => {
  const child = spawned([], 'hangs')
  const stop = new AbortController()

  const pending = codexComplete({ bin: BIN, run: spawnRun(child.spawn) }, REQUEST, { ...CALL(), signal: stop.signal })
  await Promise.resolve()
  stop.abort()

  expect(await pending).toEqual({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })
  expect(child.state.returned).toBe(1)
  expect(child.state.requests[0].argv.slice(0, 5)).toEqual([BIN, 'complete', '--provider', 'codex', '--json'])
})
