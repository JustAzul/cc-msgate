// Pure helpers for the spawn notice: the toast and transcript line that tell
// the person a run started, with how long runs of its kind usually take.
// Nothing here touches the mods API.

import { withPurpose } from './lib.js'
import { formatElapsed } from './subagent.js'

// How long an ETA lookup may take; a stalled git must not hold a scan.
export const ETA_TIMEOUT_MS = 10000

// listedRuns keeps the runs of a `status --json` listing, newest first as
// msgate prints them, or null when the output is not a listing.
export function listedRuns(stdout) {
  try {
    const parsed = JSON.parse(stdout)
    if (!parsed || !Array.isArray(parsed.runs)) return null
    return parsed.runs.filter((run) => run && typeof run.run_id === 'string').map(withPurpose)
  } catch {
    return null
  }
}

// isAnnounceable tells a listed run whose notice can be written: it has a
// purpose, a source and a state.
export function isAnnounceable(run) {
  return [run.purpose, run.source, run.state].every((field) => typeof field === 'string' && field !== '')
}

export function etaCommand(bin, purpose, cwd) {
  return [bin, 'eta', '--purpose', purpose, '--cwd', cwd, '--json']
}

// etaText is the ETA part of a notice from the `eta --json` object, or null
// when the output cannot be read.
export function etaText(stdout) {
  let eta
  try {
    eta = JSON.parse(stdout)
  } catch {
    return null
  }
  if (!eta || typeof eta !== 'object') return null
  if (eta.basis === 'none') return 'ETA unknown (no history)'
  if (!Number.isFinite(eta.eta_ms)) return null
  if (eta.basis === 'repo' && isCount(eta.samples)) return 'ETA ~' + formatElapsed(eta.eta_ms) + ' (repo average, ' + counted(eta.samples, 'run') + ')'
  if (eta.basis === 'global' && isCount(eta.repos)) return 'ETA ~' + formatElapsed(eta.eta_ms) + ' (global average, ' + counted(eta.repos, 'repo') + ')'
  return null
}

function isCount(value) {
  return Number.isInteger(value) && value >= 0
}

function counted(count, noun) {
  return count + ' ' + noun + (count === 1 ? '' : 's')
}

export const ETA_UNAVAILABLE = 'ETA unavailable'

// spawnNotice is the text of a notice: the run, its trigger and the tail.
export function spawnNotice(purpose, source, tail) {
  return 'msgate · spawned ' + purpose + ' · trigger ' + source + ' · ' + tail
}

// endedSince tells whether a run ended at or after a time (epoch milliseconds);
// a run with no readable end time did not. End times are whole seconds, so the
// time is compared from the start of its second.
export function endedSince(run, since) {
  return Date.parse(run.ended_at ?? '') >= Math.floor(since / 1000) * 1000
}

// finishedTail is the tail of a notice for a run first seen already finished:
// how long it ran, or just that it finished when its times cannot be read.
export function finishedTail(run) {
  const start = Date.parse(run.started_at ?? run.admitted_at ?? '')
  const end = Date.parse(run.ended_at ?? '')
  if (Number.isNaN(start) || Number.isNaN(end)) return 'finished'
  return 'finished in ' + formatElapsed(end - start)
}
