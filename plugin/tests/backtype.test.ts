import { expect, test } from 'claude-code/testing'
import { CALLER, OTHER_CALLER, msgateAs, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const SECOND_OPINION = { type: 'second-opinion', command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'high', sandbox: 'read-only' }

const registry = (w: ReturnType<typeof world>) => w.state.get('backedTypes')?.value as Record<string, any> | undefined

test('backType marks the caller\'s type as backed and resolves its full name', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)

  const out = await msgateAs(w, 'msgate-agents').backType(SECOND_OPINION)

  expect(out).toEqual({ value: { type: 'msgate-agents:second-opinion' } })
  expect(registry(w)).toEqual({
    'msgate-agents:second-opinion': { command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'high', sandbox: 'read-only' },
  })
})

test('a type with no model, effort or sandbox is stored without them', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)

  await msgateAs(w).backType({ type: 'adhoc', command: 'exec', promptMode: 'free' })

  expect(registry(w)).toEqual({ 'p2:adhoc': { command: 'exec', promptMode: 'free' } })
})

test('a review type is backed with the review-target prompt mode', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)

  const out = await msgateAs(w).backType({ type: 'code-review', command: 'review', promptMode: 'review-target', sandbox: 'workspace-write' })

  expect(out).toEqual({ value: { type: 'p2:code-review' } })
  expect((registry(w))['p2:code-review']).toEqual({ command: 'review', promptMode: 'review-target', sandbox: 'workspace-write' })
})

const REFUSALS: [string, Record<string, unknown>, string][] = [
  ['a type with a space and capitals', { ...SECOND_OPINION, type: 'Bad Type' }, 'type'],
  ['an empty type', { ...SECOND_OPINION, type: '' }, 'type'],
  ['a type starting with a dash', { ...SECOND_OPINION, type: '-x' }, 'type'],
  ['a type of 65 characters', { ...SECOND_OPINION, type: 'a'.repeat(65) }, 'type'],
  ['a type that is not text', { ...SECOND_OPINION, type: 7 }, 'type'],
  ['a command that is neither exec nor review', { ...SECOND_OPINION, command: 'follow' }, 'command'],
  ['a missing command', { ...SECOND_OPINION, command: undefined }, 'command'],
  ['a review command with the free prompt mode', { ...SECOND_OPINION, command: 'review', promptMode: 'free' }, 'promptMode'],
  ['an exec command with the review-target prompt mode', { ...SECOND_OPINION, promptMode: 'review-target' }, 'promptMode'],
  ['an unknown prompt mode', { ...SECOND_OPINION, promptMode: 'run-id' }, 'promptMode'],
  ['a model that is not text', { ...SECOND_OPINION, model: 5 }, 'model'],
  ['an empty model', { ...SECOND_OPINION, model: '' }, 'model'],
  ['an effort that is not text', { ...SECOND_OPINION, effort: ['high'] }, 'effort'],
  ['a sandbox of danger-full-access', { ...SECOND_OPINION, sandbox: 'danger-full-access' }, 'sandbox'],
  ['a sandbox that is not text', { ...SECOND_OPINION, sandbox: true }, 'sandbox'],
]

for (const [name, args, field] of REFUSALS) {
  test(`backType refuses ${name} as msgate:backType:${field} and stores nothing`, async ($, on) => {
    const w = world(on, { backed: {} })
    await $.session.start(START)

    const out = await msgateAs(w).backType(args)

    expect(out).toEqual({ deny: 'msgate:backType:' + field })
    expect(registry(w)).toEqual({})
  })
}

test('backType refuses a call that carries no arguments', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)

  expect(await msgateAs(w).backType(undefined)).toEqual({ deny: 'msgate:backType:type' })
})

test('a later call for the same type replaces its data', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)
  const msgate = msgateAs(w, 'msgate-agents')

  await msgate.backType({ ...SECOND_OPINION, effort: 'high' })
  await msgate.backType({ ...SECOND_OPINION, effort: 'low', sandbox: undefined })

  expect(registry(w)).toEqual({
    'msgate-agents:second-opinion': { command: 'exec', promptMode: 'free', model: 'gpt-6.1-sol', effort: 'low' },
  })
})

test('two plugins backing the same type name keep two entries, each under its own name', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)

  await msgateAs(w).backType({ ...SECOND_OPINION, effort: 'low' })
  await msgateAs(w, OTHER_CALLER).backType({ ...SECOND_OPINION, effort: 'high' })

  const types = registry(w)
  expect(Object.keys(types).sort()).toEqual(['p2:second-opinion', 'p3:second-opinion'])
  expect(types['p2:second-opinion'].effort).toBe('low')
  expect(types['p3:second-opinion'].effort).toBe('high')
})

test('backed types stay when the engine\'s module loads again', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)
  await msgateAs(w, 'msgate-agents').backType(SECOND_OPINION)

  const reloaded = msgateAs(w, 'msgate-agents')
  await reloaded.backType({ type: 'adhoc', command: 'exec', promptMode: 'free' })

  expect(Object.keys(registry(w)).sort()).toEqual(['msgate-agents:adhoc', 'msgate-agents:second-opinion'])
})

test('six calls made together are all stored', async ($, on) => {
  const w = world(on, { backed: {} })
  await $.session.start(START)
  const msgate = msgateAs(w, 'msgate-agents')
  const types = ['second-opinion', 'code-review', 'cross-check', 'expert-persona', 'delegation', 'adhoc']

  await Promise.all(types.map((type) => msgate.backType({ type, command: 'exec', promptMode: 'free' })))

  expect(Object.keys(registry(w)).sort()).toEqual(types.map((type) => 'msgate-agents:' + type).sort())
})
