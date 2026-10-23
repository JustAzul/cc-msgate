import { expect, test } from 'claude-code/testing'
import { SESSION, answered, cmd, codex, isShim, msg, ok, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const LIVE_ONLY = { options: { recordedSummaryEvery: 0 } }
const CODEX = { provider: 'codex', model: 'gpt-6-luna' }
const SONNET = { provider: 'anthropic', model: 'claude-sonnet-5' }
const isListing = (argv: string[]) => argv[1] === 'providers' && argv[2] === '--json'
const isUsageAdd = (argv: string[]) => argv[1] === 'usage'

// summarise starts the agent of an msgate type over `slices`, with the providers
// listing ranking `models`.
async function summarise($: any, on: any, models: unknown[], slices = [[msg('I will read auth.go'), cmd('rg -n token auth.go')], []]) {
  const w = world(on)
  const run = codex(slices)
  w.surfaces = ['terminal']
  w.respond = (argv, init) => (isListing(argv) ? ok(JSON.stringify({ providers: [], summary_models: models })) : run(argv))
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  await $.session.start(START)
  return w
}

// The children `msgate complete` is run as, answered like the CLI does: one
// piece of output, then the end of the child.
function serveComplete(on: any) {
  const spawns: any[] = []
  const usage = { input_tokens: 3349, cached_input_tokens: 2432, output_tokens: 10, reasoning_output_tokens: 4 }
  const body = JSON.stringify({ outcome: 'answered', text: 'Reading auth.go', model: 'gpt-6-luna', usage })
  on('process.spawn', async function* (_$: any, e: any) {
    spawns.push(e)
    yield { stream: 'stdout', text: body }
    return { value: { code: 0, signal: null } }
  })
  return spawns
}

test('a summary ranked to Codex runs msgate complete with the model, effort, session, run id and source, and is not recorded through usage add', LIVE_ONLY, async ($, on) => {
  const spawns = serveComplete(on)
  const w = await summarise($, on, [CODEX, SONNET])

  const out = await step($, 'ag-1')
  await w.clock.advance(0)

  expect(spawns).toHaveLength(1)
  expect(spawns[0].argv.slice(1)).toEqual([
    'complete', '--provider', 'codex', '--json',
    '--model', 'gpt-6-luna', '--effort', 'low',
    '--session-id', SESSION, '--run-id', 'run-1', '--source', 'mod-summary',
    '--timeout', '15',
  ])
  expect(isShim(spawns[0].argv[0])).toBe(true)
  expect(w.modelRequests).toEqual([])
  expect(w.runs.filter((r) => isUsageAdd(r.argv))).toEqual([])
  expect(out.chunks.filter((c: any) => c.kind === 'thinking').map((c: any) => c.text.trim())).toContain('» Reading auth.go')
})

test('a summary ranked to Claude is asked under the ranked model and recorded under it', LIVE_ONLY, async ($, on) => {
  const w = await summarise($, on, [SONNET, CODEX])
  w.respondModel = () => answered('Reading auth.go')

  await step($, 'ag-1')
  await w.clock.advance(0)

  const adds = w.runs.filter((r) => isUsageAdd(r.argv))
  expect(w.modelRequests.map((r) => r.model)).toEqual(['claude-sonnet-5'])
  expect(adds).toHaveLength(1)
  expect(adds[0]!.argv).toContain('claude-sonnet-5')
  expect(adds[0]!.argv).not.toContain('claude-haiku-5-5')
})

test('an empty ranking asks claude-haiku-5-5, as before the ranking', LIVE_ONLY, async ($, on) => {
  const w = await summarise($, on, [])

  await step($, 'ag-1')

  expect(w.modelRequests.map((r) => r.model)).toEqual(['claude-haiku-5-5'])
})

test('one read of the providers listing serves the candidates of every request', LIVE_ONLY, async ($, on) => {
  const w = await summarise($, on, [SONNET], [[msg('e1')], [msg('e2')], [msg('e3')], [msg('e4')], []])

  await step($, 'ag-1')

  expect(w.modelRequests).toHaveLength(2)
  expect(w.runs.filter((r) => isListing(r.argv))).toHaveLength(1)
})
