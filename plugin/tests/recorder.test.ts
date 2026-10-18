import { expect, test } from 'claude-code/testing'
import { cleanRecord, createRecorder, finishOnStop } from '../hooks/recorder.js'
import { summarySettings } from '../hooks/settings.js'
import { createSummarizer } from '../hooks/summarizer.js'
import { createLimiter } from '../hooks/limiter.js'
import { answered, apiError, emptyReply } from './kit'

// fakePorts is the engine beneath a recorder or a summarizer: every completion
// waits until the test answers it, and no clock runs.
function fakePorts() {
  const requests: any[] = []
  const waiting: { resolve: (result: unknown) => void; reject: (error: unknown) => void }[] = []
  const logs: string[] = []
  const recorded: { usage: unknown; runId: string }[] = []
  const timers: { ms: number; callback: () => void; isCancelled: boolean }[] = []
  let now = 0
  const ports = {
    surfaces: async () => ['terminal'],
    now: async () => now,
    complete: (request: unknown) => {
      requests.push(request)
      return new Promise((resolve, reject) => waiting.push({ resolve, reject }))
    },
    record: async (usage: unknown, runId: string) => {
      recorded.push({ usage, runId })
    },
    log: (text: string) => {
      logs.push(text)
    },
    labelSettled: () => {},
    after: (ms: number, callback: () => void) => {
      const timer = { ms, callback, isCancelled: false }
      timers.push(timer)
      return { cancel: () => (timer.isCancelled = true) }
    },
  }
  return {
    ports,
    requests,
    logs,
    recorded,
    timers,
    advance: (ms: number) => (now += ms),
    // answer settles the oldest completion that has not been answered.
    answer: async (result: unknown) => {
      waiting.shift()!.resolve(result)
      await flush()
    },
    // refuse makes the oldest completion that has not been answered throw.
    refuse: async (error: Error) => {
      waiting.shift()!.reject(error)
      await flush()
    },
    // expire fires the timers set for `ms` that were not cancelled.
    expire: async (ms: number) => {
      for (const timer of timers) if (timer.ms === ms && !timer.isCancelled) timer.callback()
      await flush()
    },
  }
}

// flush lets every promise that can settle without a clock settle.
async function flush() {
  for (let i = 0; i < 50; i++) await Promise.resolve()
}

test('a live request in flight leaves one slot of the limiter the summarizer shares', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const summarizer = createSummarizer({}, limiter)

  await summarizer.observe(world.ports, 'run-1', ['e1'])

  expect(world.requests).toHaveLength(1)
  expect(limiter.tryTake(0)).toBe(true)
  expect(limiter.tryTake(0)).toBe(false)
})

test('the live summarizer asks again after the number of entries its options give', async () => {
  const world = fakePorts()
  const summarizer = createSummarizer({ liveEvery: 5 })

  await summarizer.observe(world.ports, 'run-1', ['e1'])
  await world.answer(answered('Reading a.go'))
  await summarizer.observe(world.ports, 'run-1', ['e2', 'e3', 'e4', 'e5'])
  expect(world.requests).toHaveLength(1)

  await summarizer.observe(world.ports, 'run-1', ['e6'])
  expect(world.requests).toHaveLength(2)
})

test('a 429 to a live summary pauses the limiter for a minute', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const summarizer = createSummarizer({}, limiter)

  await summarizer.observe(world.ports, 'run-1', ['e1'])
  await world.answer(apiError(429))

  expect(limiter.tryTake(59999)).toBe(false)
  expect(limiter.tryTake(60000)).toBe(true)
})

// A reply as a record.

test('a record is the reply with ANSI and control characters stripped and its whitespace collapsed', () => {
  expect(cleanRecord({ isAnswered: true, text: '  Read auth.go,\n\n then ran   the tests.\r\n' })).toBe('Read auth.go, then ran the tests.')
  expect(cleanRecord({ isAnswered: true, text: '\u001b[1;32mRan the store tests.\u001b[0m' })).toBe('Ran the store tests.')
  expect(cleanRecord({ isAnswered: true, text: '\u001b]0;title\u0007Read auth.go.\u0000' })).toBe('Read auth.go.')
})

test('a reply that is empty once cleaned, or unanswered, is no record', () => {
  for (const text of ['', '  \n ', '\u001b[0m']) {
    expect(cleanRecord({ isAnswered: true, text })).toBeNull()
  }
  expect(cleanRecord({ isAnswered: false, reason: 'api-error', status: 500 })).toBeNull()
  expect(cleanRecord(undefined)).toBeNull()
})

// A reply longer than a record is cut, not refused.

const WORD = 'abcdefgh'
const words = (count: number) => Array(count).fill(WORD).join(' ')
const sentence = (count: number) => words(count) + '.'
const reply = (text: string) => ({ isAnswered: true, text })

test('a reply that fits in 400 characters is kept whole', () => {
  expect(cleanRecord(reply('x'.repeat(241)))).toBe('x'.repeat(241))
  expect(cleanRecord(reply('x'.repeat(400)))).toBe('x'.repeat(400))
})

test('a reply over 400 characters is cut after the last whole sentence that fits', () => {
  const text = [sentence(14), sentence(14), sentence(22)].join(' ')
  expect(text.length).toBeGreaterThan(400)
  expect(cleanRecord(reply(text))).toBe(sentence(14) + ' ' + sentence(14))

  const question = words(20) + '? ' + words(20) + '! ' + words(20) + '! ' + words(20) + '!'
  expect(cleanRecord(reply(question))).toBe(words(20) + '? ' + words(20) + '!')
})

test('a sentence that ends on the 400th character is kept, and the rest dropped', () => {
  const first = 'x'.repeat(399) + '.'
  expect(cleanRecord(reply(first + ' and then more'))).toBe(first)
})

test('a full stop inside a file name does not end a sentence', () => {
  const text = 'Opened auth.go ' + words(50)
  const cut = cleanRecord(reply(text))
  expect(cut!.length).toBeLessThanOrEqual(400)
  expect(cut!.endsWith(WORD)).toBe(true)
  expect(cut!.startsWith('Opened auth.go ' + WORD)).toBe(true)
})

test('a reply over 400 characters with no sentence end is cut at the last whole word that fits', () => {
  const text = words(50)
  expect(text.length).toBeGreaterThan(400)
  expect(cleanRecord(reply(text))).toBe(words(44))
})

test('a single unbroken token over 400 characters is cut at 400', () => {
  expect(cleanRecord(reply('x'.repeat(450)))).toBe('x'.repeat(400))
})

// The recorder.

const SYSTEM = [
  'You write one entry of the history of a coding agent\'s run, from the newest activity of the agent.',
  'Reply with one or two plain sentences in English, in the past tense, saying what it did. Name the file, command or function.',
  'At most 400 characters, no markdown, no quotes.',
].join('\n')

const entries = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => 'entry ' + (from + i))

function recorderOf(world: ReturnType<typeof fakePorts>, over: Record<string, unknown> = {}) {
  return createRecorder({ runId: 'run-1', every: 8, limiter: createLimiter(), ports: world.ports, ...over })
}

test('the recorder asks once eight entries have accumulated and not before, with the measured request', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 7))
  await flush()
  expect(world.requests).toHaveLength(0)

  recorder.add(entries(8, 8))
  await flush()
  expect(world.requests).toHaveLength(1)
  expect(world.requests[0]).toEqual({
    model: 'claude-haiku-5-5',
    effort: 'low',
    system: SYSTEM,
    prompt: 'Newest activity of the agent, oldest first:\n' + entries(1, 8).map((e) => '- ' + e).join('\n'),
    maxTokens: 120,
    timeoutMs: 15000,
  })
})

test('a reply settles as a record that names the first and last entry of its window', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(answered('Read auth.go and ran the tests.'))

  expect(recorder.records()).toEqual([{ from: 1, to: 8, text: '» 1–8: Read auth.go and ran the tests.\n' }])
})

test('entries are numbered across windows', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(answered('Read the tests.'))
  recorder.add(entries(9, 16))
  await flush()
  await world.answer(answered('Fixed auth.go.'))

  expect(recorder.records().map((r) => r.text)).toEqual(['» 1–8: Read the tests.\n', '» 9–16: Fixed auth.go.\n'])
})

test('a window of one entry prints its number once', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world, { every: 1 })

  recorder.add(entries(1, 1))
  await flush()
  await world.answer(answered('Read auth.go.'))
  recorder.add(entries(2, 2))
  await flush()
  await world.answer(answered('Ran the tests.'))

  expect(recorder.records().map((r) => r.text)).toEqual(['» 1: Read auth.go.\n', '» 2: Ran the tests.\n'])
})

test('a window carries the newest 24 unrecorded entries, and the older ones stay a gap', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  limiter.pause(0)
  const recorder = recorderOf(world, { limiter })

  recorder.add(entries(1, 31))
  await flush()
  expect(world.requests).toHaveLength(0)

  world.advance(60000)
  recorder.add(entries(32, 32))
  await flush()
  await world.answer(answered('Worked through the files.'))

  const sent = world.requests[0].prompt.split('\n').slice(1)
  expect(sent).toEqual(entries(9, 32).map((e) => '- ' + e))
  expect(recorder.records()).toEqual([{ from: 9, to: 32, text: '» 9–32: Worked through the files.\n' }])
})

test('an entry is cut to 300 characters when sent', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world, { every: 1 })

  recorder.add(['y'.repeat(301)])
  await flush()

  expect(world.requests[0].prompt).toBe('Newest activity of the agent, oldest first:\n- ' + 'y'.repeat(300))
})

// What counts as a failure.

const FAILURES: Record<string, (world: ReturnType<typeof fakePorts>) => Promise<void>> = {
  'an api error': (world) => world.answer(apiError(500)),
  'an empty reply': (world) => world.answer(emptyReply()),
  'a reply that is empty once cleaned': (world) => world.answer(answered('\u001b[0m  ')),
  'a request that throws': (world) => world.refuse(new Error('the engine refused the model')),
}

for (const [name, fail] of Object.entries(FAILURES)) {
  test(name + ' records nothing, is logged once per run, and leaves the window to the next request', async () => {
    const world = fakePorts()
    const recorder = recorderOf(world)

    recorder.add(entries(1, 8))
    await flush()
    await fail(world)
    expect(recorder.records()).toEqual([])
    expect(world.logs).toHaveLength(1)
    expect(world.logs[0]).toContain('run-1')

    recorder.add(entries(9, 9))
    await flush()
    expect(world.requests[1].prompt.split('\n').slice(1)).toEqual(entries(1, 9).map((e) => '- ' + e))
    await fail(world)

    expect(recorder.records()).toEqual([])
    expect(world.logs).toHaveLength(1)
  })
}

test('a window that failed is recorded by the next one, with the entries of both', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(apiError(500))
  recorder.add(entries(9, 9))
  await flush()
  await world.answer(answered('Read and fixed auth.go.'))

  expect(recorder.records()).toEqual([{ from: 1, to: 9, text: '» 1–9: Read and fixed auth.go.\n' }])
})

// The limiter and the bill.

test('a 429 to a recorded request pauses the live summaries for a minute', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const recorder = recorderOf(world, { limiter })
  const summarizer = createSummarizer({}, limiter)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(apiError(429))
  await summarizer.observe(world.ports, 'run-2', ['e1'])
  expect(world.requests).toHaveLength(1)

  world.advance(60000)
  await summarizer.observe(world.ports, 'run-2', [])
  expect(world.requests).toHaveLength(2)
})

test('a 429 to a live summary pauses the recorder for a minute', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const summarizer = createSummarizer({}, limiter)
  const recorder = recorderOf(world, { limiter })

  await summarizer.observe(world.ports, 'run-2', ['e1'])
  await world.answer(apiError(429))
  recorder.add(entries(1, 8))
  await flush()
  expect(world.requests).toHaveLength(1)

  world.advance(60000)
  recorder.add([])
  await flush()
  expect(world.requests).toHaveLength(2)
})

test('a recorded request in flight takes one of the two slots the live summaries share', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const summarizer = createSummarizer({}, limiter)
  const recorder = recorderOf(world, { limiter })

  recorder.add(entries(1, 8))
  await flush()
  await summarizer.observe(world.ports, 'run-2', ['e1'])
  await summarizer.observe(world.ports, 'run-3', ['e1'])

  expect(world.requests).toHaveLength(2)
})

test('a request the limiter refuses is made at the next add, with the entries since', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const summarizer = createSummarizer({}, limiter)
  const recorder = recorderOf(world, { limiter })

  await summarizer.observe(world.ports, 'run-2', ['e1'])
  await summarizer.observe(world.ports, 'run-3', ['e1'])
  recorder.add(entries(1, 8))
  await flush()
  expect(world.requests).toHaveLength(2)

  await world.answer(answered('Reading a.go'))
  recorder.add(entries(9, 9))
  await flush()
  expect(world.requests).toHaveLength(3)
  expect(world.requests[2].prompt.split('\n').slice(1)).toEqual(entries(1, 9).map((e) => '- ' + e))
})

test('a completion that used tokens is handed to the usage record for its run, answered or not', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)
  const billed = { input_tokens: 458, output_tokens: 18, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(answered('Read auth.go.'))
  recorder.add(entries(9, 16))
  await flush()
  await world.answer(answered('  '))
  recorder.add(entries(17, 17))
  await flush()
  await world.answer(apiError(500))

  expect(world.recorded).toEqual([
    { usage: billed, runId: 'run-1' },
    { usage: billed, runId: 'run-1' },
  ])
})

test('a recorder that is told never to ask makes no request, not even at the end', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world, { every: 0 })

  recorder.add(entries(1, 30))
  await flush()
  expect(world.requests).toHaveLength(0)

  expect(await recorder.finish({ isAborted: () => false, ports: world.ports })).toEqual([])
  expect(world.requests).toHaveLength(0)
})

// The end of a run.

const running = () => false

// ending starts finish and reports what it resolved to, without waiting for it.
function ending(recorder: ReturnType<typeof recorderOf>, world: ReturnType<typeof fakePorts>, isAborted = running) {
  const state: { result?: any[] } = {}
  void recorder.finish({ isAborted, ports: world.ports }).then((result: any[]) => {
    state.result = result
  })
  return state
}

test('finish asks once for the entries left over, even below the cadence, and resolves to every record', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 3))
  const end = ending(recorder, world)
  await flush()
  expect(world.requests).toHaveLength(1)
  await world.answer(answered('Read the files.'))

  expect(world.requests).toHaveLength(1)
  expect(end.result).toEqual([{ from: 1, to: 3, text: '» 1–3: Read the files.\n' }])
})

test('finish asks for nothing when every entry is recorded', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(answered('Read the files.'))
  const end = ending(recorder, world)
  await flush()

  expect(world.requests).toHaveLength(1)
  expect(end.result).toEqual([{ from: 1, to: 8, text: '» 1–8: Read the files.\n' }])
})

test('finish waits for the request in flight, then asks for what arrived meanwhile', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  recorder.add(entries(9, 11))
  const end = ending(recorder, world)
  await flush()
  expect(world.requests).toHaveLength(1)
  expect(end.result).toBeUndefined()

  await world.answer(answered('Read the files.'))
  expect(world.requests).toHaveLength(2)
  expect(world.requests[1].prompt.split('\n').slice(1)).toEqual(entries(9, 11).map((e) => '- ' + e))
  await world.answer(answered('Ran the tests.'))

  expect(end.result).toEqual([
    { from: 1, to: 8, text: '» 1–8: Read the files.\n' },
    { from: 9, to: 11, text: '» 9–11: Ran the tests.\n' },
  ])
})

test('finish has one deadline of 15 seconds for the wait and the final request, and cancels it when done', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  recorder.add(entries(9, 11))
  const end = ending(recorder, world)
  await flush()
  await world.answer(answered('Read the files.'))
  await world.answer(answered('Ran the tests.'))

  expect(world.timers.map((t) => t.ms)).toEqual([15000])
  expect(world.timers[0]!.isCancelled).toBe(true)
  expect(end.result).toHaveLength(2)
})

test('finish gives up on the request in flight at the deadline and ignores its late reply', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  recorder.add(entries(9, 11))
  const end = ending(recorder, world)
  await flush()
  await world.expire(15000)

  expect(end.result).toEqual([])
  await world.answer(answered('Read the files.'))
  expect(recorder.records()).toEqual([])
  expect(world.requests).toHaveLength(1)
})

test('finish gives up on the final request at the deadline, keeps the records that settled, and ignores the late reply', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  recorder.add(entries(9, 11))
  const end = ending(recorder, world)
  await flush()
  await world.answer(answered('Read the files.'))
  expect(world.requests).toHaveLength(2)
  await world.expire(15000)

  expect(end.result).toEqual([{ from: 1, to: 8, text: '» 1–8: Read the files.\n' }])
  await world.answer(answered('Ran the tests.'))
  expect(recorder.records()).toEqual(end.result)
})

test('finish of a stopped run resolves to no record at all, even those settled, and makes no call', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(answered('Read the files.'))
  recorder.add(entries(9, 11))
  const end = ending(recorder, world, () => true)
  await flush()

  expect(end.result).toEqual([])
  expect(world.requests).toHaveLength(1)
  expect(world.timers).toEqual([])
})

test('finish of a run stopped while it waits resolves to no record and asks for nothing more', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)
  let isStopped = false

  recorder.add(entries(1, 8))
  await flush()
  recorder.add(entries(9, 11))
  const end = ending(recorder, world, () => isStopped)
  await flush()
  isStopped = true
  await world.answer(answered('Read the files.'))

  expect(end.result).toEqual([])
  expect(world.requests).toHaveLength(1)
})

test('finish waits for a slot of the limiter and asks as soon as one frees', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  const summarizer = createSummarizer({}, limiter)
  const recorder = recorderOf(world, { limiter })

  await summarizer.observe(world.ports, 'run-2', ['e1'])
  await summarizer.observe(world.ports, 'run-3', ['e1'])
  recorder.add(entries(1, 3))
  const end = ending(recorder, world)
  await flush()
  expect(world.requests).toHaveLength(2)

  await world.answer(answered('Reading a.go'))
  expect(world.requests).toHaveLength(3)
  await world.answer(answered('Reading b.go'))
  await world.answer(answered('Read the files.'))

  expect(end.result).toEqual([{ from: 1, to: 3, text: '» 1–3: Read the files.\n' }])
})

test('finish waits for a pause of the limiter that ends before the deadline', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  limiter.pause(0)
  const recorder = recorderOf(world, { limiter })

  world.advance(50000)
  recorder.add(entries(1, 3))
  const end = ending(recorder, world)
  await flush()
  expect(world.requests).toHaveLength(0)

  world.advance(10000)
  await world.expire(10000)
  expect(world.requests).toHaveLength(1)
  await world.answer(answered('Read the files.'))

  expect(end.result).toEqual([{ from: 1, to: 3, text: '» 1–3: Read the files.\n' }])
})

test('finish skips the final request when the limiter never frees, with one line in the log', async () => {
  const world = fakePorts()
  const limiter = createLimiter()
  limiter.pause(0)
  const recorder = recorderOf(world, { limiter })

  recorder.add(entries(1, 3))
  const end = ending(recorder, world)
  await flush()
  await world.expire(15000)

  expect(end.result).toEqual([])
  expect(world.requests).toHaveLength(0)
  expect(world.logs).toHaveLength(1)
  expect(world.logs[0]).toContain('run-1')
})

// The settings.

const ENV_NOTE = (value: string) => 'MSGATE_SUMMARIES=' + value + ' is not 0 or 1; the summaries option applies'

test('the flag in the environment wins over the summaries option, and an odd value leaves the option in force', () => {
  const cases: [string | undefined, boolean | undefined, boolean, string[]][] = [
    ['0', true, false, []],
    ['0', false, false, []],
    ['0', undefined, false, []],
    ['1', true, true, []],
    ['1', false, true, []],
    ['1', undefined, true, []],
    [undefined, true, true, []],
    [undefined, false, false, []],
    [undefined, undefined, true, []],
    ['abc', true, true, [ENV_NOTE('abc')]],
    ['abc', false, false, [ENV_NOTE('abc')]],
    ['abc', undefined, true, [ENV_NOTE('abc')]],
    ['', false, false, [ENV_NOTE('')]],
  ]
  for (const [env, summaries, isEnabled, notes] of cases) {
    const settings = summarySettings({ options: summaries === undefined ? {} : { summaries }, env })
    expect([env, summaries, settings.isEnabled, settings.notes]).toEqual([env, summaries, isEnabled, notes])
  }
})

test('the cadences default to 3 and 8, and an option without a value gives no note', () => {
  expect(summarySettings({ options: {}, env: undefined })).toEqual({ isEnabled: true, liveEvery: 3, recordedEvery: 8, notes: [] })
  expect(summarySettings({ options: undefined, env: undefined })).toMatchObject({ liveEvery: 3, recordedEvery: 8, notes: [] })
})

test('a live cadence is an integer from 1 to 10, or the default with a note naming the option', () => {
  for (const liveSummaryEvery of [1, 5, 10]) {
    expect(summarySettings({ options: { liveSummaryEvery }, env: undefined })).toMatchObject({ liveEvery: liveSummaryEvery, notes: [] })
  }
  for (const liveSummaryEvery of ['5', 2.5, -1, 0, 11, 1000, NaN, Infinity, null, true]) {
    const settings = summarySettings({ options: { liveSummaryEvery }, env: undefined })
    expect(settings.liveEvery).toBe(3)
    expect(settings.notes).toHaveLength(1)
    expect(settings.notes[0]).toContain('liveSummaryEvery')
  }
})

test('a recorded cadence is 0 or an integer from 1 to 24, or the default with a note naming the option', () => {
  for (const recordedSummaryEvery of [0, 1, 8, 24]) {
    expect(summarySettings({ options: { recordedSummaryEvery }, env: undefined })).toMatchObject({ recordedEvery: recordedSummaryEvery, notes: [] })
  }
  for (const recordedSummaryEvery of ['8', 2.5, -1, 25, 1000, NaN, Infinity, null, false]) {
    const settings = summarySettings({ options: { recordedSummaryEvery }, env: undefined })
    expect(settings.recordedEvery).toBe(8)
    expect(settings.notes).toHaveLength(1)
    expect(settings.notes[0]).toContain('recordedSummaryEvery')
  }
})

test('every setting that is off gives its own note, in the order live, recorded, flag', () => {
  const settings = summarySettings({ options: { liveSummaryEvery: 11, recordedSummaryEvery: 25 }, env: 'x' })
  expect(settings.notes).toHaveLength(3)
  expect(settings.notes.map((n) => n.split(' ')[0])).toEqual(['liveSummaryEvery', 'recordedSummaryEvery', 'MSGATE_SUMMARIES=x'])
})

test('an add that makes a window due, followed at once by finish, asks once for that window', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  const end = ending(recorder, world)
  await flush()
  expect(world.requests).toHaveLength(1)
  await world.answer(answered('Read the files.'))

  expect(world.requests).toHaveLength(1)
  expect(end.result).toEqual([{ from: 1, to: 8, text: '» 1–8: Read the files.\n' }])
})

test('entries added once finish has started are ignored, so no request runs beside the final one', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 3))
  const end = ending(recorder, world)
  await flush()
  recorder.add(entries(4, 11))
  await flush()
  expect(world.requests).toHaveLength(1)

  await world.answer(answered('Read the files.'))
  recorder.add(entries(12, 30))
  await flush()

  expect(world.requests).toHaveLength(1)
  expect(end.result).toEqual([{ from: 1, to: 3, text: '» 1–3: Read the files.\n' }])
})

test('a reply that is too long becomes a record cut to fit, and no block outgrows its limit', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world)

  recorder.add(entries(1, 8))
  await flush()
  await world.answer(answered(sentence(14) + ' ' + sentence(14) + ' ' + sentence(22)))
  recorder.add(entries(9, 16))
  await flush()
  await world.answer(answered('x'.repeat(450)))

  expect(world.logs).toEqual([])
  expect(recorder.records().map((r) => r.text)).toEqual([
    '» 1–8: ' + sentence(14) + ' ' + sentence(14) + '\n',
    '» 9–16: ' + 'x'.repeat(400) + '\n',
  ])
  for (const { from, to, text } of recorder.records()) {
    expect(text.length).toBeLessThanOrEqual(('» ' + from + '–' + to + ': ').length + 400 + 1)
  }
})

// A cadence the buffers cannot reach.

test('a live cadence beyond the window of ten asks at ten entries', async () => {
  const world = fakePorts()
  const summarizer = createSummarizer({ liveEvery: 11 })

  await summarizer.observe(world.ports, 'run-1', ['e1'])
  await world.answer(answered('Reading a.go'))
  await summarizer.observe(world.ports, 'run-1', entries(2, 10))
  expect(world.requests).toHaveLength(1)

  await summarizer.observe(world.ports, 'run-1', entries(11, 11))
  expect(world.requests).toHaveLength(2)
})

test('a live cadence that is not a whole number is the default of three', async () => {
  for (const liveEvery of [2.5, NaN, '5', null]) {
    const world = fakePorts()
    const summarizer = createSummarizer({ liveEvery })

    await summarizer.observe(world.ports, 'run-1', ['e1'])
    await world.answer(answered('Reading a.go'))
    await summarizer.observe(world.ports, 'run-1', entries(2, 3))
    expect(world.requests).toHaveLength(1)

    await summarizer.observe(world.ports, 'run-1', entries(4, 4))
    expect(world.requests).toHaveLength(2)
  }
})

test('a recorded cadence beyond the buffer of 24 asks at 24 entries', async () => {
  const world = fakePorts()
  const recorder = recorderOf(world, { every: 25 })

  recorder.add(entries(1, 23))
  await flush()
  expect(world.requests).toHaveLength(0)

  recorder.add(entries(24, 24))
  await flush()
  expect(world.requests).toHaveLength(1)
})

test('a recorded cadence that is not a whole number is the default of eight', async () => {
  for (const every of [2.5, NaN, '3', undefined]) {
    const world = fakePorts()
    const recorder = recorderOf(world, { every })

    recorder.add(entries(1, 7))
    await flush()
    expect(world.requests).toHaveLength(0)

    recorder.add(entries(8, 8))
    await flush()
    expect(world.requests).toHaveLength(1)
  }
})

// A port that throws at the end of a run.

for (const port of ['now', 'after'] as const) {
  test('finish resolves to the records that settled when ' + port + ' throws, and logs it once', async () => {
    const world = fakePorts()
    const recorder = recorderOf(world)

    recorder.add(entries(1, 8))
    await flush()
    await world.answer(answered('Read the files.'))
    recorder.add(entries(9, 11))
    const broken = {
      ...world.ports,
      [port]: () => {
        throw new Error('the engine is gone')
      },
    }
    const records = await recorder.finish({ isAborted: running, ports: broken })

    expect(records).toEqual([{ from: 1, to: 8, text: '» 1–8: Read the files.\n' }])
    expect(world.logs).toHaveLength(1)
    expect(world.logs[0]).toContain('run-1')
  })
}

test('a request that was about to start when a stopped run finished is not made', async () => {
  const world = fakePorts()
  let clockReads = 0
  let release!: (now: number) => void
  const slowClock = { ...world.ports, now: () => (++clockReads === 1 ? new Promise<number>((resolve) => (release = resolve)) : Promise.resolve(0)) }
  const recorder = createRecorder({ runId: 'run-1', every: 8, limiter: createLimiter(), ports: slowClock })

  recorder.add(entries(1, 8))
  expect(await recorder.finish({ isAborted: () => true, ports: slowClock })).toEqual([])
  release(0)
  await flush()

  expect(world.requests).toEqual([])
  expect(recorder.records()).toEqual([])
})

test('a clock that fails when a window is due is logged once, and the next add asks for the entries of both', async () => {
  const world = fakePorts()
  let isBroken = true
  const clock = { ...world.ports, now: () => (isBroken ? Promise.reject(new Error('the clock is gone')) : Promise.resolve(0)) }
  const recorder = createRecorder({ runId: 'run-1', every: 8, limiter: createLimiter(), ports: clock })

  recorder.add(entries(1, 8))
  await flush()
  recorder.add(entries(9, 9))
  await flush()
  expect(world.requests).toEqual([])
  expect(world.logs).toHaveLength(1)
  expect(world.logs[0]).toContain('run-1')
  expect(world.logs[0]).toContain('the clock is gone')

  isBroken = false
  recorder.add(entries(10, 10))
  await flush()
  expect(world.requests[0].prompt.split('\n').slice(1)).toEqual(entries(1, 10).map((e) => '- ' + e))
})

// Ending a follow with a stop that can come at any time.

// fakeSignal is an abort signal that keeps the listeners it was given.
function fakeSignal(isAborted = false) {
  const listeners = new Set<() => void>()
  const signal = {
    aborted: isAborted,
    addEventListener: (_type: string, listener: () => void) => void listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => void listeners.delete(listener),
  }
  return {
    signal,
    listeners,
    abort: () => {
      signal.aborted = true
      for (const listener of [...listeners]) listener()
    },
  }
}

// A recorder whose finish the test settles.
function fakeRecorder() {
  const finishes: { resolve: (records: unknown[]) => void; reject: (error: Error) => void }[] = []
  return {
    finishes,
    recorder: { finish: () => new Promise<unknown[]>((resolve, reject) => finishes.push({ resolve, reject })) },
  }
}

const RECORDS = [{ from: 1, to: 8, text: '» 1–8: Read the files.\n' }]

test('a run that ends is finished to the records its recorder settled, and the stop listener is gone', async () => {
  const { signal, listeners } = fakeSignal()
  const { recorder, finishes } = fakeRecorder()
  const logs: string[] = []

  const pending = finishOnStop({ recorder, runId: 'run-1', signal, ports: { log: (text: string) => logs.push(text) } })
  expect(listeners.size).toBe(1)
  finishes[0]!.resolve(RECORDS)

  expect(await pending).toEqual(RECORDS)
  expect(listeners.size).toBe(0)
  expect(logs).toEqual([])
})

test('a stop while the recorder is finishing resolves to no records at once, and a later finish changes nothing', async () => {
  const { signal, listeners, abort } = fakeSignal()
  const { recorder, finishes } = fakeRecorder()

  const pending = finishOnStop({ recorder, runId: 'run-1', signal, ports: { log: () => {} } })
  abort()

  expect(await pending).toEqual([])
  expect(listeners.size).toBe(0)
  finishes[0]!.resolve(RECORDS)
  await flush()
  expect(await pending).toEqual([])
})

test('a signal that is aborted already resolves to no records without waiting for the recorder', async () => {
  const { signal, listeners } = fakeSignal(true)
  const { recorder } = fakeRecorder()

  expect(await finishOnStop({ recorder, runId: 'run-1', signal, ports: { log: () => {} } })).toEqual([])
  expect(listeners.size).toBe(0)
})

test('a recorder that fails to finish resolves to no records with one line in the log that names the run', async () => {
  const { signal, listeners } = fakeSignal()
  const { recorder, finishes } = fakeRecorder()
  const logs: string[] = []

  const pending = finishOnStop({ recorder, runId: 'run-1', signal, ports: { log: (text: string) => logs.push(text) } })
  finishes[0]!.reject(new Error('the engine is gone'))

  expect(await pending).toEqual([])
  expect(listeners.size).toBe(0)
  expect(logs).toHaveLength(1)
  expect(logs[0]).toContain('run-1')
  expect(logs[0]).toContain('the engine is gone')
})
