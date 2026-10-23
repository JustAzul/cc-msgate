// Pure helpers for the progress summaries: one short label that says what a
// run is doing, asked of a small model from the run's latest progress entries.
// Nothing here touches the mods API, so these functions can be read and tested
// on their own.

export const SUMMARY_MODEL = 'claude-haiku-5-5'
export const SUMMARY_EFFORT = 'low'
export const SUMMARY_MAX_TOKENS = 40
export const SUMMARY_TIMEOUT_MS = 15000
// A label is requested again each time this many entries have arrived since
// the last request.
export const ENTRIES_PER_REQUEST = 3
// What one request carries of the entries since the last: the newest ones, each
// cut to a length.
export const WINDOW_ENTRIES = 10
export const ENTRY_CHARS = 300
export const LABEL_CHARS = 100
// For each provider, across the session, so a busy session does not queue a call
// for every run.
export const SESSION_IN_FLIGHT_MAX = 2
// After a provider answers 429 no request goes out to it for this long.
export const RATE_LIMIT_PAUSE_MS = 60000
export const LABEL_MARK = '» '

const USAGE_COUNTS = ['input_tokens', 'output_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens']

export const SYSTEM = [
  'You label what a coding agent is doing right now, for a one-line status row that truncates around 40 characters.',
  'Describe its most recent action in 3-5 words using present tense (-ing). Name the file, command or function, not the branch.',
  'Reply with the label only: one line, no quotes, no markdown, no final period.',
  '',
  'Good: "Reading workerlog.go"',
  'Good: "Running store tests"',
  'Good: "Reviewing the uncommitted diff"',
  'Bad (past tense): "Read workerlog.go"',
  'Bad (too vague): "Investigating the issue"',
  'Bad (too long): "Reviewing the full branch diff and the store package integration"',
].join('\n')

// The reply starts with a character that markdown reads as formatting.
const MARKDOWN_MARKER = /^[#`*>-]/
const NEWLINE = /[\r\n]/
export const ANSI = /\u001b(?:\[[0-?]*[ -/]*[@-~]|\][^\u0007\u001b]*(?:\u0007|\u001b\\)|[@-Z\\-_])/g
export const CONTROL = /[\u0000-\u001f\u007f-\u009f]/g

// wholeWithin reads a cadence: the whole number `value`, held between `min` and
// `max`, or `fallback` when it is not a whole number.
export function wholeWithin(value, min, max, fallback) {
  return Number.isInteger(value) ? Math.min(max, Math.max(min, value)) : fallback
}

// newestEntries keeps the entries one request can carry.
export function newestEntries(entries) {
  return entries.slice(-WINDOW_ENTRIES)
}

// summaryPrompt is the user message of a request: the window oldest first, and
// the previous label when there is one.
export function summaryPrompt(entries, previous) {
  const lines = newestEntries(entries).map((entry) => '- ' + entry.slice(0, ENTRY_CHARS)).join('\n')
  const tail = previous === null ? '' : '\n\nPrevious label: ' + previous + ' (say something NEW).'
  return 'Newest activity of the agent, oldest first:\n' + lines + tail
}

export function summaryRequest(entries, previous) {
  return {
    model: SUMMARY_MODEL,
    effort: SUMMARY_EFFORT,
    system: SYSTEM,
    prompt: summaryPrompt(entries, previous),
    maxTokens: SUMMARY_MAX_TOKENS,
    timeoutMs: SUMMARY_TIMEOUT_MS,
  }
}

// cleanLabel reads a reply as a label, or null when it is not one: empty, more
// than a line, opening with a markdown marker or longer than LABEL_CHARS. The
// newline check comes first because stripping control characters removes it.
export function cleanLabel(reply) {
  const trimmed = String(reply ?? '').trim()
  if (NEWLINE.test(trimmed)) return null
  const label = trimmed.replace(ANSI, '').replace(CONTROL, '').trim()
  if (label === '' || MARKDOWN_MARKER.test(label) || label.length > LABEL_CHARS) return null
  return label
}

// outcomeOf is what a finished completion comes to: the label, or why there is
// none.
export function outcomeOf(result) {
  if (result && result.isAnswered === true) {
    const label = cleanLabel(result.text)
    return label === null ? { failure: 'the reply is not a label' } : { label }
  }
  const status = result && typeof result.status === 'number' ? ' (status ' + result.status + ')' : ''
  return { failure: 'no reply: ' + String(result?.reason ?? 'unknown') + status }
}

// isBilled tells a completion that used tokens, whether or not it answered:
// any of the four counts of its usage is above zero.
export function isBilled(result) {
  const usage = result?.usage
  return USAGE_COUNTS.some((count) => typeof usage?.[count] === 'number' && usage[count] > 0)
}

// summaryRecording is the record of one billed summary completion, made for a
// run in the session that was running it, under the model that answered.
export function summaryRecording(usage, sessionId, runId, model = SUMMARY_MODEL) {
  return { usage, sessionId, model, source: 'mod-summary', runId }
}

export function isRateLimited(result) {
  return Boolean(result) && result.isAnswered === false && result.reason === 'api-error' && result.status === 429
}

export function labelLine(label) {
  return LABEL_MARK + label
}

// newRun is what is known of one run's summaries: the entries since the last
// request, the last label and what the display is to show next.
export function newRun() {
  return { entries: [], hasRequested: false, isInFlight: false, label: null, isUnread: false, isFailureLogged: false }
}

// isDue tells that a run asks for a label now: its first entry has arrived, or
// `every` have since the last request, and none is in flight.
export function isDue(run, every = ENTRIES_PER_REQUEST) {
  if (run.isInFlight || run.entries.length === 0) return false
  return !run.hasRequested || run.entries.length >= every
}
