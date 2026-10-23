import { expect, test } from 'claude-code/testing'
import { NOUN, createService } from '../hooks/service.js'
import { answered } from './kit'
import { isBilled } from '../hooks/summary.js'

// The plugin test harness gives a test only core nouns on `$`, so a plugin's
// own noun cannot be called from here: the service is driven through its ports,
// the way register.js builds them from `$`.
const REQUEST = {
  model: 'haiku',
  maxTokens: 16,
  effort: 'low',
  timeoutMs: 30000,
  system: 'Answer with one word.',
  prompt: [{ text: 'a long shared prefix', cache: true }, { text: 'Stage one: answer ok.' }],
}
const META = { contract: 1, callId: 'followups:chk-1:e1:v0plus', provider: 'anthropic' }
const CTX = () => ({ signal: new AbortController().signal, caller: 'followups' })

function deferred<T>() {
  let release!: (value: T) => void
  const promise = new Promise<T>((resolve) => {
    release = resolve
  })
  return { promise, release }
}

// ports answers what the engine would, and keeps what the service asked of it.
function ports(over: Record<string, unknown> = {}) {
  const seen = { completions: [] as { request: unknown; signal: AbortSignal }[], recordings: [] as any[] }
  return {
    seen,
    sessionId: async () => 'sess-1',
    now: async () => Date.UTC(2026, 9, 12, 17, 25, 54),
    haikuPin: async () => undefined as string | undefined,
    complete: async (request: unknown, signal: AbortSignal) => {
      seen.completions.push({ request, signal })
      return answered('ok')
    },
    record: async (recording: unknown) => {
      seen.recordings.push(recording)
    },
    ...over,
  }
}

const call = (service: ReturnType<typeof createService>, p: ReturnType<typeof ports>, meta: unknown = META, ctx = CTX(), request: unknown = REQUEST) =>
  service.serve(p, { request, meta }, ctx)

test('the service answers with the model result unchanged', async () => {
  const p = ports()
  const result = answered('ok')
  p.complete = async () => result

  const got = await call(createService(), p)

  expect(got).toEqual({ value: result })
})

test('the request reaches the model unchanged, with the signal of the dispatch', async () => {
  const p = ports()
  const ctx = CTX()

  await call(createService(), p, META, ctx)

  expect(p.seen.completions).toHaveLength(1)
  expect(p.seen.completions[0]!.request).toBe(REQUEST)
  expect(p.seen.completions[0]!.signal).toBe(ctx.signal)
})

test('an unknown contract version, call id, provider or request is denied before the model is called', async () => {
  const cases: [string, unknown, unknown][] = [
    ['contract', { ...META, contract: 2 }, REQUEST],
    ['contract', null, REQUEST],
    ['call-id', { ...META, callId: '' }, REQUEST],
    ['call-id', { ...META, callId: 'has a space' }, REQUEST],
    ['call-id', { ...META, callId: 'x'.repeat(201) }, REQUEST],
    ['call-id', { ...META, callId: 7 }, REQUEST],
    ['unsupported-provider', { ...META, provider: 'gemini' }, REQUEST],
    ['unsupported-provider', { ...META, provider: null }, REQUEST],
    ['request', META, 'not a request'],
  ]
  for (const [why, meta, request] of cases) {
    const p = ports()
    const got = await call(createService(), p, meta, CTX(), request)
    expect(got).toEqual({ deny: 'msgate:pre-model:' + why })
    expect(p.seen.completions).toHaveLength(0)
  }
})

test('a call without a provider is served as anthropic, and fields it does not know are ignored', async () => {
  const p = ports()
  const { provider: _, ...meta } = META

  const got = await call(createService(), p, { ...meta, futureField: { x: 1 } })

  expect(got).toHaveProperty('value')
  expect(p.seen.completions).toHaveLength(1)
})

test('a call id already in flight is denied as a duplicate, and is served again once it has answered', async () => {
  const service = createService()
  const held = deferred<unknown>()
  const reached = deferred<void>()
  const slow = ports({
    complete: () => {
      reached.release()
      return held.promise
    },
  })
  const first = call(service, slow)
  await reached.promise

  const second = ports()
  expect(await call(service, second)).toEqual({ deny: 'msgate:pre-model:duplicate' })
  expect(second.seen.completions).toHaveLength(0)

  held.release(answered('ok'))
  await first
  expect(await call(service, ports())).toHaveProperty('value')
})

const BILLED = { input_tokens: 458, output_tokens: 18, cache_read_input_tokens: 9211, cache_creation_input_tokens: 0 }
const AT = Date.UTC(2026, 9, 12, 17, 25, 54)

test('a billed call is recorded for the session and the clock of its start, as the plugin that asked', async () => {
  const p = ports()
  p.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })
  const meta = { ...META, attrs: { check_id: 'chk-1', stage: 'v0plus', round: '0' } }

  await call(createService(), p, meta)

  expect(p.seen.recordings).toEqual([
    {
      usage: BILLED,
      sessionId: 'sess-1',
      model: 'claude-haiku-5-5',
      source: 'mod:followups',
      callId: META.callId,
      attrs: { check_id: 'chk-1', stage: 'v0plus', round: '0', model_inferred: 'table', model_requested: 'haiku' },
      at: AT,
    },
  ])
})

test('a call that used no tokens is not recorded, whichever way it ended', async () => {
  const p = ports()
  p.complete = async () => ({ isAnswered: false, reason: 'aborted', usage: { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } })

  await call(createService(), p)

  expect(p.seen.recordings).toHaveLength(0)
})

test('a call that failed after using tokens is recorded', async () => {
  const p = ports()
  p.complete = async () => ({ isAnswered: false, reason: 'empty-reply', usage: BILLED })

  await call(createService(), p)

  expect(p.seen.recordings).toHaveLength(1)
})

test('the session is read once, before the model is called', async () => {
  const p = ports()
  const order: string[] = []
  p.sessionId = async () => {
    order.push('session')
    return 'sess-1'
  }
  p.complete = async () => {
    order.push('model')
    return { isAnswered: true, text: 'ok', usage: BILLED }
  }

  await call(createService(), p)

  expect(order).toEqual(['session', 'model'])
})

test('the model id the environment pins for haiku is the one recorded, marked as read from it', async () => {
  const p = ports()
  p.haikuPin = async () => 'claude-haiku-9-9'
  p.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })

  await call(createService(), p)

  expect(p.seen.recordings[0].model).toBe('claude-haiku-9-9')
  expect(p.seen.recordings[0].attrs).toEqual({ model_inferred: 'env', model_requested: 'haiku' })
})

test('a request that names a full model id is recorded under it, not marked as inferred', async () => {
  const p = ports()
  p.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })

  await call(createService(), p, META, CTX(), { ...REQUEST, model: 'claude-haiku-5-5' })

  expect(p.seen.recordings[0].model).toBe('claude-haiku-5-5')
  expect(p.seen.recordings[0].attrs).toEqual({})
})

test('an attribute the record cannot take is left out and does not stop the call', async () => {
  const p = ports()
  p.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })
  const attrs: Record<string, unknown> = { ok_key: 'fine', 'bad key': 'x', '9lives': 'x', checkId: 'x', long_value: 'y'.repeat(201), not_text: 7 }
  for (let i = 0; i < 20; i++) attrs['k_' + String.fromCharCode(97 + i)] = 'v'

  const got = await call(createService(), p, { ...META, attrs })

  expect(got).toHaveProperty('value')
  expect(p.seen.recordings).toHaveLength(1)
  const recorded = p.seen.recordings[0].attrs
  expect(recorded.ok_key).toBe('fine')
  for (const key of ['bad key', '9lives', 'checkId', 'long_value', 'not_text']) expect(recorded).not.toHaveProperty(key)
  expect(Object.keys(recorded).filter((k) => k !== 'model_inferred' && k !== 'model_requested')).toHaveLength(16)
})

test('the attrs the service sets about the model are its own, whatever the caller sent', async () => {
  const p = ports()
  p.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })

  await call(createService(), p, { ...META, attrs: { model_inferred: 'spoofed' } })

  expect(p.seen.recordings[0].attrs.model_inferred).toBe('table')
})

test('the answer does not wait for the recording, and a recording that fails changes nothing', async () => {
  const never = ports()
  never.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })
  never.record = () => new Promise(() => {})
  expect(await call(createService(), never)).toHaveProperty('value')

  const broken = ports()
  broken.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })
  broken.record = async () => {
    throw new Error('usage add failed')
  }
  expect(await call(createService(), broken)).toHaveProperty('value')

  const syncBroken = ports()
  syncBroken.complete = async () => ({ isAnswered: true, text: 'ok', usage: BILLED })
  syncBroken.record = () => {
    throw new Error('usage add threw')
  }
  expect(await call(createService(), syncBroken)).toHaveProperty('value')
})

test('a model call the engine refuses to send is denied as after-model, nothing is recorded, and the id is free again', async () => {
  const service = createService()
  const p = ports()
  p.complete = async () => {
    throw new Error('model not allowed')
  }

  expect(await call(service, p)).toEqual({ deny: 'msgate:after-model:refused' })

  expect(p.seen.recordings).toHaveLength(0)
  expect(await call(service, ports())).toHaveProperty('value')
})

test('a hook that fails before the model call is denied as pre-model, and the call it left behind never reaches the model', async () => {
  const service = createService()
  const held = deferred<number>()
  const slow = ports({ now: () => held.promise })
  const pending = call(service, slow)
  await Promise.resolve()

  // The hook ran out of budget while it waited: the handler answers for it.
  expect(service.failed({ meta: META }, 'timeout')).toEqual({ deny: 'msgate:pre-model:abandoned-timeout' })

  held.release(AT)
  expect(await pending).toEqual({ deny: 'msgate:pre-model:cancelled' })
  expect(slow.seen.completions).toHaveLength(0)
})

test('a hook that fails once the model call is made is denied as after-model', async () => {
  const service = createService()
  const held = deferred<unknown>()
  const reached = deferred<void>()
  const slow = ports({
    complete: () => {
      reached.release()
      return held.promise
    },
  })
  void call(service, slow)
  await reached.promise

  expect(service.failed({ meta: META }, 'throw')).toEqual({ deny: 'msgate:after-model:throw' })
  held.release(answered('ok'))
})

test('a dispatch abandoned before the model call is made is denied as pre-model and does not call the model', async () => {
  const p = ports()
  const abort = new AbortController()
  abort.abort()

  const got = await call(createService(), p, META, { signal: abort.signal, caller: 'followups' })

  expect(got).toEqual({ deny: 'msgate:pre-model:cancelled' })
  expect(p.seen.completions).toHaveLength(0)
})

test('a hook that was not run because of a re-entry is pre-model and leaves the call in flight alone', async () => {
  const service = createService()
  const held = deferred<unknown>()
  const reached = deferred<void>()
  const slow = ports({
    complete: () => {
      reached.release()
      return held.promise
    },
  })
  const first = call(service, slow)
  await reached.promise

  expect(service.failed({ meta: META }, 're-entry')).toEqual({ deny: 'msgate:pre-model:re-entry' })

  held.release(answered('ok'))
  expect(await first).toHaveProperty('value')
})

test('a failure with no call of this id in flight proves nothing about the model', () => {
  expect(createService().failed({ meta: META }, 'throw')).toEqual({ deny: 'msgate:unknown-state:throw' })
  expect(createService().failed(undefined, 'timeout')).toEqual({ deny: 'msgate:unknown-state:timeout' })
})

test('every member of the interface is a function, and the body that stands in for the hook says nothing about the model', async () => {
  for (const [name, member] of Object.entries(NOUN)) expect([name, typeof member]).toEqual([name, 'function'])

  await expect(NOUN.complete()).rejects.toThrow('msgate:unserved')
})

// The codex path: the request is answered by `msgate complete`, which records
// the call itself, so the service records nothing and never asks Claude.
const CODEX_META = { ...META, provider: 'codex', attrs: { check_id: 'chk-1', stage: 'v0plus' } }
const CODEX_REQUEST = { model: 'gpt-6-luna', effort: 'low', system: 'Answer with one word.', prompt: [{ text: 'Stage one: ', cache: true }, { text: 'answer ok.' }], maxTokens: 16 }
const CODEX_BODY = { outcome: 'answered', text: 'ok', model: 'gpt-6-luna', usage: { input_tokens: 3349, cached_input_tokens: 2432, output_tokens: 10, reasoning_output_tokens: 4 } }

function codexPorts(over: Record<string, unknown> = {}) {
  const p = ports({
    complete: async () => {
      throw new Error('the Claude path was reached')
    },
    record: async () => {
      throw new Error('a second record was written')
    },
    haikuPin: async () => {
      throw new Error('the Claude model was priced')
    },
  })
  const runs: { argv: string[]; init: any }[] = []
  const codex = {
    bin: '/plugin/bin/msgate',
    run: async (argv: string[], init: any) => {
      runs.push({ argv, init })
      return { exitCode: 0, stdout: JSON.stringify(CODEX_BODY), stderr: '' }
    },
    ...over,
  }
  return { ...p, codex, runs }
}

const callCodex = (p: ReturnType<typeof codexPorts>, request: unknown = CODEX_REQUEST, ctx = CTX()) => call(createService(), p as any, CODEX_META, ctx, request)

test('a codex completion is answered by msgate complete, with the plugin that asked as the source and the attrs the record takes', async () => {
  const p = codexPorts()

  const got = await callCodex(p)

  expect(got).toEqual({ value: { isAnswered: true, text: 'ok', usage: { input_tokens: 917, output_tokens: 10, cache_read_input_tokens: 2432, cache_creation_input_tokens: 0 } } })
  expect(p.runs).toHaveLength(1)
  expect(p.runs[0]!.argv).toEqual([
    '/plugin/bin/msgate', 'complete', '--provider', 'codex', '--json',
    '--model', 'gpt-6-luna', '--effort', 'low',
    '--session-id', 'sess-1', '--source', 'mod:followups', '--call-id', META.callId,
    '--attr', 'check_id=chk-1', '--attr', 'stage=v0plus', '--timeout', '600',
  ])
})

test('a codex completion that used tokens writes no second record and never reaches the Claude path', async () => {
  const p = codexPorts()
  const claude: string[] = []
  p.complete = async () => void claude.push('complete') as never
  p.record = async () => void claude.push('record') as never
  p.haikuPin = async () => void claude.push('haikuPin') as never

  const got = (await callCodex(p)) as any

  expect(isBilled(got.value)).toBe(true)
  expect(claude).toEqual([])
})

test('a codex request with tools is denied as pre-model request, and no process is run', async () => {
  const p = codexPorts()

  const got = await callCodex(p, { ...CODEX_REQUEST, tools: [{ name: 'search' }] })

  expect(got).toEqual({ deny: 'msgate:pre-model:request' })
  expect(p.runs).toHaveLength(0)
})

test('a codex request with an image is denied as pre-model request, and no process is run', async () => {
  const p = codexPorts()

  const got = await callCodex(p, { ...CODEX_REQUEST, prompt: [{ text: 'look' }, { type: 'image', source: { type: 'base64', data: 'AAAA' } }] })

  expect(got).toEqual({ deny: 'msgate:pre-model:request' })
  expect(p.runs).toHaveLength(0)
})

test('a codex request with any other non-text block is denied as pre-model request, and no process is run', async () => {
  const p = codexPorts()

  const got = await callCodex(p, { ...CODEX_REQUEST, system: [{ type: 'document', text: 'x' }] })

  expect(got).toEqual({ deny: 'msgate:pre-model:request' })
  expect(p.runs).toHaveLength(0)
})

test('a refused codex request leaves its call id free', async () => {
  const service = createService()
  const p = codexPorts()

  expect(await call(service, p as any, CODEX_META, CTX(), { ...CODEX_REQUEST, tools: [] })).toEqual({ deny: 'msgate:pre-model:request' })

  expect(await call(service, p as any, CODEX_META, CTX(), CODEX_REQUEST)).toHaveProperty('value')
})

test('an aborted codex completion kills the process and answers aborted', async () => {
  const stop = new AbortController()
  let isKilled = false
  const reached = deferred<void>()
  const p = codexPorts({
    run: (_argv: string[], init: any) =>
      new Promise((resolve) => {
        reached.release()
        init.signal.addEventListener('abort', () => {
          isKilled = true
          resolve({ exitCode: 130, stdout: JSON.stringify({ sdk_status: 'cancelled', exit_code: 130, error: 'cancelled' }), stderr: '' })
        })
      }),
  })

  const pending = callCodex(p, CODEX_REQUEST, { signal: stop.signal, caller: 'followups' })
  await reached.promise
  stop.abort()

  expect(await pending).toEqual({ value: { isAnswered: false, reason: 'aborted', usage: { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } } })
  expect(isKilled).toBe(true)
})

test('a codex process that cannot be run is denied as after-model, and the call id is free again', async () => {
  const service = createService()
  const p = codexPorts({
    run: async () => {
      throw new Error('spawn failed')
    },
  })

  expect(await call(service, p as any, CODEX_META, CTX(), CODEX_REQUEST)).toEqual({ deny: 'msgate:after-model:refused' })

  expect(await call(service, codexPorts() as any, CODEX_META, CTX(), CODEX_REQUEST)).toHaveProperty('value')
})

test('an unknown provider is still denied, and a call without one is still served as anthropic with no codex port', async () => {
  const p = ports()
  const { provider: _, ...meta } = META

  expect(await call(createService(), p, { ...meta, provider: 'openai' })).toEqual({ deny: 'msgate:pre-model:unsupported-provider' })
  expect(await call(createService(), p, meta)).toHaveProperty('value')
})
