import { expect, test } from 'claude-code/testing'
import { isShim, msgateAs, ok, world } from './kit'

const versionJson = (version: unknown) => ok(JSON.stringify({ version, source_commit: 'abc', build_seq: 42, platform: 'linux/amd64' }))

test('version resolves to the release version the engine\'s binary reports', async ($, on) => {
  const w = world(on)
  w.respond = () => versionJson('1.0.0')

  const out = await msgateAs(w).version()

  expect(out).toEqual({ value: '1.0.0' })
  expect(isShim(w.runs[0]!.argv[0]!)).toBe(true)
  expect(w.runs[0]!.argv.slice(1)).toEqual(['version', '--json'])
})

test('the version is read once per load', async ($, on) => {
  const w = world(on)
  w.respond = () => versionJson('1.0.0')
  const msgate = msgateAs(w)

  await msgate.version()
  await msgate.version()
  await Promise.all([msgate.version(), msgate.version()])

  expect(w.runs.length).toBe(1)
})

test('a module that loads again reads the version again', async ($, on) => {
  const w = world(on)
  w.respond = () => versionJson('1.0.0')

  await msgateAs(w).version()
  await msgateAs(w).version()

  expect(w.runs.length).toBe(2)
})

const UNAVAILABLE: [string, () => any][] = [
  ['exits non-zero', () => ({ exitCode: 70, stdout: '', stderr: 'msgate: cannot resolve the home\n' })],
  ['prints something that is not JSON', () => ok('msgate 1.0.0 (commit abc)')],
  ['prints no version', () => ok(JSON.stringify({ source_commit: 'abc' }))],
  ['prints a version that is not text', () => versionJson(1)],
  ['prints an empty version', () => versionJson('')],
  ['cannot be run', () => Promise.reject(new Error('spawn failed'))],
]

for (const [name, failing] of UNAVAILABLE) {
  test(`version is refused as msgate:version:unavailable when the binary ${name}, and is read again by the next call`, async ($, on) => {
    const w = world(on)
    w.respond = failing
    const msgate = msgateAs(w)

    expect(await msgate.version()).toEqual({ deny: 'msgate:version:unavailable' })

    w.respond = () => versionJson('1.2.3')
    expect(await msgate.version()).toEqual({ value: '1.2.3' })
  })
}
