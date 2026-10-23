// Which model answers a summary request: the cheapest of the ranking whose
// provider is up, not paused and has a slot, with one hand-off to another
// provider after a rate-limited refusal. Nothing here touches the mods API; what
// it needs of the engine arrives as `ports`, so the decisions can be read and
// tested on their own.

import { createLimiter } from './limiter.js'
import { SUMMARY_MODEL, isBilled, isRateLimited } from './summary.js'

const ANTHROPIC = 'anthropic'
const CODEX = 'codex'
const PROVIDERS = [ANTHROPIC, CODEX]
// The one candidate when the ranking names none.
const FALLBACK = Object.freeze({ provider: ANTHROPIC, model: SUMMARY_MODEL })

const NO_USAGE = { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }
// What a call that was cancelled before it started answers.
const aborted = () => ({ isAnswered: false, reason: 'aborted', usage: { ...NO_USAGE } })

const listed = (value) => (Array.isArray(value) ? value : [])

const isRanked = (entry) => PROVIDERS.includes(entry?.provider) && typeof entry.model === 'string' && entry.model !== ''

// candidatesOf is the models a summary may use, cheapest first: the ranking of
// the providers listing with the providers it names down left out. A ranking
// that names no model of anthropic or codex, or a listing that could not be
// read (null), reads as the one fallback candidate.
export function candidatesOf(listing) {
  const down = new Set(listed(listing?.providers).filter((entry) => entry?.available === false).map((entry) => entry.provider))
  const ranked = listed(listing?.summary_models).filter(isRanked).map(({ provider, model }) => ({ provider, model }))
  return (ranked.length === 0 ? [FALLBACK] : ranked).filter((candidate) => !down.has(candidate.provider))
}

// createSummaryChoice is one load's limits and picks: a limiter for each
// provider, shared by the live summaries and the recorded ones. A candidate list
// that was left empty, because every provider is down, reads as none available
// just as paused providers do.
export function createSummaryChoice() {
  const limiters = new Map(PROVIDERS.map((provider) => [provider, createLimiter()]))

  // take claims the slot of the first candidate whose provider is not paused and
  // has one free, leaving out the provider `except`, or answers null. The check
  // and the claim are one step, so a caller that takes it after its last await
  // cannot lose a slot to another.
  function take(candidates, now, except = null) {
    for (const candidate of candidates) {
      if (candidate.provider !== except && limiters.get(candidate.provider).tryTake(now)) return { ...candidate }
    }
    return null
  }

  // isNoneAvailable tells that no candidate can be asked at `now`: each one's
  // provider is paused, or there is no candidate. A provider whose slots are all
  // taken is busy, not unavailable, and its request waits for one.
  function isNoneAvailable(candidates, now) {
    return candidates.every((candidate) => limiters.get(candidate.provider).pausedFor(now) > 0)
  }

  const release = (lease) => limiters.get(lease.provider).release()

  // wait answers when a slot of a provider the candidates name is released or a
  // pause among them ends, whichever is first; a deadline above cuts it short.
  async function wait(candidates, now, ports) {
    const used = [...new Set(candidates.map((candidate) => candidate.provider))].map((provider) => limiters.get(provider))
    const pauses = used.map((limiter) => limiter.pausedFor(now)).filter((ms) => ms > 0)
    let timer
    const ended = new Promise((resolve) => {
      timer = pauses.length > 0 ? ports.after(Math.min(...pauses), resolve) : undefined
    })
    await Promise.race([...used.map((limiter) => limiter.freed()), ended])
    timer?.cancel()
  }

  // attempt sends the request to the leased candidate, unless the call was
  // cancelled by the time it is to go. An Anthropic call that was billed is
  // recorded under its model, never waited for; the CLI records a Codex one
  // itself. A rate-limited refusal pauses that provider alone.
  async function attempt(ports, lease, request, call) {
    await call.onAttempt?.(lease.provider)
    if (call.signal.aborted) return aborted()
    const asked = { ...request, model: lease.model }
    const result =
      lease.provider === CODEX ? await ports.completeCodex(asked, { runId: call.runId, signal: call.signal }) : await ports.complete(asked)
    if (lease.provider === ANTHROPIC && isBilled(result)) void ports.record(result.usage, call.runId, lease.model).catch(() => {})
    if (isRateLimited(result)) limiters.get(lease.provider).pause(await ports.now())
    return result
  }

  // complete answers the request with the leased candidate, and hands it once to
  // a candidate of another provider when the first is refused for want of rate
  // room. It releases every slot it holds, and `call` is { runId, signal,
  // candidates, onAttempt? }: the candidates the hand-off looks through, and the
  // callback told the provider of each attempt.
  async function complete(ports, lease, request, call) {
    let first
    try {
      first = await attempt(ports, lease, request, call)
    } finally {
      release(lease)
    }
    if (!isRateLimited(first)) return first

    const now = await ports.now()
    const next = call.signal.aborted ? null : take(call.candidates, now, lease.provider)
    if (next === null) return first

    try {
      return await attempt(ports, next, request, call)
    } finally {
      release(next)
    }
  }

  return { take, wait, complete, isNoneAvailable }
}
