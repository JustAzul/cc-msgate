import { expect, test } from 'claude-code/testing'
import { INTERFACE } from '../hooks/iface.js'

test('the interface is an object of the five methods and nothing else', () => {
  expect(Object.keys(INTERFACE).sort()).toEqual(['backType', 'complete', 'jobs', 'run', 'version'])
  for (const method of Object.values(INTERFACE)) expect(typeof method).toBe('function')
})

for (const method of ['backType', 'version', 'run', 'jobs'] as const) {
  test(`${method}'s body, reached only when no hook answered, rejects as msgate:${method}:unserved`, async () => {
    await expect((INTERFACE[method] as any)()).rejects.toThrow('msgate:' + method + ':unserved')
  })
}
