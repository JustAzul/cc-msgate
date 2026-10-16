import { expect, test } from 'claude-code/testing'
import { BAND_REFRESH_MS } from '../hooks/band.js'
import { listing, otherRuns, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const

test('the mod registers its two tools with schemas the model can use', async ($, on) => {
  const w = world(on)
  await $.session.start(START)

  expect(w.tools.map((t) => t.name).sort()).toEqual(['jobs', 'run'])
  const spec = Object.fromEntries(w.tools.map((t) => [t.name, t]))
  for (const t of w.tools) {
    expect(t.description.length).toBeGreaterThan(20)
    expect(t.inputSchema.type).toBe('object')
  }
  expect(spec.run.inputSchema.required).toEqual(['prompt'])
  expect(spec.jobs.inputSchema.required).toEqual(['action'])
  expect(spec.jobs.inputSchema.properties.action.enum).toEqual(['list', 'status', 'result', 'cancel'])
  for (const key of ['prompt', 'conversation_id', 'provider', 'purpose', 'model', 'effort', 'sandbox', 'cwd']) {
    expect(spec.run.inputSchema.properties[key].type).toBe('string')
  }
  expect(spec.jobs.inputSchema.properties.run_id.type).toBe('string')
})

test('the mod registers exactly one command, /msgate-jobs, that runs mid-turn', async ($, on) => {
  const w = world(on)
  await $.session.start(START)

  expect(w.commands.length).toBe(1)
  expect(w.commands[0]).toMatchObject({ name: 'msgate-jobs', immediate: true })
})

test('starting the session twice registers the tools again without error', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await $.session.start(START)

  expect(w.tools.length).toBe(4)
})

test('a command that is refused is logged and does not stop the session from starting', async ($, on) => {
  const w = world(on)
  w.refuseCommand = true
  w.respond = () => listing()
  await $.session.start(START)

  expect(w.tools.map((t) => t.name).sort()).toEqual(['jobs', 'run'])
  expect(w.logs.length).toBe(1)
  expect(w.logs[0]).toContain('msgate-jobs')
  // The poll is running although the command was refused: its read joins the
  // spawn read of the third period.
  await w.clock.advance(BAND_REFRESH_MS)
  expect(otherRuns(w).length).toBe(1)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(otherRuns(w).length).toBe(2)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(otherRuns(w).length).toBe(4)
})
