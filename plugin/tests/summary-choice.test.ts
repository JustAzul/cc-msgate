import { expect, test } from 'claude-code/testing'
import { createRecorder, finishOnStop } from '../hooks/recorder.js'
import { createSummarizer } from '../hooks/summarizer.js'
import { candidatesOf, createSummaryChoice } from '../hooks/summary-choice.js'
import { NO_USAGE, answered, apiError, msg } from './kit'

const CLAUDE = { provider: 'anthropic', model: 'claude-haiku-5-5' }
const CODEX = { provider: 'codex', model: 'gpt-6-luna' }
const providers = (...entries: [string, boolean][]) => entries.map(([provider, available]) => ({ provider, available }))
const ranked = (...entries: { provider: string; model: string }[]) => entries.map((entry) => ({ ...entry, cost_usd: 0.0001, basis: 'measured', calls: 3 }))
const aborted = () => ({ isAnswered: false, reason: 'aborted', usage: NO_USAGE })

type Call = { request: any; call?: any; settle: (result: unknown) => void; signal?: AbortSignal }

// fakePorts is the engine beneath the summaries: the candidates the test sets,
// a Claude and a Codex completion that each wait until the test answers it (a
// Codex one also ends when its signal aborts), and a clock that only the test
// moves.
function fakePorts(candidates: { provider: string; model: string }[] = [CLAUDE]) {
  const state = { candidates }
  const claude: Call[] = []
  const codex: Call[] = []
  const logs: string[] = []
  const recorded: { usage: unknown; runId: string; model: string }[] = []
  const timers: { ms: number; callback: () => void; isCancelled: boolean }[] = []
  let now = 0
  const ports = {
    surfaces: async () => ['terminal'],
    now: async () => now,
    candidates: async () => state.candidates,
    complete: (request: unknown) => new Promise((resolve) => claude.push({ request, settle: resolve })),
    completeCodex: (request: unknown, call: { signal: AbortSignal }) =>
      new Promise((resolve) => {
        codex.push({ request, call, signal: call.signal, settle: resolve })
        call.signal.addEventListener('abort', () => resolve(aborted()), { once: true })
      }),
    record: async (usage: unknown, runId: string, model: string) => {
      recorded.push({ usage, runId, model })
    },
    log: (text: string) => void logs.push(text),
    labelSettled: () => {},
    random: () => 0,
    after: (ms: number, callback: () => void) => {
      const timer = { ms, callback, isCancelled: false }
      timers.push(timer)
      return { cancel: () => (timer.isCancelled = true) }
    },
  }
  return {
    ports,
    state,
    claude,
    codex,
    logs,
    recorded,
    timers,
    activeTimers: () => timers.filter((t) => !t.isCancelled).map((t) => t.ms),
    expire: async (ms: number) => {
      for (const timer of timers) if (timer.ms === ms && !timer.isCancelled) timer.callback()
      await flush()
    },
    // answer settles the oldest call of a provider that nobody answered.
    answer: async (calls: Call[], result: unknown) => {
      calls.shift()!.settle(result)
      await flush()
    },
  }
}

async function flush() {
  for (let i = 0; i < 50; i++) await Promise.resolve()
}

const entries = (count: number) => Array.from({ length: count }, (_, i) => msg('entry ' + (i + 1)))
const summarizerOf = () => createSummarizer({}, createSummaryChoice())

// The candidates.

test('the candidates are the ranked models in the order the ranking gives them, a Codex one included', () => {
  const listing = { providers: providers(['codex', true], ['anthropic', true]), summary_models: ranked(CODEX, CLAUDE) }

  expect(candidatesOf(listing)).toEqual([CODEX, CLAUDE])
})

test('a candidate whose provider is unavailable is skipped', () => {
  const listing = { providers: providers(['codex', false], ['anthropic', true]), summary_models: ranked(CODEX, CLAUDE) }

  expect(candidatesOf(listing)).toEqual([CLAUDE])
})

test('a ranked model of a provider other than anthropic and codex is no candidate', () => {
  const other = { provider: 'gemini', model: 'gemini-9' }

  expect(candidatesOf({ providers: [], summary_models: ranked(other, CODEX) })).toEqual([CODEX])
})

test('an empty ranking falls back to anthropic and claude-haiku-5-5', () => {
  expect(candidatesOf({ providers: providers(['codex', true]), summary_models: [] })).toEqual([CLAUDE])
})

test('a listing that could not be read falls back to anthropic and claude-haiku-5-5', () => {
  expect(candidatesOf(null)).toEqual([CLAUDE])
})

// The pick.

test('the cheapest candidate is asked, a Codex one included, at effort low', async () => {
  const w = fakePorts([CODEX, CLAUDE])

  await summarizerOf().observe(w.ports, 'run-1', [msg('e1')])

  expect(w.claude).toHaveLength(0)
  expect(w.codex).toHaveLength(1)
  expect(w.codex[0]!.request).toMatchObject({ model: 'gpt-6-luna', effort: 'low' })
})

test('a request to a Claude candidate carries that candidate\'s model', async () => {
  const w = fakePorts([{ provider: 'anthropic', model: 'claude-sonnet-5' }])

  await summarizerOf().observe(w.ports, 'run-1', [msg('e1')])

  expect(w.claude[0]!.request).toMatchObject({ model: 'claude-sonnet-5', effort: 'low' })
})

test('no candidate at all asks for nothing', async () => {
  const w = fakePorts([])

  await summarizerOf().observe(w.ports, 'run-1', [msg('e1')])

  expect(w.claude).toHaveLength(0)
  expect(w.codex).toHaveLength(0)
})

// The hand-off.

test('after a rate-limited refusal the same request goes at once to the next provider\'s candidate', async () => {
  const w = fakePorts([CLAUDE, CODEX])
  const summarizer = summarizerOf()

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  const asked = w.claude[0]!.request
  await w.answer(w.claude, apiError(429))

  expect(w.codex).toHaveLength(1)
  expect(w.codex[0]!.request).toEqual({ ...asked, model: 'gpt-6-luna' })
})

test('the pause of the refusing provider does not carry over to the one that takes the request', async () => {
  const w = fakePorts([CLAUDE, CODEX])
  const summarizer = summarizerOf()

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.claude, apiError(429))
  await w.answer(w.codex, answered('Reading auth.go'))
  await summarizer.observe(w.ports, 'run-1', entries(3))

  expect(w.claude).toHaveLength(0)
  expect(w.codex).toHaveLength(1)
})

test('only one hand-off happens per request', async () => {
  const w = fakePorts([CLAUDE, CODEX])
  const summarizer = summarizerOf()

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.claude, apiError(429))
  await w.answer(w.codex, apiError(429))

  expect(w.claude).toHaveLength(0)
  expect(w.codex).toHaveLength(0)
  expect(w.logs).toHaveLength(1)
  expect(summarizer.shown('run-1')).toBeNull()
})

test('a failure other than a rate limit gets no hand-off', async () => {
  const w = fakePorts([CLAUDE, CODEX])
  const summarizer = summarizerOf()

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.claude, apiError(500))

  expect(w.codex).toHaveLength(0)
  expect(w.logs).toHaveLength(1)
  expect(summarizer.shown('run-1')).toBeNull()
})

test('a refused request with no other provider to take it is reported once and settles no label', async () => {
  const w = fakePorts([CLAUDE])
  const summarizer = summarizerOf()

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.claude, apiError(429))

  expect(w.logs).toHaveLength(1)
  expect(summarizer.shown('run-1')).toBeNull()
})

test('a call cancelled while the clock is read after a Codex 429 starts no hand-off', async () => {
  const choice = createSummaryChoice()
  const controller = new AbortController()
  const claude: unknown[] = []
  let reads = 0
  const ports = {
    // The first read is the pause after the 429, the second the hand-off's.
    now: async () => {
      if (++reads === 2) controller.abort()
      return 0
    },
    completeCodex: async () => apiError(429),
    complete: async (request: unknown) => void claude.push(request) as never,
    record: async () => {},
  }
  const call = { runId: 'run-1', signal: controller.signal, candidates: [CODEX, CLAUDE] }

  const result = await choice.complete(ports, choice.take([CODEX, CLAUDE], 0)!, { prompt: 'p' }, call)

  expect(claude).toEqual([])
  expect(result).toMatchObject({ isAnswered: false, status: 429 })
})

test('a call cancelled before it is sent is not sent', async () => {
  const choice = createSummaryChoice()
  const controller = new AbortController()
  const sent: unknown[] = []
  const ports = {
    now: async () => 0,
    completeCodex: async (request: unknown) => void sent.push(request) as never,
    complete: async (request: unknown) => void sent.push(request) as never,
    record: async () => {},
  }
  const call = { runId: 'run-1', signal: controller.signal, candidates: [CODEX], onAttempt: async () => controller.abort() }

  const result = await choice.complete(ports, choice.take([CODEX], 0)!, { prompt: 'p' }, call)

  expect(sent).toEqual([])
  expect(result).toMatchObject({ isAnswered: false, reason: 'aborted' })
})

// The limits of a provider.

test('a third concurrent summary finds the Claude slots taken and goes to the Codex candidate', async () => {
  const w = fakePorts([CLAUDE, CODEX])
  const summarizer = summarizerOf()

  for (const runId of ['run-1', 'run-2', 'run-3']) await summarizer.observe(w.ports, runId, [msg('e1')])

  expect(w.claude).toHaveLength(2)
  expect(w.codex).toHaveLength(1)
})

test('a third concurrent summary waits when only Claude candidates are left', async () => {
  const w = fakePorts([CLAUDE])
  const summarizer = summarizerOf()

  for (const runId of ['run-1', 'run-2', 'run-3']) await summarizer.observe(w.ports, runId, [msg('e1')])

  expect(w.claude).toHaveLength(2)
  expect(w.codex).toHaveLength(0)
})

test('a Codex candidate has two slots of its own', async () => {
  const w = fakePorts([CODEX])
  const summarizer = summarizerOf()

  for (const runId of ['run-1', 'run-2', 'run-3']) await summarizer.observe(w.ports, runId, [msg('e1')])

  expect(w.codex).toHaveLength(2)
})

test('a Claude 429 does not hold back Codex', async () => {
  const w = fakePorts([CLAUDE])
  const summarizer = summarizerOf()
  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.claude, apiError(429))

  await summarizer.observe(w.ports, 'run-2', [msg('e1')])
  expect(w.claude).toHaveLength(0)

  w.state.candidates = [CLAUDE, CODEX]
  await summarizer.observe(w.ports, 'run-3', [msg('e1')])
  expect(w.claude).toHaveLength(0)
  expect(w.codex).toHaveLength(1)
})

// The bill.

test('a Codex summary is not recorded through usage add', async () => {
  const w = fakePorts([CODEX])

  await summarizerOf().observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.codex, answered('Reading auth.go'))

  expect(w.recorded).toEqual([])
})

test('an Anthropic summary is recorded under the model it used', async () => {
  const w = fakePorts([{ provider: 'anthropic', model: 'claude-sonnet-5' }])

  await summarizerOf().observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.claude, answered('Reading auth.go'))

  expect(w.recorded).toEqual([{ usage: answered('').usage, runId: 'run-1', model: 'claude-sonnet-5' }])
})

test('a request handed off to Claude is recorded under the model of the candidate that took it', async () => {
  const w = fakePorts([CODEX, CLAUDE])

  await summarizerOf().observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(w.codex, apiError(429))
  await w.answer(w.claude, answered('Reading auth.go'))

  expect(w.recorded.map((r) => r.model)).toEqual(['claude-haiku-5-5'])
})

// The signals.

test('a live Codex summary is asked with the run id, and aborted when its run is forgotten', async () => {
  const w = fakePorts([CODEX])
  const summarizer = summarizerOf()

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  expect(w.codex[0]!.call.runId).toBe('run-1')
  expect(w.codex[0]!.signal!.aborted).toBe(false)

  summarizer.forget('run-1')
  await flush()

  expect(w.codex[0]!.signal!.aborted).toBe(true)
  expect(w.logs).toHaveLength(0)
})

// The recorder.

const recorderOf = (w: ReturnType<typeof fakePorts>) => createRecorder({ runId: 'run-1', every: 8, choice: createSummaryChoice(), ports: w.ports })
const finishing = (recorder: ReturnType<typeof recorderOf>, w: ReturnType<typeof fakePorts>, over: Record<string, unknown> = {}) => {
  const state = { isDone: false }
  void recorder.finish({ isAborted: () => false, ports: w.ports, ...over }).then(() => void (state.isDone = true))
  return state
}

test('the finish of a recorder waits 30 s for a Codex call', async () => {
  const w = fakePorts([CODEX])
  const recorder = recorderOf(w)
  recorder.add(entries(8))
  await flush()

  const end = finishing(recorder, w)
  await flush()
  await w.expire(15000)

  expect(w.activeTimers()).toEqual([30000])
  expect(end.isDone).toBe(false)

  await w.expire(30000)

  expect(end.isDone).toBe(true)
  expect(w.codex[0]!.signal!.aborted).toBe(true)
})

test('the finish of a recorder waits 15 s for a Claude call', async () => {
  const w = fakePorts([CLAUDE])
  const recorder = recorderOf(w)
  recorder.add(entries(8))
  await flush()

  const end = finishing(recorder, w)
  await flush()
  await w.expire(30000)

  expect(w.activeTimers()).toEqual([15000])
  expect(end.isDone).toBe(false)

  await w.expire(15000)

  expect(end.isDone).toBe(true)
})

test('the finish of a recorder waits 30 s for the last window when a Codex call takes it', async () => {
  const w = fakePorts([CODEX])
  const recorder = recorderOf(w)
  recorder.add(entries(3))
  await flush()

  const end = finishing(recorder, w)
  await flush()

  expect(w.codex).toHaveLength(1)
  expect(w.activeTimers()).toEqual([30000])
  await w.answer(w.codex, answered('Read the entries.'))
  expect(end.isDone).toBe(true)
})

test('finishing a run with a Codex call in flight aborts it', async () => {
  const w = fakePorts([CODEX])
  const recorder = recorderOf(w)
  recorder.add(entries(8))
  await flush()
  expect(w.codex[0]!.call.runId).toBe('run-1')

  await recorder.finish({ isAborted: () => true, ports: w.ports })

  expect(w.codex[0]!.signal!.aborted).toBe(true)
})

test('a turn stopped while a Codex call is in flight aborts it and returns no records', async () => {
  const w = fakePorts([CODEX])
  const recorder = recorderOf(w)
  const turn = new AbortController()
  recorder.add(entries(8))
  await flush()

  const result = finishOnStop({ recorder, runId: 'run-1', signal: turn.signal, ports: w.ports })
  await flush()
  expect(w.codex[0]!.signal!.aborted).toBe(false)
  turn.abort()

  expect(await result).toEqual([])
  expect(w.codex[0]!.signal!.aborted).toBe(true)
})

test('a recorded summary asked of Claude is recorded under the model it used', async () => {
  const w = fakePorts([{ provider: 'anthropic', model: 'claude-sonnet-5' }])
  const recorder = recorderOf(w)

  recorder.add(entries(8))
  await flush()
  await w.answer(w.claude, answered('Read the entries.'))

  expect(w.recorded.map((r) => r.model)).toEqual(['claude-sonnet-5'])
})

test('live and recorded summaries share the slots of a provider', async () => {
  const w = fakePorts([CODEX])
  const choice = createSummaryChoice()
  const summarizer = createSummarizer({}, choice)
  const recorder = createRecorder({ runId: 'run-2', every: 8, choice, ports: w.ports })

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  recorder.add(entries(8))
  await flush()
  expect(w.codex).toHaveLength(2)

  await summarizer.observe(w.ports, 'run-3', [msg('e1')])
  expect(w.codex).toHaveLength(2)
})
