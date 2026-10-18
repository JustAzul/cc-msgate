// The requests a session may have out at once, shared by the live summaries and
// the recorded ones: a few in flight, and none for a while after a 429.

import { RATE_LIMIT_PAUSE_MS, SESSION_IN_FLIGHT_MAX } from './summary.js'

export function createLimiter() {
  let inFlight = 0
  let pausedUntil = 0
  let waiters = []

  // tryTake claims a slot at `now`, or answers false when the cap is reached or
  // the limiter is paused. The check and the claim are one step, so a caller
  // that takes it after its last await cannot lose a slot to another.
  function tryTake(now) {
    if (inFlight >= SESSION_IN_FLIGHT_MAX || now < pausedUntil) return false
    inFlight += 1
    return true
  }

  function release() {
    inFlight -= 1
    const woken = waiters
    waiters = []
    for (const wake of woken) wake()
  }

  // freed answers when a slot is next released.
  function freed() {
    return new Promise((resolve) => waiters.push(resolve))
  }

  // pausedFor is how long a pause still holds at `now`, or 0.
  function pausedFor(now) {
    return Math.max(0, pausedUntil - now)
  }

  // pause stops every request for RATE_LIMIT_PAUSE_MS from `now`.
  function pause(now) {
    pausedUntil = now + RATE_LIMIT_PAUSE_MS
  }

  return { tryTake, release, freed, pause, pausedFor }
}
