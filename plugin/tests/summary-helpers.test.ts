import { expect, test } from 'claude-code/testing'
import { SYSTEM, cleanLabel, isDue, newRun, outcomeOf, summaryPrompt } from '../hooks/summary.js'

test('a label is the reply trimmed, with control and ANSI characters stripped', () => {
  expect(cleanLabel('  Reading auth.go \n')).toBe('Reading auth.go')
  expect(cleanLabel('\u001b[1;32mRunning store tests\u001b[0m')).toBe('Running store tests')
  expect(cleanLabel('Reading\u0007 auth.go\u0000')).toBe('Reading auth.go')
  expect(cleanLabel('\u001b]0;title\u0007Reading auth.go')).toBe('Reading auth.go')
})

test('a reply that is empty, longer than a line, marked up or over 100 characters is no label', () => {
  for (const reply of ['', '   ', '\u001b[0m', 'two\nlines', 'one\r\ntwo', '# Heading', '`code`', '* item', '- item', '> quote', 'x'.repeat(101)]) {
    expect(cleanLabel(reply)).toBeNull()
  }
  expect(cleanLabel('x'.repeat(100))).toBe('x'.repeat(100))
  expect(cleanLabel('Reading a-b.go')).toBe('Reading a-b.go')
  expect(cleanLabel(undefined)).toBeNull()
})

test('the prompt lists the newest ten entries oldest first, cut to 300 characters', () => {
  const entries = Array.from({ length: 12 }, (_, i) => 'entry ' + (i + 1))
  const lines = summaryPrompt(entries, null).split('\n')
  expect(lines[0]).toBe('Newest activity of the agent, oldest first:')
  expect(lines.slice(1)).toEqual(Array.from({ length: 10 }, (_, i) => '- entry ' + (i + 3)))
  expect(summaryPrompt(['y'.repeat(301)], null)).toBe('Newest activity of the agent, oldest first:\n- ' + 'y'.repeat(300))
})

test('the previous label closes the prompt as its own paragraph', () => {
  expect(summaryPrompt(['a'], 'Reading a.go')).toBe(
    'Newest activity of the agent, oldest first:\n- a\n\nPrevious label: Reading a.go (say something NEW).',
  )
})

test('the system prompt is the measured one', () => {
  expect(SYSTEM.split('\n')[0]).toBe('You label what a coding agent is doing right now, for a one-line status row that truncates around 40 characters.')
  expect(SYSTEM.split('\n')).toHaveLength(10)
})

test('a run asks for a label on its first entry, then at every third new entry, one at a time', () => {
  const run = newRun()
  expect(isDue(run)).toBe(false)
  run.entries = ['a']
  expect(isDue(run)).toBe(true)
  run.hasRequested = true
  run.entries = ['b', 'c']
  expect(isDue(run)).toBe(false)
  run.entries = ['b', 'c', 'd']
  expect(isDue(run)).toBe(true)
  run.isInFlight = true
  expect(isDue(run)).toBe(false)
})

test('a run asks again after the number of entries given, one at a time', () => {
  const run = newRun()
  run.hasRequested = true
  run.entries = ['a', 'b', 'c', 'd']
  expect(isDue(run, 5)).toBe(false)
  expect(isDue(run, 4)).toBe(true)
  expect(isDue(run, 1)).toBe(true)
  run.isInFlight = true
  expect(isDue(run, 1)).toBe(false)
})

test('an outcome is the label, or why there is none', () => {
  expect(outcomeOf({ isAnswered: true, text: ' Reading a.go ' })).toEqual({ label: 'Reading a.go' })
  expect(outcomeOf({ isAnswered: true, text: '# x' })).toEqual({ failure: 'the reply is not a label' })
  expect(outcomeOf({ isAnswered: false, reason: 'api-error', status: 429 })).toEqual({ failure: 'no reply: api-error (status 429)' })
  expect(outcomeOf({ isAnswered: false, reason: 'empty-reply' })).toEqual({ failure: 'no reply: empty-reply' })
})
