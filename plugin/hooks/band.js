// Pure helpers for the band above the prompt that shows the session's runs
// another caller started with --agent-feedback (a hook's review, a script's
// job) while they work. Nothing here touches the mods API.

import { isTerminal, withPurpose } from './lib.js'
import { formatCost } from './prices.js'
import { AGENT_SOURCE, formatElapsed } from './subagent.js'

export const BAND_REFRESH_MS = 5000
// The most of a progress entry a band line carries; the band cuts the line to
// its width as well.
const PROGRESS_CHARS = 200

// flaggedRuns keeps the runs of a `status --json` listing that ask to be shown
// and are still going. The agent types' own runs already show as their agents.
export function flaggedRuns(stdout) {
  try {
    const parsed = JSON.parse(stdout)
    const runs = parsed && Array.isArray(parsed.runs) ? parsed.runs : []
    return runs.filter(isShownInBand).map(withPurpose)
  } catch {
    return []
  }
}

function isShownInBand(run) {
  if (!run || typeof run.run_id !== 'string') return false
  return run.agent_feedback === true && run.source !== AGENT_SOURCE && !isTerminal(run.state)
}

// followedRuns is what the band follows after a poll: every run the listing
// still flags, keeping what was already read of the ones it followed before.
export function followedRuns(followed, listed) {
  return listed.map((run) => followed.find((known) => known.run_id === run.run_id) ?? newFollowed(run))
}

function newFollowed(run) {
  const startedAt = Date.parse(run.started_at ?? run.admitted_at ?? '')
  return {
    run_id: run.run_id,
    purpose: String(run.purpose ?? ''),
    source: String(run.source ?? ''),
    startedAt: Number.isNaN(startedAt) ? null : startedAt,
    from: 0,
    line: '',
    cost: null,
  }
}

// bandLine is one followed run as the band shows it.
export function bandLine(run, now) {
  const parts = ['msgate', run.purpose, run.source]
  if (run.startedAt !== null) parts.push(formatElapsed(now - run.startedAt))
  parts.push(formatCost(run.cost) ?? '')
  if (run.line !== '') parts.push(run.line.replace(/\s+/g, ' ').trim().slice(0, PROGRESS_CHARS))
  return parts.filter((part) => part !== '').join(' · ')
}

// labelled is a run as the band shows it while summaries are active: its label
// in place of the newest raw step, or no step before the first label. A label
// of undefined means summaries are not active, and the run is as it was.
export function labelled(run, label) {
  if (label === undefined) return run
  return { ...run, line: label ?? '' }
}

// withProgress applies what a refresh read to the runs the band follows now:
// a run the read found ended is dropped, and one with new entries shows the
// newest. A run without a read (one a poll added meanwhile) is kept as it is,
// and so is the cost of one whose read failed; a read that answers no cost
// clears it.
export function withProgress(followed, read) {
  return followed.flatMap((run) => {
    const progress = read.get(run.run_id)
    if (progress === undefined) return [run]
    if (isTerminal(progress.state)) return []
    const line = progress.text.length > 0 ? progress.text[progress.text.length - 1] : run.line
    const cost = progress.cost === undefined ? run.cost : progress.cost
    return [{ ...run, from: progress.next, line, cost }]
  })
}
