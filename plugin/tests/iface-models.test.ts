import { expect, test } from 'claude-code/testing'
import { isShim, msgateAs, ok, world } from './kit'

const CATALOG = {
  provider: 'codex',
  models: [
    { id: 'gpt-6-astra', name: 'GPT 6 Astra', visibility: 'list' },
    { id: 'gpt-6-luna', name: 'GPT 6 Luna', visibility: 'hide' },
  ],
}

test('models resolves the catalog the engine\'s binary prints', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(CATALOG))

  const out = await msgateAs(w).models()

  expect(out).toEqual({ value: CATALOG })
  expect(isShim(w.runs[0]!.argv[0]!)).toBe(true)
  expect(w.runs[0]!.argv.slice(1)).toEqual(['models', '--json'])
})

test('the catalog is read once per load', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(CATALOG))
  const msgate = msgateAs(w)

  await msgate.models()
  await msgate.models()
  await Promise.all([msgate.models(), msgate.models()])

  expect(w.runs.length).toBe(1)
})

test('models asks the binary for the provider it was given, and keeps one catalog per provider', async ($, on) => {
  const w = world(on)
  w.respond = (argv) => ok(JSON.stringify({ ...CATALOG, provider: argv.includes('--provider') ? argv[argv.indexOf('--provider') + 1] : 'codex' }))
  const msgate = msgateAs(w)

  const named: any = await msgate.models({ provider: 'other' })
  const again: any = await msgate.models({ provider: 'other' })
  const fallback: any = await msgate.models({})

  expect(named.value.provider).toBe('other')
  expect(again).toEqual(named)
  expect(fallback.value.provider).toBe('codex')
  expect(w.runs.map((r) => r.argv.slice(1))).toEqual([
    ['models', '--json', '--provider', 'other'],
    ['models', '--json'],
  ])
})

const catalogOf = (over: Record<string, unknown>) => ok(JSON.stringify({ ...CATALOG, ...over }))
const modelOf = (over: Record<string, unknown>) => catalogOf({ models: [{ ...CATALOG.models[0], ...over }] })

const FAILING: [string, () => any, string][] = [
  ['exits non-zero', () => ({ exitCode: 127, stdout: '', stderr: 'msgate: codex is not on PATH\n' }), 'codex is not on PATH'],
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
    w.respond = failing
    const msgate = msgateAs(w)

    expect(await msgate.models()).toEqual({ deny: 'msgate:models:failed' })
    expect(w.debugLogs.join('\n')).toContain(why)

    w.respond = () => ok(JSON.stringify(CATALOG))
    expect(await msgate.models()).toEqual({ value: CATALOG })
  })
}

test('models refuses a provider that is not text without running the binary', async ($, on) => {
  const w = world(on)

  expect(await msgateAs(w).models({ provider: 7 })).toEqual({ deny: 'msgate:models:failed' })
  expect(await msgateAs(w).models({ provider: '' })).toEqual({ deny: 'msgate:models:failed' })
  expect(w.runs).toEqual([])
})

test('a module that loads again reads the catalog again', async ($, on) => {
  const w = world(on)
  w.respond = () => ok(JSON.stringify(CATALOG))

  await msgateAs(w).models()
  await msgateAs(w).models()

  expect(w.runs.length).toBe(2)
})
