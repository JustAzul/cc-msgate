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
  wholeWithin,
} from './summary.js'

export const RECORD_CHARS = 400
export const RECORD_MAX_TOKENS = 120
// What one request carries: the newest entries not yet recorded.
export const RECORD_WINDOW_ENTRIES = 24
// How many entries a window waits for when nothing else is said.
export const DEFAULT_RECORDED_EVERY = 8
// The end of a run waits this long, in all, for the records it still owes: as
// long as the slowest provider it waits on is allowed.
export const FINISH_DEADLINE_MS = 15000
export const CODEX_FINISH_DEADLINE_MS = 30000

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

const deadlineOf = (provider) => (provider === 'codex' ? CODEX_FINISH_DEADLINE_MS : FINISH_DEADLINE_MS)

// createDeadline times the end of a run from `startedAt`. Each provider it waits
// on can lengthen the wait, never shorten it, and `onExpire` runs when it is up.
function createDeadline(ports, startedAt, onExpire) {
  let allowed = 0
  let timer
  return {
    async extend(provider) {
      const ms = deadlineOf(provider)
      if (ms <= allowed) return
      allowed = ms
      const left = ms - ((await ports.now()) - startedAt)
      // A longer wait asked for during the await has the timer.
      if (allowed !== ms) return
      timer?.cancel()
      timer = ports.after(Math.max(0, left), onExpire)
    },
    allowedMs: () => allowed,
    cancel: () => timer?.cancel(),
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
// `choice` is the session's model choice and limits, shared with the live
// summaries. A call to Codex is stopped when the recorder is abandoned. `ports` serve the
// requests add starts when the call names none; a follow that outlives the step
// it was made in names the current step's with each add, as finish takes its own
// for the ones it makes.
export function createRecorder({ runId, every: asked, choice, ports }) {
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
  // The provider of the call in flight, and the deadline of a finish under way.
  let provider = null
  let deadline = null
  const stop = new AbortController()

  // abandon ends the recorder's interest in replies: none that comes later
  // settles anything, and a call that can be stopped is.
  function abandon() {
    isAbandoned = true
    stop.abort()
  }

  const isOver = (isAborted) => isAbandoned || isAborted()

  function attempted(next) {
    provider = next
    return deadline?.extend(next)
  }

  // add takes the progress lines a display has just read, oldest first. It
  // starts a request when one is due and never waits for it. Once finish has
  // been called it ignores them.
  function add(lines, through = ports) {
    if (isFinishing) return

    for (const text of lines) unrecorded.push({ number: ++count, text })
    unrecorded = unrecorded.slice(-RECORD_WINDOW_ENTRIES)
    void startIfDue(through).catch((error) => {
      logFailure(through, 'could not schedule a record: ' + String(error && error.message ? error.message : error))
    })
  }

  const isDue = () => every > 0 && !isInFlight && unrecorded.length >= every

  // The checks that decide a start and the claim of a slot follow the last await.
  async function startIfDue(through) {
    if (!isDue()) return
    const candidates = await through.candidates()
    const now = await through.now()
    // A finish that began during the awaits starts the last window itself.
    if (isFinishing || !isDue()) return

    const lease = choice.take(candidates, now)
    if (lease !== null) start(through, lease, candidates)
  }

  // start sends everything unrecorded; the caller holds the leased slot, which
  // the choice gives back.
  function start(through, lease, candidates) {
    isInFlight = true
    inFlight = settle(through, unrecorded.slice(), { lease, candidates }).catch(() => {})
    return inFlight
  }

  async function settle(through, window, held) {
    const call = { runId, signal: stop.signal, candidates: held.candidates, onAttempt: attempted }
    let outcome
    try {
      outcome = recordOutcome(await choice.complete(through, held.lease, recordRequest(window), call))
    } catch (error) {
      outcome = { failure: 'the request failed: ' + String(error && error.message ? error.message : error) }
    } finally {
      isInFlight = false
      provider = null
    }

    if (outcome.sentence === undefined) {
      // A call that abandoning stopped has nothing to report.
      if (!stop.signal.aborted) logFailure(through, outcome.failure)
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
  // asks once for what is left, all under one deadline that the provider of the
  // call it waits on sets, and resolves to the records that settled. A stopped
  // run records nothing, and `signal`, when given, abandons the recorder as it
  // aborts. A port that throws ends the wait, not the run: the records that
  // settled are still returned.
  async function finish({ isAborted, ports, signal }) {
    // The run has ended: no entry comes after, and none may start a request.
    isFinishing = true
    if (every === 0 || isAborted()) {
      abandon()
      return []
    }

    signal?.addEventListener('abort', abandon, { once: true })
    try {
      await waitForRecords(isAborted, ports)
    } catch (error) {
      logFailure(ports, 'the end of the run failed: ' + String(error && error.message ? error.message : error))
    } finally {
      deadline?.cancel()
      signal?.removeEventListener('abort', abandon)
      // A reply that comes later settles nothing.
      abandon()
    }

    return isAborted() ? [] : settled.slice()
  }

  async function waitForRecords(isAborted, ports) {
    let expire
    const expired = new Promise((resolve) => {
      expire = resolve
    })
    deadline = createDeadline(ports, await ports.now(), () => {
      abandon()
      expire(false)
    })
    await deadline.extend(provider ?? 'anthropic')
    const isDone = await Promise.race([drain(isAborted, ports), expired])
    if (!isDone) logFailure(ports, 'the last window was not recorded within ' + deadline.allowedMs() + ' ms')
  }

  async function drain(isAborted, ports) {
    for (;;) {
      while (isInFlight) await inFlight
      if (isOver(isAborted) || unrecorded.length === 0) return true

      const candidates = await ports.candidates()
      const now = await ports.now()
      // Nothing can take the request, or an add can have started one: look again.
      if (isOver(isAborted) || candidates.length === 0) return true
      if (isInFlight) continue

      const lease = choice.take(candidates, now)
      if (lease !== null) {
        await start(ports, lease, candidates)
        return true
      }
      await choice.wait(candidates, now, ports)
    }
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
    return await Promise.race([recorder.finish({ isAborted: () => signal.aborted, ports, signal }), stopped])
  } catch (error) {
    ports.log('could not finish the record of run ' + runId + ': ' + String(error && error.message ? error.message : error))
    return []
  } finally {
    signal.removeEventListener('abort', onStop)
  }
}
