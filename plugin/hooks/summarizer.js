// The progress summaries of a session, shared by the agent row and the band:
// it keeps each run's entries since its last request and asks for a label when
// one is due. A display reads what it settled and never waits for a request.

import { createSummaryChoice } from './summary-choice.js'
import {
  ENTRIES_PER_REQUEST,
  WINDOW_ENTRIES,
  isDue,
  labelLine,
  newRun,
  newestEntries,
  outcomeOf,
  pickWord,
  summaryRequest,
  wholeWithin,
  wordLine,
} from './summary.js'

// createSummarizer is one load's summaries; `options` is the plugin's config
// and `choice` the session's model choice and request limits, shared with the
// recorded summaries. What it needs of the engine arrives as `ports`, because a
// hooks module may not hand `$` to another module: surfaces(), now(),
// candidates(), complete(request), completeCodex(request, call), record(usage,
// runId, model), log(text), labelSettled(runId) and random(), a number in [0, 1)
// that picks the word shown when no provider can be asked, and optionally
// isEnabled(), which overrides the summaries option.
export function createSummarizer(options, choice = createSummaryChoice()) {
  const runs = new Map()
  // A cadence beyond the window could never be reached.
  const every = wholeWithin(options.liveEvery, 1, WINDOW_ENTRIES, ENTRIES_PER_REQUEST)

  // observe takes the entries a display has just read of a run and starts a
  // request when one is due. It answers whether summaries are active, read each
  // time: the config allows them and the session draws on some surface.
  async function observe(ports, runId, lines) {
    if (!(await isAllowed(ports)) || (await ports.surfaces()).length === 0) return false
    let run = runs.get(runId)
    if (run === undefined) {
      run = { ...newRun(), controller: new AbortController() }
      runs.set(runId, run)
    }
    run.entries = newestEntries(run.entries.concat(lines))
    await startIfDue(ports, runId, run)
    return true
  }

  async function isAllowed(ports) {
    return ports.isEnabled === undefined ? options.summaries !== false : ports.isEnabled()
  }

  // The checks that decide a start and the claim of a slot follow the last await,
  // so two runs cannot both take the last slot.
  async function startIfDue(ports, runId, run) {
    if (!isDue(run, every)) return
    const candidates = await ports.candidates()
    const now = await ports.now()
    if (!isDue(run, every)) return
    if (choice.isNoneAvailable(candidates, now)) return showWord(ports, runId, run)
    const lease = choice.take(candidates, now)
    if (lease === null) return

    clearWord(run)
    run.isInFlight = true
    run.hasRequested = true
    const sent = run.entries
    run.entries = []
    run.settling = settle(ports, runId, run, sent, { lease, candidates }).catch(() => {})
  }

  // clearWord drops the run's random word, taken or not.
  function clearWord(run) {
    run.word = null
    run.isWordUnread = false
  }

  // showWord is the label of a due request that no provider can answer: a
  // random word, with nothing sent and nothing recorded. The entries it covers
  // are spent, as a request's are. The run's settled label goes: it is stale, and
  // the band, which never shows a word, falls back to the newest raw step.
  function showWord(ports, runId, run) {
    run.word = pickWord(ports.random, run.word)
    run.label = null
    run.isUnread = false
    run.isWordUnread = true
    run.hasRequested = true
    run.entries = []
    ports.labelSettled(runId)
  }

  // settle asks the choice for the label, which gives back the slot of `held`.
  async function settle(ports, runId, run, sent, held) {
    const call = { runId, signal: run.controller.signal, candidates: held.candidates }
    let outcome
    try {
      outcome = outcomeOf(await choice.complete(ports, held.lease, summaryRequest(sent, run.label), call))
    } catch (error) {
      outcome = { failure: 'the request failed: ' + String(error && error.message ? error.message : error) }
    } finally {
      run.isInFlight = false
    }

    // A run that was forgotten has nobody to show a label or a failure to.
    if (call.signal.aborted) return
    if (outcome.label !== undefined) {
      run.label = outcome.label
      clearWord(run)
      run.isUnread = true
      ports.labelSettled(runId)
      return
    }

    if (run.isFailureLogged) return
    run.isFailureLogged = true
    ports.log('could not summarize the progress of run ' + runId + ': ' + outcome.failure)
  }

  // take is what the agent row streams next: the label that settled since the
  // last call.
  function take(runId) {
    const run = runs.get(runId)
    if (run === undefined || !run.isUnread) return []
    run.isUnread = false
    return [labelLine(run.label)]
  }

  // takeWords is the random word that came since the last call, as the line the
  // thinking shows. It is no label: the row's history never keeps it.
  function takeWords(runId) {
    const run = runs.get(runId)
    if (run === undefined || !run.isWordUnread) return []
    run.isWordUnread = false
    return [wordLine(run.word)]
  }

  // spinner is what the spinner of a run says: its random word while no provider
  // can be asked, else its label, or null before one.
  function spinner(runId) {
    const run = runs.get(runId)
    if (run === undefined) return null
    return run.word === null ? run.label : wordLine(run.word)
  }

  // isOnWord tells that the run's newest label is a random word, which only the
  // spinner and the thinking line show.
  function isOnWord(runId) {
    return runs.get(runId)?.word != null
  }

  // settled resolves when the request in flight for the run, if any, has come
  // back: a display that must show the label of a window waits for it here.
  function settled(runId) {
    return runs.get(runId)?.settling ?? Promise.resolve()
  }

  // shown is what the band shows of a run in place of its newest raw step, or
  // null until a label has settled.
  function shown(runId) {
    return runs.get(runId)?.label ?? null
  }

  // forget drops a run and stops the request in flight for it that can be stopped.
  function forget(runId) {
    runs.get(runId)?.controller.abort()
    runs.delete(runId)
  }

  return { observe, take, takeWords, settled, shown, spinner, isOnWord, forget }
}
