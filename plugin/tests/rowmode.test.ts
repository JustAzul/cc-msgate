import { expect, test } from 'claude-code/testing'
import { rowModeSettings } from '../hooks/rowmode.js'

test('a variable that is absent, empty or unreadable forces nothing', async () => {
  for (const env of [undefined, null, '', 7]) {
    expect(rowModeSettings({ env })).toEqual({ isRefused: false, notes: [] })
  }
})

test('auto forces the refusal, with no note', async () => {
  expect(rowModeSettings({ env: 'auto' })).toEqual({ isRefused: true, notes: [] })
})

test('a permission mode the host knows is accepted and refuses nothing', async () => {
  for (const env of ['default', 'acceptEdits', 'bypassPermissions', 'plan', 'dontAsk']) {
    expect(rowModeSettings({ env })).toEqual({ isRefused: false, notes: [] })
  }
})

test('any other value is accepted and refuses nothing, with a note naming it', async () => {
  expect(rowModeSettings({ env: 'banana' })).toEqual({
    isRefused: false,
    notes: ['MSGATE_ROW_MODE=banana is not a permission mode the host knows; only auto refuses the row'],
  })
})
