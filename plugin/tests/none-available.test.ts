import { expect, test } from 'claude-code/testing'
import { createRecorder } from '../hooks/recorder.js'
import { createSummarizer } from '../hooks/summarizer.js'
import { createSummaryChoice } from '../hooks/summary-choice.js'
import { pickWord } from '../hooks/summary.js'
import { answered, apiError, cmd, msg } from './kit'

const CLAUDE = { provider: 'anthropic', model: 'claude-haiku-5-5' }
const CODEX = { provider: 'codex', model: 'gpt-6-luna' }
const reasoning = (text: string) => ({ kind: 'reasoning', text })
const WORD_LINE = /^» [A-Za-z]+…$/

// fakePorts is the engine beneath the summaries: the candidates the test sets,
// completions that wait until the test answers them, and a `random` that cycles
// through the numbers the test gives.
function fakePorts(candidates: { provider: string; model: string }[], randoms: number[] = [0]) {
  const state = { candidates }
  const requests: unknown[] = []
  const waiting: ((result: unknown) => void)[] = []
  const logs: string[] = []
  let draws = 0
  const ports = {
    surfaces: async () => ['terminal'],
    now: async () => 0,
    candidates: async () => state.candidates,
    complete: (request: unknown) => new Promise((resolve) => (requests.push(request), waiting.push(resolve))),
    completeCodex: (request: unknown) => new Promise(() => void requests.push(request)),
    record: async () => {},
    log: (text: string) => void logs.push(text),
    labelSettled: () => {},
    random: () => randoms[draws++ % randoms.length]!,
    after: () => ({ cancel: () => {} }),
  }
  return { ports, state, requests, logs, answer: async (result: unknown) => (waiting.shift()!(result), flush()) }
}

async function flush() {
  for (let i = 0; i < 50; i++) await Promise.resolve()
}

// pause holds the provider of `candidate` for a minute from time 0, by a 429.
async function pause(choice: ReturnType<typeof createSummaryChoice>, candidate: typeof CLAUDE) {
  const ports = { now: async () => 0, complete: async () => apiError(429), completeCodex: async () => apiError(429), record: async () => {} }
  const call = { runId: 'run-0', signal: new AbortController().signal, candidates: [candidate] }
  await choice.complete(ports, choice.take([candidate], 0)!, {}, call)
}

// The choice.

test('no candidate at all is none available', () => {
  expect(createSummaryChoice().isNoneAvailable([], 0)).toBe(true)
})

test('a provider with both slots taken is busy, not none available', () => {
  const choice = createSummaryChoice()
  choice.take([CLAUDE], 0)
  choice.take([CLAUDE], 0)

  expect(choice.take([CLAUDE], 0)).toBeNull()
  expect(choice.isNoneAvailable([CLAUDE], 0)).toBe(false)
})

test('candidates whose providers are all paused are none available, and one that is not paused makes it available', async () => {
  const choice = createSummaryChoice()
  await pause(choice, CLAUDE)

  expect(choice.isNoneAvailable([CLAUDE], 0)).toBe(true)
  expect(choice.isNoneAvailable([CLAUDE, CODEX], 0)).toBe(false)

  await pause(choice, CODEX)

  expect(choice.isNoneAvailable([CLAUDE, CODEX], 0)).toBe(true)
  expect(choice.isNoneAvailable([CLAUDE, CODEX], 60000)).toBe(false)
})

// The words.

test('a word is a plain word, and the one after a given word is another', () => {
  const first = pickWord(() => 0, null)

  expect(first).toMatch(/^[A-Za-z]+$/)
  expect(pickWord(() => 0.999, null)).not.toBe(first)
  expect(pickWord(() => 0, first)).not.toBe(first)
})

// The live summary.

test('with no candidate a due label is a random word line, and no request is made', async () => {
  const w = fakePorts([])
  const summarizer = createSummarizer({}, createSummaryChoice())

  await summarizer.observe(w.ports, 'run-1', [msg('I will read auth.go')])

  expect(w.requests).toEqual([])
  const words = summarizer.takeWords('run-1')
  expect(words).toHaveLength(1)
  expect(words[0]).toMatch(WORD_LINE)
  expect(summarizer.spinner('run-1')).toBe(words[0])
})

test('a random word is no label: the band and the row history have none', async () => {
  const w = fakePorts([])
  const summarizer = createSummarizer({}, createSummaryChoice())

  await summarizer.observe(w.ports, 'run-1', [msg('I will read auth.go')])

  expect(summarizer.shown('run-1')).toBeNull()
  expect(summarizer.take('run-1')).toEqual([])
})

test('a word is taken once, and a new one comes each time a label is due', async () => {
  const w = fakePorts([], [0])
  const summarizer = createSummarizer({}, createSummaryChoice())

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  const first = summarizer.takeWords('run-1')
  expect(summarizer.takeWords('run-1')).toEqual([])

  await summarizer.observe(w.ports, 'run-1', [msg('e2'), msg('e3')])
  expect(summarizer.takeWords('run-1')).toEqual([])

  await summarizer.observe(w.ports, 'run-1', [msg('e4')])
  const second = summarizer.takeWords('run-1')
  expect(second).toHaveLength(1)
  expect(second[0]).toMatch(WORD_LINE)
  expect(second).not.toEqual(first)
})

test('providers that are busy, not unavailable, give no word', async () => {
  const w = fakePorts([CLAUDE])
  const summarizer = createSummarizer({}, createSummaryChoice())

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await summarizer.observe(w.ports, 'run-2', [msg('e1')])
  await summarizer.observe(w.ports, 'run-3', [msg('e1')])

  expect(w.requests).toHaveLength(2)
  expect(summarizer.takeWords('run-3')).toEqual([])
  expect(summarizer.spinner('run-3')).toBeNull()
})

test('a provider paused by a 429 is none available, so a due label is a word', async () => {
  const w = fakePorts([CLAUDE])
  const choice = createSummaryChoice()
  await pause(choice, CLAUDE)
  const summarizer = createSummarizer({}, choice)

  await summarizer.observe(w.ports, 'run-1', [msg('e1')])

  expect(w.requests).toEqual([])
  expect(summarizer.takeWords('run-1')).toHaveLength(1)
})

test('a word that replaces a settled label leaves the run with no label to show, and none unread', async () => {
  const w = fakePorts([CLAUDE])
  const summarizer = createSummarizer({}, createSummaryChoice())
  await summarizer.observe(w.ports, 'run-1', [msg('e1')])
  await w.answer(answered('Reading auth.go'))
  expect(summarizer.shown('run-1')).toBe('Reading auth.go')

  w.state.candidates = []
  await summarizer.observe(w.ports, 'run-1', [msg('e2'), msg('e3'), msg('e4')])

  expect(summarizer.shown('run-1')).toBeNull()
  expect(summarizer.take('run-1')).toEqual([])
  expect(summarizer.takeWords('run-1')).toHaveLength(1)
  expect(summarizer.isOnWord('run-1')).toBe(true)
})

test('a word never taken is gone once a request starts, and the spinner keeps the label or none', async () => {
  const w = fakePorts([])
  const summarizer = createSummarizer({}, createSummaryChoice())
  await summarizer.observe(w.ports, 'run-1', [msg('e1')])

  w.state.candidates = [CLAUDE]
  await summarizer.observe(w.ports, 'run-1', [msg('e2'), msg('e3'), msg('e4')])

  expect(w.requests).toHaveLength(1)
  expect(summarizer.takeWords('run-1')).toEqual([])
  expect(summarizer.spinner('run-1')).toBeNull()
})

test('a word never taken is gone once a label settles, and the spinner gives that label', async () => {
  const w = fakePorts([])
  const summarizer = createSummarizer({}, createSummaryChoice())
  await summarizer.observe(w.ports, 'run-1', [msg('e1')])

  w.state.candidates = [CLAUDE]
  await summarizer.observe(w.ports, 'run-1', [msg('e2'), msg('e3'), msg('e4')])
  await w.answer(answered('Reading auth.go'))

  expect(summarizer.takeWords('run-1')).toEqual([])
  expect(summarizer.spinner('run-1')).toBe('Reading auth.go')
})

test('a label that settles after a word is the run label again, and the word is gone', async () => {
  const w = fakePorts([])
  const summarizer = createSummarizer({}, createSummaryChoice())
  await summarizer.observe(w.ports, 'run-1', [msg('e1')])

  w.state.candidates = [CLAUDE]
  await summarizer.observe(w.ports, 'run-1', [msg('e2'), msg('e3'), msg('e4')])
  await w.answer(answered('Reading auth.go'))

  expect(summarizer.shown('run-1')).toBe('Reading auth.go')
  expect(summarizer.isOnWord('run-1')).toBe(false)
  expect(summarizer.spinner('run-1')).toBe('Reading auth.go')
})

// The recorded summary.

const recorderOf = (w: ReturnType<typeof fakePorts>, every = 4) => createRecorder({ runId: 'run-1', every, choice: createSummaryChoice(), ports: w.ports })
const textsOf = (recorder: ReturnType<typeof recorderOf>) => recorder.records().map((record) => record.text)

test('with no candidate the record of a window is its last message, with no request', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w)

  recorder.add([msg('first thought'), cmd('rg -n token auth.go'), msg('Token checks are in auth.go.'), cmd('go test ./auth')])
  await flush()

  expect(w.requests).toEqual([])
  expect(textsOf(recorder)).toEqual(['» 1–4: Token checks are in auth.go.\n'])
})

test('with no message in the window the record is its last command, as a shell line', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w)

  recorder.add([cmd('rg -n token auth.go'), reasoning('thinking'), cmd('go test ./auth'), reasoning('more thinking')])
  await flush()

  expect(textsOf(recorder)).toEqual(['» 1–4: $ go test ./auth\n'])
})

test('a window that ends in a reasoning entry still takes the last message before it', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w)

  recorder.add([msg('Read auth.go.'), cmd('go test ./auth'), msg('Tests pass.'), reasoning('deciding what is next')])
  await flush()

  expect(textsOf(recorder)).toEqual(['» 1–4: Tests pass.\n'])
})

test('a window of reasoning only writes no record, and the next window starts after it', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w)

  recorder.add([reasoning('a'), reasoning('b'), reasoning('c'), reasoning('d')])
  await flush()
  expect(recorder.records()).toEqual([])

  recorder.add([msg('w'), msg('x'), msg('y'), msg('z')])
  await flush()
  expect(textsOf(recorder)).toEqual(['» 5–8: z\n'])
})

test('the proxy line is cleaned and cut like a record', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 1)
  const sentence = 'Checked the token path in auth.go and found it sound.'

  recorder.add([msg('\u001b[1mLine one\u001b[0m\n\n  line   two. ' + (sentence + ' ').repeat(12))])
  await flush()

  const text = textsOf(recorder)[0]!
  expect(text.startsWith('» 1: Line one line two. Checked the token path in auth.go and found it sound.')).toBe(true)
  expect(text.endsWith('sound.\n')).toBe(true)
  expect(text.length).toBeLessThanOrEqual(3 + 400 + 1)
  expect(text).not.toContain('\u001b')
})

test('paused providers write the proxy line too, and a window that waits for no one is written at the end of the run', async () => {
  const w = fakePorts([CLAUDE])
  const choice = createSummaryChoice()
  await pause(choice, CLAUDE)
  const recorder = createRecorder({ runId: 'run-1', every: 8, choice, ports: w.ports })

  recorder.add([msg('Read auth.go.'), cmd('go test ./auth')])
  await recorder.finish({ isAborted: () => false, ports: { ...w.ports, after: () => ({ cancel: () => {} }) } })

  expect(w.requests).toEqual([])
  expect(textsOf(recorder)).toEqual(['» 1–2: Read auth.go.\n'])
})

test('providers that are busy, not unavailable, write no proxy line', async () => {
  const w = fakePorts([CLAUDE])
  const choice = createSummaryChoice()
  choice.take([CLAUDE], 0)
  choice.take([CLAUDE], 0)
  const recorder = createRecorder({ runId: 'run-1', every: 4, choice, ports: w.ports })

  recorder.add([msg('a'), msg('b'), msg('c'), msg('d')])
  await flush()

  expect(recorder.records()).toEqual([])
  expect(w.requests).toEqual([])
})

test('a recorder with candidates still asks the model, not the proxy', async () => {
  const w = fakePorts([CLAUDE])
  const recorder = recorderOf(w)

  recorder.add([msg('a'), msg('b'), msg('c'), msg('d')])
  await flush()

  expect(w.requests).toHaveLength(1)
  expect(recorder.records()).toEqual([])
})

// The close of a step.

test('closing a step with none available writes the proxy line of the entries so far, below the cadence, with no request', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 8)

  recorder.add([msg('Read auth.go.'), cmd('go test ./auth'), msg('Tests pass.')])
  await recorder.closeStep(w.ports)

  expect(w.requests).toEqual([])
  expect(textsOf(recorder)).toEqual(['» 1–3: Tests pass.\n'])
})

test('closing a step spends the window, so the next step starts after it', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 8)

  recorder.add([msg('a'), msg('b')])
  await recorder.closeStep(w.ports)
  recorder.add([msg('c'), msg('d')])
  await recorder.closeStep(w.ports)

  expect(textsOf(recorder)).toEqual(['» 1–2: b\n', '» 3–4: d\n'])
})

test('closing a step with a provider available writes nothing and asks nothing', async () => {
  const w = fakePorts([CLAUDE])
  const recorder = recorderOf(w, 8)

  recorder.add([msg('a'), msg('b')])
  await recorder.closeStep(w.ports)

  expect(w.requests).toEqual([])
  expect(recorder.records()).toEqual([])
})

test('closing a step of a recorder that is told never to record writes nothing', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 0)

  recorder.add([msg('a'), msg('b')])
  await recorder.closeStep(w.ports)

  expect(recorder.records()).toEqual([])
})

test('closing a step while a request is in flight writes nothing', async () => {
  const w = fakePorts([CLAUDE])
  const recorder = recorderOf(w, 2)
  recorder.add([msg('a'), msg('b')])
  await flush()
  expect(w.requests).toHaveLength(1)

  w.state.candidates = []
  recorder.add([msg('c')])
  await recorder.closeStep(w.ports)

  expect(recorder.records()).toEqual([])
  expect(w.requests).toHaveLength(1)
})

test('closing a step after the run began to finish writes nothing', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 8)
  recorder.add([msg('a'), msg('b')])

  void recorder.finish({ isAborted: () => true, ports: w.ports })
  await recorder.closeStep(w.ports)

  expect(recorder.records()).toEqual([])
})

test('a step that closes while a finish began during its reads writes nothing', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 8)
  recorder.add([msg('a'), msg('b')])

  const closing = recorder.closeStep(w.ports)
  void recorder.finish({ isAborted: () => true, ports: w.ports })
  await closing

  expect(recorder.records()).toEqual([])
})

test('closing a step with a window of reasoning only spends it and writes no record', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 8)

  recorder.add([reasoning('a'), reasoning('b')])
  await recorder.closeStep(w.ports)
  expect(recorder.records()).toEqual([])

  recorder.add([msg('c')])
  await recorder.closeStep(w.ports)
  expect(textsOf(recorder)).toEqual(['» 3: c\n'])
})

test('closing a step with nothing unrecorded asks nothing of the engine', async () => {
  const w = fakePorts([])
  const recorder = recorderOf(w, 8)
  let reads = 0
  w.ports.candidates = async () => (reads++, [])

  await recorder.closeStep(w.ports)

  expect(reads).toBe(0)
})
