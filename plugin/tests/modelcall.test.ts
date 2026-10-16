import { expect, test } from 'claude-code/testing'
import { modelCallCommand } from '../hooks/modelcall.js'
import { summaryRecording } from '../hooks/summary.js'

const BIN = '/plugin/bin/msgate'
const USAGE = { input_tokens: 458, output_tokens: 18 }

test('a summary call is recorded for its run, with no call id, attrs or time of its own', () => {
  expect(modelCallCommand(BIN, summaryRecording(USAGE, 'sess-1', 'run-1'))).toEqual([
    BIN, 'usage', 'add', '--session-id', 'sess-1', '--provider', 'anthropic', '--model', 'claude-haiku-5-5',
    '--source', 'mod-summary', '--run-id', 'run-1', '--json',
  ])
})

test('a service call is recorded with its call id, attrs and the second it was made at', () => {
  const recording = {
    usage: USAGE,
    sessionId: 'sess-1',
    model: 'claude-haiku-5-5',
    source: 'mod:followups',
    callId: 'followups:chk-1:e1:v0plus',
    attrs: { check_id: 'chk-1', round: '0' },
    at: Date.UTC(2026, 9, 12, 17, 25, 54, 987),
  }

  expect(modelCallCommand(BIN, recording)).toEqual([
    BIN, 'usage', 'add', '--session-id', 'sess-1', '--provider', 'anthropic', '--model', 'claude-haiku-5-5',
    '--source', 'mod:followups', '--call-id', 'followups:chk-1:e1:v0plus', '--attr', 'check_id=chk-1', '--attr', 'round=0',
    '--ts', '2026-10-12T17:25:54Z', '--json',
  ])
})
