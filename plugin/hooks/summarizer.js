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
  summaryRequest,
  wholeWithin,
} from './summary.js'

// createSummarizer is one load's summaries; `options` is the plugin's config
// and `choice` the session's model choice and request limits, shared with the
// recorded summaries. What it needs of the engine arrives as `ports`, because a
// hooks module may not hand `$` to another module: surfaces(), now(),
// candidates(), complete(request), completeCodex(request, call), record(usage,
// runId, model), log(text) and labelSettled(runId), and optionally isEnabled(),
// which overrides the summaries option.
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
    const lease = isDue(run, every) ? choice.take(candidates, now) : null
    if (lease === null) return

    run.isInFlight = true
    run.hasRequested = true
    const sent = run.entries
    run.entries = []
    run.settling = settle(ports, runId, run, sent, { lease, candidates }).catch(() => {})
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

  return { observe, take, settled, shown, forget }
}
