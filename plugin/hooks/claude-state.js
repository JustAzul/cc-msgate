// Claude's own state, written into the cache every session reads: the plugin
// sees Claude's rate-limit windows and its refused completions, and tells the
// binary with `providers mark anthropic`. The engine comes in through ports,
// so the decisions can be read and tested on their own.

import { failureText, messageOf } from './lib.js'

const FULL = 100
// How long a caller waits on a mark: the command's own timeout.
const MARK_TIMEOUT_MS = 5000

// markCommand marks Claude unavailable as rate-limited, until `untilMs` when
// given, else until the next check.
export function markCommand(bin, untilMs) {
  const argv = [bin, 'providers', 'mark', 'anthropic', '--reason', 'rate-limited']
  if (untilMs !== undefined) argv.push('--until', new Date(untilMs).toISOString())
  return argv
}

// A mark with no time holds until the next check, which is a minute.
const CHECK_MS = 60000

// exhausted tells the windows at 100 percent: { isFull } when none is, else
// { isFull, until } with the latest reset among them in milliseconds, absent
// when none of them says when.
function exhausted(rateLimits) {
  const full = rateLimits.filter((limit) => limit.percentUsed >= FULL)
  const resets = full.map((limit) => Date.parse(limit.resetsAt)).filter(Number.isFinite)
  return { isFull: full.length > 0, until: resets.length === 0 ? undefined : Math.max(...resets) }
}

// isRateLimited tells a completion the API refused with a 429 or the kind
// rate_limit; any other refusal says nothing of Claude's room.
function isRateLimited(result) {
  return Boolean(result) && result.isAnswered === false && result.reason === 'api-error' && (result.status === 429 || result.error === 'rate_limit')
}

export function createClaudeState() {
  // When the session last read its windows, and until when the last mark
  // written holds, in milliseconds.
  let lookedAt = null
  let markedUntil = 0
  let isFailureLogged = false

  // logFailure logs the first failure, at debug level.
  function logFailure(ports, why) {
    if (isFailureLogged) return
    isFailureLogged = true
    ports.log('could not mark Claude down: ' + why)
  }

  // write runs a mark and keeps it as in force until `heldUntil` when the
  // command wrote it. It never throws.
  async function write(ports, argv, now, heldUntil) {
    try {
      const reply = await ports.run(argv, { timeoutMs: MARK_TIMEOUT_MS })
      if (reply.exitCode === 0) {
        markedUntil = heldUntil
        return
      }
      logFailure(ports, failureText(reply))
    } catch (error) {
      logFailure(ports, messageOf(error))
    }
    // Not in force: the next look tries again, and a refusal waits a check.
    markedUntil = now + CHECK_MS
  }

  // look reads the session's rate-limit windows, at most once a minute, and
  // marks Claude down when one is at 100 percent and no mark already holds.
  async function look(ports) {
    const now = await ports.now()
    if (lookedAt !== null && now - lookedAt < CHECK_MS) return
    lookedAt = now
    let windows
    try {
      windows = exhausted((await ports.usage()).rateLimits)
    } catch (error) {
      return logFailure(ports, messageOf(error))
    }
    const { isFull, until } = windows
    if (!isFull || (until !== undefined && until <= now)) return
    if (now < markedUntil && (until === undefined || until <= markedUntil)) return
    await write(ports, markCommand(ports.bin, until), now, until ?? now + CHECK_MS)
  }

  // refused marks Claude down, until the next check, when a completion came
  // back refused for want of room in the rate limit.
  async function refused(ports, result) {
    if (!isRateLimited(result)) return
    const now = await ports.now()
    if (now < markedUntil) return
    await write(ports, markCommand(ports.bin), now, now + CHECK_MS)
  }

  return { look, refused }
}
