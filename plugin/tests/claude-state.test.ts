import { expect, test } from 'claude-code/testing'
import { createClaudeState } from '../hooks/claude-state.js'
import { apiError, answered, emptyReply, NO_USAGE } from './kit'

const BIN = '/plugin/bin/msgate'
const T0 = Date.UTC(2026, 9, 23, 12, 0, 0)
const MINUTE = 60_000
const iso = (ms: number) => new Date(ms).toISOString()

// ports answers what the engine would: the usage the session reports, the
// clock, and the commands run, kept as argv.
function ports(over: Record<string, unknown> = {}) {
  const seen = { argvs: [] as string[][], timeouts: [] as (number | undefined)[], logs: [] as string[], usageReads: 0 }
  const p = {
    seen,
    clock: T0,
    limits: [] as { kind: string; percentUsed: number; resetsAt?: string }[],
    reply: { exitCode: 0, stdout: '', stderr: '' } as { exitCode: number; stdout: string; stderr: string } | Error,
    bin: BIN,
    now: async () => p.clock,
    usage: async () => {
      seen.usageReads += 1
      return { rateLimits: p.limits }
    },
    run: async (argv: string[], init?: { timeoutMs?: number }) => {
      seen.argvs.push(argv)
      seen.timeouts.push(init?.timeoutMs)
      if (p.reply instanceof Error) throw p.reply
      return p.reply
    },
    log: (text: string) => {
      seen.logs.push(text)
    },
    ...over,
  }
  return p
}

test('a full seven_day window resetting days ahead marks anthropic rate-limited for one check only', async () => {
  const p = ports()
  p.limits = [{ kind: 'seven_day', percentUsed: 100, resetsAt: iso(T0 + 3 * 24 * 3_600_000) }]

  await createClaudeState().look(p)

  expect(p.seen.argvs).toEqual([[BIN, 'providers', 'mark', 'anthropic', '--reason', 'rate-limited', '--until', iso(T0 + MINUTE)]])
})

test('a five_hour window resetting within the check is marked until its reset', async () => {
  const p = ports()
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 30_000) }]

  await createClaudeState().look(p)

  expect(p.seen.argvs).toEqual([[BIN, 'providers', 'mark', 'anthropic', '--reason', 'rate-limited', '--until', iso(T0 + 30_000)]])
})

test('with several windows at 100 percent the latest reset bounds the mark, and a window below 100 is left out', async () => {
  const p = ports()
  p.limits = [
    { kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 10_000) },
    { kind: 'seven_day', percentUsed: 100, resetsAt: iso(T0 + 40_000) },
    { kind: 'spend_limit', percentUsed: 99.5, resetsAt: iso(T0 + 90 * 3_600_000) },
  ]

  await createClaudeState().look(p)

  expect(p.seen.argvs).toEqual([[BIN, 'providers', 'mark', 'anthropic', '--reason', 'rate-limited', '--until', iso(T0 + 40_000)]])
})

test('no window at 100 percent marks nothing', async () => {
  const p = ports()
  p.limits = [{ kind: 'five_hour', percentUsed: 99.9, resetsAt: iso(T0 + 3_600_000) }]

  await createClaudeState().look(p)

  expect(p.seen.argvs).toEqual([])
})

test('a second due look a minute later, with the window still full, writes a renewed mark', async () => {
  const p = ports()
  const state = createClaudeState()
  p.limits = [{ kind: 'seven_day', percentUsed: 100, resetsAt: iso(T0 + 3 * 24 * 3_600_000) }]

  await state.look(p)
  p.clock = T0 + MINUTE
  await state.look(p)

  expect(p.seen.argvs.map((argv) => argv.at(-1))).toEqual([iso(T0 + MINUTE), iso(T0 + 2 * MINUTE)])
})

test('a due look after the window drops below 100 percent writes nothing, and the mark is left to lapse', async () => {
  const p = ports()
  const state = createClaudeState()
  p.limits = [{ kind: 'seven_day', percentUsed: 100, resetsAt: iso(T0 + 3 * 24 * 3_600_000) }]

  await state.look(p)
  p.clock = T0 + MINUTE
  p.limits = [{ kind: 'seven_day', percentUsed: 60, resetsAt: iso(T0 + 3 * 24 * 3_600_000) }]
  await state.look(p)

  expect(p.seen.argvs).toHaveLength(1)
})

test('a window of a kind that is not counted, such as seven_day_opus, marks nothing at 100 percent', async () => {
  const p = ports()
  p.limits = [
    { kind: 'five_hour', percentUsed: 40, resetsAt: iso(T0 + 3_600_000) },
    { kind: 'seven_day', percentUsed: 70, resetsAt: iso(T0 + 3 * 24 * 3_600_000) },
    { kind: 'seven_day_opus', percentUsed: 100, resetsAt: iso(T0 + 3 * 24 * 3_600_000) },
  ]

  await createClaudeState().look(p)

  expect(p.seen.argvs).toEqual([])
})

test('a look whose window resets before a refusal mark runs out writes nothing over it', async () => {
  const p = ports()
  const state = createClaudeState()

  await state.refused(p, apiError(429))
  p.clock = T0 + 10_000
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 20_000) }]
  await state.look(p)

  expect(p.seen.argvs).toEqual([PLAIN_MARK])
})

test('the session looks at its windows at most once a minute', async () => {
  const p = ports()
  const state = createClaudeState()

  await state.look(p)
  p.clock = T0 + MINUTE - 1
  await state.look(p)
  expect(p.seen.usageReads).toBe(1)

  p.clock = T0 + MINUTE
  await state.look(p)
  expect(p.seen.usageReads).toBe(2)
})

test('a window at 100 percent whose reset has passed marks nothing', async () => {
  const p = ports()
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 - 1000) }]

  await createClaudeState().look(p)

  expect(p.seen.argvs).toEqual([])
})

test('a window at 100 percent with no reset is marked for one check, and again a minute later', async () => {
  const p = ports()
  const state = createClaudeState()
  p.limits = [{ kind: 'spend_limit', percentUsed: 120 }]

  await state.look(p)
  p.clock = T0 + MINUTE
  await state.look(p)

  expect(p.seen.argvs.map((argv) => argv.at(-1))).toEqual([iso(T0 + MINUTE), iso(T0 + 2 * MINUTE)])
})

test('after the mark has run out, a window still at 100 percent marks again', async () => {
  const p = ports()
  const state = createClaudeState()
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 2 * MINUTE) }]

  await state.look(p)
  p.clock = T0 + 3 * MINUTE
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 5 * 3_600_000) }]
  await state.look(p)

  expect(p.seen.argvs.map((argv) => argv.at(-1))).toEqual([iso(T0 + MINUTE), iso(T0 + 4 * MINUTE)])
})

// A completion refused for want of rate-limit room marks Claude down until the next check.
const PLAIN_MARK = [BIN, 'providers', 'mark', 'anthropic', '--reason', 'rate-limited']

const RATE_LIMITED: [string, unknown][] = [
  ['an api error with status 429', apiError(429)],
  ['an api error classed rate_limit with no status', { ...apiError(null), error: 'rate_limit' }],
]
for (const [name, result] of RATE_LIMITED) {
  test(`${name} marks anthropic rate-limited with no time`, async () => {
    const p = ports()

    await createClaudeState().refused(p, result)

    expect(p.seen.argvs).toEqual([PLAIN_MARK])
  })
}

const NOT_RATE_LIMITED: [string, unknown][] = [
  ['an api error with status 500', apiError(500)],
  ['an overloaded api error', { ...apiError(529), error: 'overloaded' }],
  ['an api error with no status', apiError(null)],
  ['an empty reply', emptyReply()],
  ['an aborted completion', { isAnswered: false, reason: 'aborted', usage: NO_USAGE }],
  ['an answer', answered('Reading auth.go')],
]
for (const [name, result] of NOT_RATE_LIMITED) {
  test(`${name} marks nothing`, async () => {
    const p = ports()

    await createClaudeState().refused(p, result)

    expect(p.seen.argvs).toEqual([])
  })
}

test('a run of refusals writes one mark a minute, and none while a mark with a time holds', async () => {
  const p = ports()
  const state = createClaudeState()

  await state.refused(p, apiError(429))
  p.clock = T0 + MINUTE - 1
  await state.refused(p, apiError(429))
  expect(p.seen.argvs).toEqual([PLAIN_MARK])

  p.clock = T0 + MINUTE
  await state.refused(p, apiError(429))
  expect(p.seen.argvs).toEqual([PLAIN_MARK, PLAIN_MARK])

  p.clock = T0 + 2 * MINUTE
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 3 * 3_600_000) }]
  await state.look(p)
  p.clock = T0 + 3 * MINUTE - 1
  await state.refused(p, apiError(429))
  expect(p.seen.argvs).toHaveLength(3)
})

// A mark that cannot be written changes nothing else.

const FULL_WINDOW = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 3 * 3_600_000) }]
const BROKEN: [string, Record<string, unknown>][] = [
  ['exits non-zero', { reply: { exitCode: 7, stdout: '', stderr: 'msgate: cannot write the cache' } }],
  ['cannot be run', { reply: new Error('spawn failed') }],
]
for (const [name, over] of BROKEN) {
  test(`a mark whose command ${name} is logged once, throws nothing and is tried again a minute later`, async () => {
    const p = ports(over)
    const state = createClaudeState()
    p.limits = FULL_WINDOW

    await state.look(p)
    await state.refused(p, apiError(429))
    expect(p.seen.argvs).toHaveLength(1)
    expect(p.seen.logs).toHaveLength(1)

    p.clock = T0 + MINUTE
    await state.look(p)
    await state.refused(p, apiError(429))
    expect(p.seen.argvs).toHaveLength(2)
    expect(p.seen.logs).toHaveLength(1)
  })
}

test('a mark that failed is not held as written: the next look writes it once the command works', async () => {
  const p = ports({ reply: new Error('spawn failed') })
  const state = createClaudeState()
  p.limits = FULL_WINDOW

  await state.look(p)
  p.reply = { exitCode: 0, stdout: '', stderr: '' }
  p.clock = T0 + MINUTE
  await state.look(p)

  expect(p.seen.argvs).toHaveLength(2)
})

test('windows that cannot be read are logged once and mark nothing', async () => {
  const p = ports({
    usage: async () => {
      throw new Error('usage is unavailable')
    },
  })
  const state = createClaudeState()

  await state.look(p)
  p.clock = T0 + MINUTE
  await state.look(p)

  expect(p.seen.argvs).toEqual([])
  expect(p.seen.logs).toHaveLength(1)
})

test('every mark is run with the command timeout, so a caller waits no longer than that', async () => {
  const p = ports()
  p.limits = FULL_WINDOW

  await createClaudeState().look(p)
  await createClaudeState().refused(p, apiError(429))

  expect(p.seen.timeouts).toEqual([5000, 5000])
})

// Two writers at once: a look at the windows and a refused completion.

const TIMED_MARK = [BIN, 'providers', 'mark', 'anthropic', '--reason', 'rate-limited', '--until', iso(T0 + MINUTE)]

// slow is a command that takes a while to finish, so that a second writer
// starting meanwhile overlaps with it.
function slow(p: ReturnType<typeof ports>) {
  return async (argv: string[], init?: { timeoutMs?: number }) => {
    p.seen.argvs.push(argv)
    p.seen.timeouts.push(init?.timeoutMs)
    await new Promise((resolve) => setTimeout(resolve, 5))
    return { exitCode: 0, stdout: '', stderr: '' }
  }
}

test('a look at 100 percent and a 429 refusal started together leave the timed mark written, and the untimed one not written', async () => {
  const p = ports()
  p.run = slow(p)
  p.limits = FULL_WINDOW
  const state = createClaudeState()

  await Promise.all([state.look(p), state.refused(p, apiError(429))])

  expect(p.seen.argvs).toEqual([TIMED_MARK])
})

test('a refusal mark written first is replaced by the timed mark of a look whose window time is later', async () => {
  const p = ports()
  p.run = slow(p)
  p.limits = FULL_WINDOW
  const times = [T0, T0 + 30_000]
  p.now = async () => times.shift() as number
  const state = createClaudeState()

  await Promise.all([state.refused(p, apiError(429)), state.look(p)])

  expect(p.seen.argvs.at(-1)).toEqual([...TIMED_MARK.slice(0, -1), iso(T0 + 30_000 + MINUTE)])
  expect(p.seen.argvs.filter((argv) => !argv.includes('--until')).length).toBeLessThanOrEqual(1)
})

test('marks are written one at a time, in the order the writers started', async () => {
  const p = ports()
  let running = 0
  let most = 0
  p.run = async (argv: string[]) => {
    p.seen.argvs.push(argv)
    running += 1
    most = Math.max(most, running)
    await new Promise((resolve) => setTimeout(resolve, 5))
    running -= 1
    return { exitCode: 0, stdout: '', stderr: '' }
  }
  p.limits = FULL_WINDOW
  const state = createClaudeState()

  await Promise.all([state.look(p), state.refused(p, apiError(429)), state.refused(p, apiError(429))])

  expect(most).toBe(1)
})

test('a mark that could not be renewed makes a refusal wait for the next check instead of writing an untimed one', async () => {
  const p = ports()
  const state = createClaudeState()
  p.limits = FULL_WINDOW

  await state.look(p)
  p.clock = T0 + MINUTE
  p.limits = [{ kind: 'five_hour', percentUsed: 100, resetsAt: iso(T0 + 5 * 3_600_000) }]
  p.reply = new Error('spawn failed')
  await state.look(p)
  p.clock = T0 + MINUTE + 30_000
  p.reply = { exitCode: 0, stdout: '', stderr: '' }
  await state.refused(p, apiError(429))

  expect(p.seen.argvs.filter((argv) => !argv.includes('--until'))).toEqual([])
})
