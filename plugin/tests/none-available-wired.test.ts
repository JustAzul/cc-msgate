import { expect, test } from 'claude-code/testing'
import { BAND_REFRESH_MS } from '../hooks/band.js'
import { AGENTS, POLL_MS, SESSION, WAIT_TIMEOUT, answered, job, listing, msg, ok, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const ROW_TYPE = AGENTS + ':run'
const PLAIN = { [ROW_TYPE]: { command: 'follow', promptMode: 'run-id' } }
const STEPPED = { [ROW_TYPE]: { command: 'follow', promptMode: 'run-id', stepped: true } }
const SPINNER_PROPS = { word: 'Sauteing', message: null, suffix: '…', mode: 'requesting' } as any
const WORD_LINE = /^» [A-Za-z]+…$/
// Every provider the plugin ranks is down.
const NONE = JSON.stringify({
  providers: [
    { provider: 'anthropic', available: false },
    { provider: 'codex', available: false },
  ],
  summary_models: [],
})
const isListing = (argv: string[]) => argv[1] === 'providers' && argv[2] === '--json'

const finished = () =>
  job({ run_id: 'run-hook', source: 'hook-stop', purpose: 'code-review', background: false, session_id: SESSION, agent_feedback: true, state: 'done', outcome: 'ok', started_at: '1970-01-01T00:00:00Z' })

// A hook's run that works through `slices`: each wait times out and the progress
// read after it answers the next slice's entries, "entry N" messages numbered
// from 1; the wait after the last slice finds the run done. No provider can be asked.
function follower(slices: number[]) {
  let waits = 0
  let reads = 0
  let total = 0
  return (argv: string[]) => {
    if (isListing(argv)) return ok(NONE)
    switch (argv[1]) {
      case 'wait':
        return ++waits <= slices.length ? WAIT_TIMEOUT : ok(JSON.stringify(finished()))
      case 'progress': {
        const count = slices[reads++] ?? 0
        const entries = Array.from({ length: count }, (_, i) => ({ seq: total + i, at: 't', kind: 'message', text: 'entry ' + (total + i + 1) }))
        total += count
        return ok(JSON.stringify({ run_id: 'run-hook', state: 'running', next: total, entries }))
      }
      case 'status':
        return ok(JSON.stringify(finished()))
      default:
        return ok('')
    }
  }
}

async function openRow($: any, w: ReturnType<typeof world>, slices: number[]) {
  w.surfaces = ['terminal']
  w.respond = follower(slices)
  w.agents.push({ id: 'ag-hidden', type: ROW_TYPE })
  w.messages['ag-hidden'] = [{ role: 'user', text: 'msgate run run-hook' }]
  w.state.set('hiddenRows', { value: { 'ag-hidden': 'run-hook' }, version: 1 })
  await $.session.start(START)
}

const stepAt = ($: any, index: number) => step($, 'ag-hidden', { turnId: 'turn-1', index })
const textsOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'text').map((c) => c.text)
const thinkingOf = (chunks: any[]) => chunks.filter((c) => c.kind === 'thinking').map((c) => c.text.trim())

test('a stepped row followed with no provider available keeps no random word in its history, and each step closes on the proxy line of its window', { options: { recordedSummaryEvery: 3 } }, async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])

  const steps = [await stepAt($, 0), await stepAt($, 1), await stepAt($, 2)]
  const last = await stepAt($, 3)

  expect(w.modelRequests).toEqual([])
  expect(steps.map((out) => textsOf(out.chunks))).toEqual([['» 1–3: entry 3\n'], ['» 4–6: entry 6\n'], ['» 7–9: entry 9\n']])
  expect(textsOf(last.chunks)).toEqual(['msgate run run-hook ended ok'])
  for (const out of [...steps, last]) expect(out.result.answer).not.toMatch(/» [A-Za-z]+…/)
})

test('at the default cadences every step that closes before the run ends carries the proxy line of its own entries', async ($, on) => {
  const w = world(on, { backed: STEPPED })
  await openRow($, w, [3, 3, 3])

  const steps = [await stepAt($, 0), await stepAt($, 1), await stepAt($, 2)]
  const last = await stepAt($, 3)

  expect(w.modelRequests).toEqual([])
  expect(steps.map((out) => textsOf(out.chunks))).toEqual([['» 1–3: entry 3\n'], ['» 4–6: entry 6\n'], ['» 7–9: entry 9\n']])
  expect(textsOf(last.chunks)).toEqual(['msgate run run-hook ended ok'])
  for (const out of [...steps, last]) expect(out.result.answer).not.toMatch(/» [A-Za-z]+…/)
})

test('a row followed with no provider available shows a random word as its thinking, with no model call', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on, { backed: PLAIN })
  await openRow($, w, [3, 3])

  const out = await step($, 'ag-hidden')

  expect(w.modelRequests).toEqual([])
  const words = thinkingOf(out.chunks).filter((line) => WORD_LINE.test(line))
  expect(words.length).toBeGreaterThan(0)
  expect(thinkingOf(out.chunks).every((line) => WORD_LINE.test(line) || line.startsWith('msgate'))).toBe(true)
})

// A turn of agent `ag-1` that stands still at its second wait, with one entry read.
function turnAtSecondWait($: any, w: any) {
  let release!: () => void
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  let isAtSecondWait!: () => void
  const atSecondWait = new Promise<void>((resolve) => {
    isAtSecondWait = resolve
  })
  let waits = 0
  w.respond = async (argv: string[]) => {
    if (isListing(argv)) return ok(NONE)
    switch (argv[1]) {
      case 'exec':
        return ok(JSON.stringify({ conversation_id: 'conv-1', run_id: 'run-1' }))
      case 'wait':
        if (++waits === 1) return WAIT_TIMEOUT
        isAtSecondWait()
        await held
        return ok(JSON.stringify({ run_id: 'run-1', state: 'done', outcome: 'ok' }))
      case 'progress':
        return ok(JSON.stringify({ run_id: 'run-1', state: 'running', next: 1, entries: waits === 1 ? [{ seq: 0, at: 't', kind: 'message', text: 'I will read auth.go' }] : [] }))
      case 'status':
        return ok(JSON.stringify({ run_id: 'run-1', conversation_id: 'conv-1', state: 'done', outcome: 'ok', output_path: '/o' }))
      default:
        return ok('Codex: done.')
    }
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  return { turn: $.session.start(START).then(() => step($, 'ag-1')), atSecondWait, release }
}

test('the spinner of an agent says a random word while no provider is available, and draws again when it comes', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = turnAtSecondWait($, w)
  await run.atSecondWait

  const ui = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'Spinner', props: SPINNER_PROPS, requestId: 'ag-1' })
  const texts = (await ui.findAll({ type: 'Text' })).map((t: any) => t.text)
  await ui.unmount()

  expect(texts).toHaveLength(1)
  expect(texts[0]).toMatch(WORD_LINE)
  expect(w.invalidations).toEqual(['ui.render'])
  expect(w.modelRequests).toEqual([])
  run.release()
  await run.turn
})

const BAND_PROPS = { hasSurvey: false, isWorking: false, maxRows: 10, bodyColumns: 200, scroll: { offset: 0, bodyRows: 9 } } as any

async function bandText($: any) {
  const band = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'AbovePrompt', props: BAND_PROPS })
  return (await band.findAll({ type: 'Text' })).map((t: any) => t.text)
}

test('the band of a run whose label gave way to a random word shows the newest raw step, never the old label or the word', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => answered('Reading auth.go')
  const entries: { kind: string; text: string }[] = [msg('e1')]
  let isDown = false
  w.respond = (argv: string[]) => {
    if (isListing(argv)) return ok(isDown ? NONE : JSON.stringify({ providers: [], summary_models: [] }))
    if (argv[1] === 'progress') {
      const from = Number(argv[4])
      return ok(JSON.stringify({ run_id: 'run-h', state: 'running', next: entries.length, entries: entries.slice(from).map((e, i) => ({ seq: from + i, at: 't', ...e })) }))
    }
    if (argv[1] === 'stats') return ok(JSON.stringify({ usage_totals: {} }))
    return listing(job({ run_id: 'run-h', state: 'running', source: 'hook-post-commit', purpose: 'code-review', background: false, agent_feedback: true, started_at: '1970-01-01T00:00:00Z' }))
  }
  await $.session.start(START)
  await w.clock.advance(POLL_MS)
  await w.clock.advance(BAND_REFRESH_MS)
  expect(await bandText($)).toEqual(['msgate · code-review · hook-post-commit · 20s · Reading auth.go'])

  // The providers listing is held for five minutes.
  isDown = true
  await w.clock.advance(6 * 60000)
  entries.push(msg('e2'), msg('e3'), msg('e4'))
  await w.clock.advance(5 * BAND_REFRESH_MS)

  expect(w.modelRequests).toHaveLength(1)
  const text = (await bandText($)).join(' ')
  expect(text).toContain('e4')
  expect(text).not.toContain('Reading auth.go')
  expect(text).not.toMatch(/» [A-Za-z]+…/)
})
