import { expect, test } from 'claude-code/testing'
import { createModelsCache, serveModels } from '../hooks/iface.js'
import { engineOf, isShim, ok, world } from './kit'

// The interface's models method over the world, with the engine's clock beneath
// it; a fresh cache stands for the module loading again.
function modelsAs(w: ReturnType<typeof world>, catalogs = createModelsCache()) {
  const $ = { ...engineOf(w), clock: { now: async () => w.clock.now() } }
  return { models: (args?: unknown) => serveModels($, args, catalogs) }
}

// What `providers --json` prints for the providers named, and the catalog for `models`.
const entry = (over: Record<string, unknown> = {}) => ({ provider: 'codex', available: true, reason: '', checked_at: '2026-10-23T10:00:00Z', resets_at: null, source: 'check', ...over })
const providersJson = (...entries: unknown[]) => ok(JSON.stringify({ providers: entries, summary_models: [] }))
const command = (argv: string[]) => argv[1]
const commandsRun = (w: ReturnType<typeof world>) => w.runs.map((r) => r.argv.slice(1))
const catalogReads = (w: ReturnType<typeof world>) => commandsRun(w).filter((argv) => argv[0] === 'models')
// A world whose listing names codex available and whose other commands answer as given.
const serving = (w: ReturnType<typeof world>, answer: (argv: string[]) => any) => {
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry()) : answer(argv))
}

const CATALOG = {
  provider: 'codex',
  models: [
    { id: 'gpt-6-astra', name: 'GPT 6 Astra', visibility: 'list' },
    { id: 'gpt-6-luna', name: 'GPT 6 Luna', visibility: 'hide' },
  ],
}

test('an available provider answers the catalog the engine\'s binary prints, that it is available and when that was checked', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry()) : ok(JSON.stringify(CATALOG)))

  const out = await modelsAs(w).models()

  expect(out).toEqual({ value: { ...CATALOG, available: true, checked_at: '2026-10-23T10:00:00Z' } })
  expect(w.runs.every((r) => isShim(r.argv[0]!))).toBe(true)
  expect(commandsRun(w)).toEqual([['providers', '--json'], ['models', '--json']])
})

test('the catalog is read once per load', async ($, on) => {
  const w = world(on)
  serving(w, () => ok(JSON.stringify(CATALOG)))
  const msgate = modelsAs(w)

  await msgate.models()
  await msgate.models()
  await Promise.all([msgate.models(), msgate.models()])

  expect(catalogReads(w)).toEqual([['models', '--json']])
})

test('models asks the binary for the provider it was given, and keeps one catalog per provider', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry(), entry({ provider: 'other' })) : ok(JSON.stringify({ ...CATALOG, provider: argv.includes('--provider') ? argv[argv.indexOf('--provider') + 1] : 'codex' })))
  const msgate = modelsAs(w)

  const named: any = await msgate.models({ provider: 'other' })
  const again: any = await msgate.models({ provider: 'other' })
  const fallback: any = await msgate.models({})

  expect(named.value.provider).toBe('other')
  expect(again).toEqual(named)
  expect(fallback.value.provider).toBe('codex')
  expect(catalogReads(w)).toEqual([
    ['models', '--json', '--provider', 'other'],
    ['models', '--json'],
  ])
})

const catalogOf = (over: Record<string, unknown>) => ok(JSON.stringify({ ...CATALOG, ...over }))
const modelOf = (over: Record<string, unknown>) => catalogOf({ models: [{ ...CATALOG.models[0], ...over }] })

const FAILING: [string, () => any, string][] = [
  ['exits non-zero', () => ({ exitCode: 1, stdout: '', stderr: 'msgate: codex could not list models\n' }), 'codex could not list models'],
  ['prints something that is not JSON', () => ok('gpt-6-astra\tGPT 6 Astra\tlist'), 'JSON'],
  ['prints no provider', () => catalogOf({ provider: undefined }), 'provider'],
  ['prints an empty provider', () => catalogOf({ provider: '' }), 'provider'],
  ['prints models that are not a list', () => catalogOf({ models: 'gpt-6-astra' }), 'models'],
  ['prints a model that is not an object', () => catalogOf({ models: ['gpt-6-astra'] }), 'model'],
  ['prints a model without an id', () => modelOf({ id: undefined }), 'id'],
  ['prints a model with an empty id', () => modelOf({ id: '' }), 'id'],
  ['prints a model whose name is not text', () => modelOf({ name: 3 }), 'name'],
  ['prints a model whose visibility is not text', () => modelOf({ visibility: null }), 'visibility'],
  ['cannot be run', () => Promise.reject(new Error('spawn failed')), 'spawn failed'],
]

for (const [name, failing, why] of FAILING) {
  test(`models is refused as msgate:models:failed when the binary ${name}, says why in the debug log, and is read again by the next call`, async ($, on) => {
    const w = world(on)
    serving(w, failing)
    const msgate = modelsAs(w)

    expect(await msgate.models()).toEqual({ deny: 'msgate:models:failed' })
    expect(w.debugLogs.join('\n')).toContain(why)

    serving(w, () => ok(JSON.stringify(CATALOG)))
    expect(await msgate.models()).toEqual({ value: { ...CATALOG, available: true, checked_at: '2026-10-23T10:00:00Z' } })
  })
}

test('models refuses a provider that is not text without running the binary', async ($, on) => {
  const w = world(on)

  expect(await modelsAs(w).models({ provider: 7 })).toEqual({ deny: 'msgate:models:failed' })
  expect(await modelsAs(w).models({ provider: '' })).toEqual({ deny: 'msgate:models:failed' })
  expect(w.runs).toEqual([])
})

test('a module that loads again reads the catalog again', async ($, on) => {
  const w = world(on)
  serving(w, () => ok(JSON.stringify(CATALOG)))

  await modelsAs(w).models()
  await modelsAs(w).models()

  expect(catalogReads(w).length).toBe(2)
})

test('an unavailable provider answers no models, why it is down and when that was checked, and its catalog is not read', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry({ available: false, reason: 'rate-limited' })) : ok(JSON.stringify(CATALOG)))

  const out = await modelsAs(w).models()

  expect(out).toEqual({ value: { provider: 'codex', models: [], available: false, reason: 'rate-limited', checked_at: '2026-10-23T10:00:00Z' } })
  expect(commandsRun(w)).toEqual([['providers', '--json']])
})

const UNREADABLE: [string, () => any][] = [
  ['exits non-zero', () => ({ exitCode: 1, stdout: '', stderr: 'msgate: no home\n' })],
  ['prints something that is not JSON', () => ok('codex\tavailable')],
  ['prints no list of providers', () => ok(JSON.stringify({ summary_models: [] }))],
  ['cannot be run', () => Promise.reject(new Error('spawn failed'))],
]

for (const [name, unreadable] of UNREADABLE) {
  test(`a providers listing that ${name} leaves the provider available, with no checked_at`, async ($, on) => {
    const w = world(on)
    w.respond = (argv) => (command(argv) === 'providers' ? unreadable() : ok(JSON.stringify(CATALOG)))

    expect(await modelsAs(w).models()).toEqual({ value: { ...CATALOG, available: true } })
  })
}

test('a provider the listing does not name, or has never checked, is available', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry({ provider: 'anthropic', available: false, reason: 'rate-limited' }), entry({ checked_at: null, source: 'none' })) : ok(JSON.stringify(CATALOG)))

  expect(await modelsAs(w).models()).toEqual({ value: { ...CATALOG, available: true } })
})

test('a provider the listing names as not installed answers not-installed', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry({ available: false, reason: 'not-installed' })) : ok(JSON.stringify(CATALOG)))

  expect(await modelsAs(w).models()).toEqual({ value: { provider: 'codex', models: [], available: false, reason: 'not-installed', checked_at: '2026-10-23T10:00:00Z' } })
})

test('a catalog read that finds the binary missing answers not-installed as of that read, where the listing has no entry yet', async ($, on) => {
  const w = world(on)
  await w.clock.set(Date.parse('2026-10-23T12:00:00Z'))
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson() : { exitCode: 127, stdout: '', stderr: 'msgate: codex is not on PATH\n' })

  expect(await modelsAs(w).models()).toEqual({ value: { provider: 'codex', models: [], available: false, reason: 'not-installed', checked_at: '2026-10-23T12:00:00.000Z' } })
})

const FIVE_MINUTES = 5 * 60_000
const ONE_MINUTE = 60_000
const providerReads = (w: ReturnType<typeof world>) => commandsRun(w).filter((argv) => argv[0] === 'providers')

test('an available provider is read once for the calls made within five minutes, including calls made together, and again after them', async ($, on) => {
  const w = world(on)
  serving(w, () => ok(JSON.stringify(CATALOG)))
  const msgate = modelsAs(w)

  await Promise.all([msgate.models(), msgate.models()])
  await w.clock.advance(FIVE_MINUTES - 1)
  await msgate.models()
  expect(providerReads(w).length).toBe(1)

  await w.clock.advance(1)
  await msgate.models()
  expect(providerReads(w).length).toBe(2)
})

test('availability that changes within a load shows up on the first call after the held answer is five minutes old', async ($, on) => {
  const w = world(on)
  let down = false
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry({ available: !down, reason: down ? 'quota-exhausted' : '' })) : ok(JSON.stringify(CATALOG)))
  const msgate = modelsAs(w)

  expect(((await msgate.models()) as any).value.available).toBe(true)
  down = true
  await w.clock.advance(FIVE_MINUTES - 1)
  expect(((await msgate.models()) as any).value.available).toBe(true)

  await w.clock.advance(1)
  const after: any = await msgate.models()
  expect(after.value.available).toBe(false)
  expect(after.value.reason).toBe('quota-exhausted')
  expect(after.value.models).toEqual([])
})

test('an unavailable provider is read again after one minute, and shows up as available again', async ($, on) => {
  const w = world(on)
  let down = true
  w.respond = (argv) => (command(argv) === 'providers' ? providersJson(entry({ available: !down, reason: down ? 'rate-limited' : '' })) : ok(JSON.stringify(CATALOG)))
  const msgate = modelsAs(w)

  expect(((await msgate.models()) as any).value.available).toBe(false)
  down = false
  await w.clock.advance(ONE_MINUTE - 1)
  expect(((await msgate.models()) as any).value.available).toBe(false)
  expect(providerReads(w).length).toBe(1)

  await w.clock.advance(1)
  const after: any = await msgate.models()
  expect(after.value.available).toBe(true)
  expect(after.value.models.length).toBe(2)
  expect(providerReads(w).length).toBe(2)
})

test('a listing that could not be read is not held: the next call reads it again', async ($, on) => {
  const w = world(on)
  let readable = false
  w.respond = (argv) => (command(argv) === 'providers' ? (readable ? providersJson(entry({ available: false, reason: 'rate-limited' })) : { exitCode: 1, stdout: '', stderr: 'msgate: busy\n' }) : ok(JSON.stringify(CATALOG)))
  const msgate = modelsAs(w)

  expect(((await msgate.models()) as any).value.available).toBe(true)
  readable = true
  expect(((await msgate.models()) as any).value.available).toBe(false)
})
