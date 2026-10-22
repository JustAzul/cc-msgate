// Pure helpers for the session's cost: how a cost shows in the status line and
// the band, and how the price refresh is asked for and read. Nothing here
// touches the mods API, so these functions can be read and tested on their own.

import { failureText } from './lib.js'

// A refresh at session start skips a cache checked within this long; one asked
// for after a compaction, or for a model with no price, always checks.
export const SESSION_START_MAX_AGE = '24h'
// The price list is fetched within 20 seconds and the model catalog read within
// 10, so a refresh that outlasts this one is stuck.
export const REFRESH_TIMEOUT_MS = 60000

const DECIMAL = /^(\d+)(?:\.(\d*))?$/
const CENTS_PER_DOLLAR = 100n
// The digit of the remainder past the cents at which a cost rounds up: half a
// cent and over.
const ROUND_UP_FROM = 5

// formatCost shows a cost, a decimal string of US dollars, in dollars and cents
// without ever leaving integer arithmetic: rounded half up, and `<$0.01` when
// it is above zero and below half a cent. Anything that is not a decimal string
// shows nothing (undefined).
export function formatCost(costUsd) {
  const cost = parseCost(costUsd)
  if (cost === null) return undefined
  const rounded = cost.cents + (cost.isHalfCentOrMoreLeft ? 1n : 0n)
  return rounded === 0n && cost.isAboveZero ? '<$0.01' : dollars(rounded)
}

// formatLowerBound shows a cost that is only a lower bound, part of the usage
// having no price: truncated to cents, so it never overstates, and led by `≥`.
export function formatLowerBound(costUsd) {
  const cost = parseCost(costUsd)
  return cost === null ? undefined : '≥' + dollars(cost.cents)
}

// parseCost reads a decimal string of dollars into whole cents and what is
// left past them, or null when it is not one.
function parseCost(costUsd) {
  const match = typeof costUsd === 'string' ? DECIMAL.exec(costUsd) : null
  if (match === null) return null
  const fraction = match[2] ?? ''
  const rest = fraction.slice(2)
  return {
    cents: BigInt(match[1]) * CENTS_PER_DOLLAR + BigInt(fraction.slice(0, 2).padEnd(2, '0')),
    isHalfCentOrMoreLeft: rest !== '' && Number(rest[0]) >= ROUND_UP_FROM,
    isAboveZero: /[1-9]/.test(match[1] + fraction),
  }
}

function dollars(cents) {
  return '$' + cents / CENTS_PER_DOLLAR + '.' + String(cents % CENTS_PER_DOLLAR).padStart(2, '0')
}

// The two refreshes the mod asks for: the session-start one lets a cache
// checked within a day stand; the full one always asks the source.
export const SESSION_START_REFRESH = Object.freeze({ maxAge: SESSION_START_MAX_AGE })
export const FULL_REFRESH = Object.freeze({ maxAge: null })

// refreshCommand is `prices refresh` for one of the requests above.
export function refreshCommand(bin, request) {
  const maxAge = request.maxAge === null ? [] : ['--max-age', request.maxAge]
  return [bin, 'prices', 'refresh', ...maxAge, '--json']
}

// mergeRefreshRequests is the one refresh a queued request and a new one come
// to: it may skip a fresh cache only if every request it stands for may.
export function mergeRefreshRequests(queued, request) {
  if (queued === null) return request
  return queued.maxAge !== null && request.maxAge !== null ? queued : FULL_REFRESH
}

// isRefreshedByCompaction tells a compaction of the main conversation, by the
// person or by the engine at its threshold, from one that is not: a plugin's,
// the ahead-of-time one, and a subagent's own.
export function isRefreshedByCompaction(e) {
  return (e.trigger === 'manual' || e.trigger === 'auto') && e.agentId === undefined
}

// refreshOutcome reads the reply of `prices refresh`: why it failed (null when
// it did not), and whether it changed the cache. A refresh that had nothing to
// do (fresh, busy, off) did not fail.
export function refreshOutcome(reply) {
  if (reply.exitCode !== 0) return { failure: failureText(reply), hasChanged: false }
  try {
    const report = JSON.parse(reply.stdout)
    if (isRefreshReport(report)) return { failure: null, hasChanged: report.ran && report.changed }
  } catch {
    // Reported below as unexpected output.
  }
  return { failure: 'msgate printed an unexpected refresh report: ' + String(reply.stdout).trim().slice(0, 200), hasChanged: false }
}

// isRefreshReport tells the report `prices refresh --json` prints from anything
// else: an object whose ran and changed are booleans and whose reason is text.
function isRefreshReport(report) {
  return (
    report !== null &&
    typeof report === 'object' &&
    !Array.isArray(report) &&
    typeof report.ran === 'boolean' &&
    typeof report.changed === 'boolean' &&
    typeof report.reason === 'string'
  )
}
