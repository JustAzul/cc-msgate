// The recorded summary of a run: a sentence or two for each window of its
// progress entries, kept while the run goes on and handed over when it ends.
// Nothing here touches the mods API; what it needs of the engine arrives as
// `ports`, as for the live summaries.

import {
  ANSI,
  CONTROL,
  ENTRY_CHARS,
  LABEL_MARK,
  SUMMARY_EFFORT,
  SUMMARY_MODEL,
  SUMMARY_TIMEOUT_MS,
  isBilled,
  isRateLimited,
  wholeWithin,
} from './summary.js'

export const RECORD_CHARS = 400
export const RECORD_MAX_TOKENS = 120
// What one request carries: the newest entries not yet recorded.
export const RECORD_WINDOW_ENTRIES = 24
// How many entries a window waits for when nothing else is said.
export const DEFAULT_RECORDED_EVERY = 8
// The end of a run waits this long, in all, for the records it still owes.
export const FINISH_DEADLINE_MS = 15000

export const RECORD_SYSTEM = [
  'You write one entry of the history of a coding agent\'s run, from the newest activity of the agent.',
  'Reply with one or two plain sentences in English, in the past tense, saying what it did. Name the file, command or function.',
  'At most ' + RECORD_CHARS + ' characters, no markdown, no quotes.',
].join('\n')

// A sentence ends at . ! or ? that a space or the end of the text follows, so
// the full stop of auth.go does not count.
function lastSentenceEnd(text, limit) {
  for (let i = limit - 1; i >= 0; i -= 1) {
    if (/[.!?]/.test(text[i]) && (i + 1 === text.length || text[i + 1] === ' ')) return i + 1
  }
  return 0
}

// fitted cuts a text longer than RECORD_CHARS to the last whole sentence that
// fits, else to the last whole word, else to the limit.
function fitted(text) {
  if (text.length <= RECORD_CHARS) return text
  const sentenceEnd = lastSentenceEnd(text, RECORD_CHARS)
  if (sentenceEnd > 0) return text.slice(0, sentenceEnd)
  const wordEnd = text.lastIndexOf(' ', RECORD_CHARS)
  return text.slice(0, wordEnd > 0 ? wordEnd : RECORD_CHARS)
}

// cleanRecord reads a completion as a record sentence, or null when it is not
// one: unanswered, or empty after cleaning. A longer text is cut to fit. Runs
// of whitespace become one space before control characters go, so a line break
// does not glue two words.
export function cleanRecord(result) {
  if (!result || result.isAnswered !== true) return null
  const record = String(result.text ?? '')
    .replace(ANSI, '')
    .replace(/\s+/g, ' ')
    .replace(CONTROL, '')
    .trim()
  return record === '' ? null : fitted(record)
}

// recordOutcome is what a finished completion comes to: the sentence of a
// record, or why there is none.
function recordOutcome(result) {
  const sentence = cleanRecord(result)
  if (sentence !== null) return { sentence }
  if (result && result.isAnswered === true) return { failure: 'the reply is not a record' }
  const status = result && typeof result.status === 'number' ? ' (status ' + result.status + ')' : ''
  return { failure: 'no reply: ' + String(result?.reason ?? 'unknown') + status }
}

// recordRequest is the completion that summarizes a window: its entries oldest
// first, each cut to ENTRY_CHARS.
export function recordRequest(window) {
  const lines = window.map((entry) => '- ' + entry.text.slice(0, ENTRY_CHARS)).join('\n')
  return {
    model: SUMMARY_MODEL,
    effort: SUMMARY_EFFORT,
    system: RECORD_SYSTEM,
    prompt: 'Newest activity of the agent, oldest first:\n' + lines,
    maxTokens: RECORD_MAX_TOKENS,
    timeoutMs: SUMMARY_TIMEOUT_MS,
  }
}

// recordText is the block a record writes into a row's history: the numbers of
// the first and last entry it covers, then the sentence.
function recordText(from, to, sentence) {
  return LABEL_MARK + (from === to ? from : from + '–' + to) + ': ' + sentence + '\n'
}

// createRecorder follows one run. It numbers the entries it is given from 1,
// keeps the newest RECORD_WINDOW_ENTRIES not yet recorded and, once `every` of
// them have accumulated, asks for a record of them. `every` is a whole number
// up to that buffer, 8 when it is not; 0 never asks.
// `limiter` is the session's, shared with the live summaries. `ports` serve the
// requests add starts; finish takes its own for the ones it makes.
export function createRecorder({ runId, every: asked, limiter, ports }) {
  // A cadence beyond the buffer could never be reached.
  const every = wholeWithin(asked, 0, RECORD_WINDOW_ENTRIES, DEFAULT_RECORDED_EVERY)
  const settled = []
  let count = 0
  let unrecorded = []
  let isInFlight = false
  let isFailureLogged = false
  let inFlight = Promise.resolve()
  let isAbandoned = false
  let isFinishing = false

  // add takes the progress lines a display has just read, oldest first. It
  // starts a request when one is due and never waits for it. Once finish has
  // been called it ignores them.
  function add(lines) {
    if (isFinishing) return

    for (const text of lines) unrecorded.push({ number: ++count, text })
    unrecorded = unrecorded.slice(-RECORD_WINDOW_ENTRIES)
    void startIfDue().catch((error) => {
      logFailure(ports, 'could not schedule a record: ' + String(error && error.message ? error.message : error))
    })
  }

  const isDue = () => every > 0 && !isInFlight && unrecorded.length >= every

  // The checks that decide a start and the claim of a slot follow the last await.
  async function startIfDue() {
    if (!isDue()) return
    const now = await ports.now()
    // A finish that began during the await starts the last window itself.
    if (isFinishing || !isDue() || !limiter.tryTake(now)) return

    start(ports)
  }

  // start sends everything unrecorded; the caller holds a slot of the limiter.
  function start(through) {
    isInFlight = true
    inFlight = settle(through, unrecorded.slice()).catch(() => {})
    return inFlight
  }

  async function settle(through, window) {
    let outcome
    try {
      const result = await through.complete(recordRequest(window))
      // Recorded whether or not the reply is a record, and never waited for.
      if (isBilled(result)) void through.record(result.usage, runId).catch(() => {})
      outcome = recordOutcome(result)
      if (isRateLimited(result)) limiter.pause(await through.now())
    } catch (error) {
      outcome = { failure: 'the request failed: ' + String(error && error.message ? error.message : error) }
    } finally {
      limiter.release()
      isInFlight = false
    }

    if (outcome.sentence === undefined) {
      logFailure(through, outcome.failure)
      return
    }

    if (isAbandoned) return

    const from = window[0].number
    const to = window[window.length - 1].number
    settled.push({ from, to, text: recordText(from, to, outcome.sentence) })
    unrecorded = unrecorded.filter((entry) => entry.number > to)
  }

  // One line per run: a run that fails every window would fill the log.
  function logFailure(through, failure) {
    if (isFailureLogged) return
    isFailureLogged = true
    through.log('could not record the progress of run ' + runId + ': ' + failure)
  }

  // finish is for a run that has ended. It waits for the request in flight and
  // asks once for what is left, all under one deadline, and resolves to the
  // records that settled. A stopped run records nothing. A port that throws
  // ends the wait, not the run: the records that settled are still returned.
  async function finish({ isAborted, ports }) {
    // The run has ended: no entry comes after, and none may start a request.
    isFinishing = true
    if (every === 0 || isAborted()) {
      isAbandoned = true
      return []
    }

    let timer
    try {
      const expired = new Promise((resolve) => {
        timer = ports.after(FINISH_DEADLINE_MS, () => {
          isAbandoned = true
          resolve(false)
        })
      })
      const isDone = await Promise.race([drain(isAborted, ports), expired])
      if (!isDone) logFailure(ports, 'the last window was not recorded within ' + FINISH_DEADLINE_MS + ' ms')
    } catch (error) {
      logFailure(ports, 'the end of the run failed: ' + String(error && error.message ? error.message : error))
    } finally {
      timer?.cancel()
      // A reply that comes later settles nothing.
      isAbandoned = true
    }

    return isAborted() ? [] : settled.slice()
  }

  async function drain(isAborted, ports) {
    for (;;) {
      while (isInFlight) await inFlight
      if (isAbandoned || isAborted() || unrecorded.length === 0) return true

      const now = await ports.now()
      if (isAbandoned || isAborted()) return true

      // An add can have started a request during the await: look again.
      if (isInFlight) continue
      if (limiter.tryTake(now)) break
      await slotMayBeFree(now, ports)
    }

    await start(ports)
    return true
  }

  // slotMayBeFree waits for a slot to be released or for the pause to end,
  // whichever is first; the deadline of finish cuts it short.
  async function slotMayBeFree(now, ports) {
    const pause = limiter.pausedFor(now)
    let timer
    const ended = new Promise((resolve) => {
      timer = pause > 0 ? ports.after(pause, resolve) : undefined
    })
    await Promise.race([limiter.freed(), ended])
    timer?.cancel()
  }

  return { add, finish, records: () => settled.slice() }
}

// finishOnStop ends a follow's recorder and returns the records it settled: none
// when the turn was stopped, which cuts the wait short whatever the recorder is
// still waiting for, and none when the end failed, since the run's answer does
// not depend on them. `signal` is the turn's abort signal; the listener it was
// given is always removed.
export async function finishOnStop({ recorder, runId, signal, ports }) {
  let onStop
  const stopped = new Promise((resolve) => {
    onStop = () => resolve([])
    if (signal.aborted) onStop()
    else signal.addEventListener('abort', onStop, { once: true })
  })
  try {
    return await Promise.race([recorder.finish({ isAborted: () => signal.aborted, ports }), stopped])
  } catch (error) {
    ports.log('could not finish the record of run ' + runId + ': ' + String(error && error.message ? error.message : error))
    return []
  } finally {
    signal.removeEventListener('abort', onStop)
  }
}
