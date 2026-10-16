// The progress summaries of a session, shared by the agent row and the band:
// it keeps each run's entries since its last request and asks for a label when
// one is due. A display reads what it settled and never waits for a request.

import {
  RATE_LIMIT_PAUSE_MS,
  SESSION_IN_FLIGHT_MAX,
  isBilled,
  isDue,
  isRateLimited,
  labelLine,
  newRun,
  newestEntries,
  outcomeOf,
  summaryRequest,
} from './summary.js'

// createSummarizer is one load's summaries; `options` is the plugin's config.
// What it needs of the engine arrives as `ports`, because a hooks module may
// not hand `$` to another module: surfaces(), now(), complete(request),
// record(usage, runId), log(text) and labelSettled(runId).
export function createSummarizer(options) {
  const runs = new Map()
  let inFlight = 0
  let pausedUntil = 0

  // observe takes the entries a display has just read of a run and starts a
  // request when one is due. It answers whether summaries are active, read each
  // time: the config allows them and the session draws on some surface.
  async function observe(ports, runId, lines) {
    if (options.summaries === false || (await ports.surfaces()).length === 0) return false
    let run = runs.get(runId)
    if (run === undefined) {
      run = newRun()
      runs.set(runId, run)
    }
    run.entries = newestEntries(run.entries.concat(lines))
    await startIfDue(ports, runId, run)
    return true
  }

  // The checks that decide a start and the claim of a slot follow the last await,
  // so two runs cannot both take the last slot.
  async function startIfDue(ports, runId, run) {
    if (!isDue(run)) return
    const now = await ports.now()
    if (!isDue(run) || inFlight >= SESSION_IN_FLIGHT_MAX || now < pausedUntil) return

    inFlight += 1
    run.isInFlight = true
    run.hasRequested = true
    const sent = run.entries
    run.entries = []
    void settle(ports, runId, run, sent).catch(() => {})
  }

  async function settle(ports, runId, run, sent) {
    let outcome
    try {
      const result = await ports.complete(summaryRequest(sent, run.label))
      // Recorded whether or not the reply is a label, and never waited for.
      if (isBilled(result)) void ports.record(result.usage, runId).catch(() => {})
      outcome = outcomeOf(result)
      if (isRateLimited(result)) pausedUntil = (await ports.now()) + RATE_LIMIT_PAUSE_MS
    } catch (error) {
      outcome = { failure: 'the request failed: ' + String(error && error.message ? error.message : error) }
    } finally {
      inFlight -= 1
      run.isInFlight = false
    }

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

  // shown is what the band shows of a run in place of its newest raw step, or
  // null until a label has settled.
  function shown(runId) {
    return runs.get(runId)?.label ?? null
  }

  function forget(runId) {
    runs.delete(runId)
  }

  return { observe, take, shown, forget }
}
