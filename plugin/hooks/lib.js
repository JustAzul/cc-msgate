// Pure helpers for the agentcli mod. Nothing here touches the mods API: every
// call that reaches Claude Code lives in register.js, so these functions can be
// read and tested on their own.

export const POLL_MS = 15000
export const NOTICE_OUTPUT_BYTES = 8192
export const RESULT_OUTPUT_BYTES = 65536
export const JOB_SOURCE = 'mod'
export const NOTIFIED_LIMIT = 500
export const DELIVERY_PREFIX = 'delivery:'
// The most pending entries one poll reads, and how long a session's queue may sit
// untouched before another session's load removes it.
export const DELIVERY_BATCH = 50
export const ABANDONED_MS = 30 * 24 * 60 * 60 * 1000
// `agentcli status` exits with this code for a run it has no record of.
export const EXIT_RUN_NOT_FOUND = 4

const TERMINAL_STATES = ['done', 'failed', 'cancelled', 'timeout', 'lost']
const JOBS_ACTIONS = ['list', 'status', 'result', 'cancel']
// Ids start with a letter, digit or underscore so they can never read as a flag.
const ID_PATTERN = /^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$/
const OPTIONAL_FLAGS = ['provider', 'scenario', 'model', 'effort', 'sandbox', 'cwd']

export function isTerminal(state) {
  return TERMINAL_STATES.includes(state)
}

export function shimPath(pluginRoot) {
  return pluginRoot + '/bin/agentcli'
}

function isId(value) {
  return typeof value === 'string' && ID_PATTERN.test(value)
}

function fail(error) {
  return { error }
}

// turnCommand builds the argv and standard input of a command that starts a
// background turn: `ask` opens a conversation, `send` continues one. The prompt
// goes on standard input so no prompt text ever becomes part of an argument.
export function turnCommand(kind, bin, sessionId, input) {
  const args = input ?? {}
  if (typeof args.prompt !== 'string' || args.prompt.trim() === '') {
    return fail('prompt is required and must be a non-empty string')
  }
  const argv = [bin]
  if (kind === 'send') {
    if (!isId(args.conversation_id)) {
      return fail('conversation_id is required and must be an id: letters, digits, ".", "_" or "-", not starting with "-" or "."')
    }
    argv.push('send', args.conversation_id)
  } else {
    argv.push('exec')
  }
  argv.push('--background', '--json', '--source', 'mod', '--session-id', sessionId)
  for (const name of OPTIONAL_FLAGS) {
    const value = args[name]
    if (value === undefined || value === null || value === '') continue
    if (typeof value !== 'string') return fail(name + ' must be a string')
    argv.push('--' + name, value)
  }
  argv.push('-')
  return { argv, stdin: args.prompt }
}

// jobsCommand maps one `jobs` tool call to the agentcli command that answers it.
export function jobsCommand(bin, sessionId, input) {
  const args = input ?? {}
  if (!JOBS_ACTIONS.includes(args.action)) {
    return fail('action must be one of: ' + JOBS_ACTIONS.join(', '))
  }
  if (args.action === 'list') {
    return { argv: [bin, 'status', '--json', '--session-id', sessionId] }
  }
  if (!isId(args.run_id)) {
    return fail('run_id is required for ' + args.action + ' and must be a run id: letters, digits, ".", "_" or "-", not starting with "-" or "."')
  }
  if (args.action === 'status') return { argv: [bin, 'status', args.run_id, '--json'] }
  return { argv: [bin, args.action, args.run_id] }
}

// failureText turns a non-zero agentcli exit into one line the model can act
// on: the JSON error object when --json produced one, else what it wrote to
// standard error.
export function failureText(reply) {
  let detail = ''
  let status = ''
  try {
    const parsed = JSON.parse(reply.stdout)
    if (parsed && typeof parsed.error === 'string') detail = parsed.error
    if (parsed && typeof parsed.sdk_status === 'string') status = parsed.sdk_status
  } catch {
    // Not JSON: fall back to the plain text below.
  }
  if (detail === '') {
    detail = String(reply.stderr || reply.stdout || '').trim().replace(/^agentcli: /, '')
  }
  const head = 'agentcli exited ' + reply.exitCode + (status ? ' (' + status + ')' : '')
  return detail === '' ? head + ' with no message' : head + ': ' + detail
}

// admittedJob reads the ids out of a successful background admission.
export function admittedJob(stdout) {
  try {
    const parsed = JSON.parse(stdout)
    if (parsed && typeof parsed.conversation_id === 'string' && typeof parsed.run_id === 'string') {
      return { conversation_id: parsed.conversation_id, run_id: parsed.run_id }
    }
  } catch {
    // Reported below as unexpected output.
  }
  return null
}

// sessionJobs keeps the background jobs of a `status --json` listing. Runs the
// session made in the foreground share its session id but are not jobs.
export function sessionJobs(stdout) {
  try {
    const parsed = JSON.parse(stdout)
    const runs = parsed && Array.isArray(parsed.runs) ? parsed.runs : []
    return runs.filter((run) => run && run.background === true && typeof run.run_id === 'string')
  } catch {
    return []
  }
}

// modJobs keeps the jobs this mod started. A job started from the CLI, a hook
// or a skill belongs to whoever started it, who collects it with `wait`.
export function modJobs(jobs) {
  return jobs.filter((job) => job.source === JOB_SOURCE)
}

export function runningCount(jobs) {
  return jobs.filter((job) => !isTerminal(job.state)).length
}

export function statusText(count) {
  if (count <= 0) return undefined
  return count + (count === 1 ? ' job running' : ' jobs running')
}

// oldestFirst orders a listing, which agentcli prints newest first, so that
// notices and the notified set follow the order the jobs were admitted in.
export function oldestFirst(jobs) {
  return [...jobs].reverse().sort((a, b) => {
    const left = String(a.admitted_at ?? '')
    const right = String(b.admitted_at ?? '')
    return left < right ? -1 : left > right ? 1 : 0
  })
}

// readNotified returns the persisted set of announced run ids, or an empty set
// when the stored value is missing or damaged.
export function readNotified(stored) {
  return Array.isArray(stored) ? stored.filter((id) => typeof id === 'string') : []
}

// remember appends an id and keeps only the most recent NOTIFIED_LIMIT.
export function remember(notified, id) {
  return [...notified, id].slice(-NOTIFIED_LIMIT)
}

export function deliveryKey(sessionId) {
  return DELIVERY_PREFIX + sessionId
}

// readQueue returns the entries of a stored delivery queue, or none when the
// stored value is missing or damaged.
export function readQueue(stored) {
  return Array.isArray(stored) ? stored.filter((entry) => entry && typeof entry.run_id === 'string') : []
}

// queueEntry is the record of an admitted job that has not been delivered.
export function queueEntry(ids, purpose, now) {
  return {
    run_id: ids.run_id,
    conversation_id: ids.conversation_id,
    purpose: typeof purpose === 'string' && purpose !== '' ? purpose : null,
    admitted_at: now,
    attempted_at: null,
  }
}

// isSameQueue tells that a change left every entry as it was.
export function isSameQueue(before, after) {
  return before.length === after.length && before.every((entry, index) => entry === after[index])
}

export function withEntry(queue, entry) {
  return [...queue.filter((kept) => kept.run_id !== entry.run_id), entry]
}

export function withoutRun(queue, runId) {
  return queue.filter((entry) => entry.run_id !== runId)
}

// withAttempt sets when the delivery of a run was tried, or null once the try is
// known to have failed.
export function withAttempt(queue, runId, attemptedAt) {
  return queue.map((entry) => (entry.run_id === runId ? { ...entry, attempted_at: attemptedAt } : entry))
}

// byAdmission orders entries by when they were admitted, a run id breaking a tie.
function byAdmission(a, b) {
  if (a.admitted_at !== b.admitted_at) return a.admitted_at - b.admitted_at
  return a.run_id < b.run_id ? -1 : a.run_id > b.run_id ? 1 : 0
}

// nextBatch is the entries one poll works, oldest admitted first. A queue of
// more than DELIVERY_BATCH entries is read in turns: the batch starts after
// `cursor`, the last entry the previous batch read (null before the first), and
// wraps around, so entries behind a long run of unfinished ones are still reached.
export function nextBatch(queue, cursor) {
  const sorted = [...queue].sort(byAdmission)
  if (sorted.length <= DELIVERY_BATCH || cursor === null) return sorted.slice(0, DELIVERY_BATCH)
  const after = sorted.findIndex((entry) => byAdmission(entry, cursor) > 0)
  const from = after === -1 ? 0 : after
  return [...sorted.slice(from), ...sorted.slice(0, from)].slice(0, DELIVERY_BATCH)
}

// attemptedRuns is the run ids whose delivery was tried and not known to have
// failed: a notice for one of them may already have reached the session.
export function attemptedRuns(queue) {
  return new Set(queue.filter((entry) => entry.attempted_at !== null && entry.attempted_at !== undefined).map((entry) => entry.run_id))
}

// isAbandoned tells that a queue's newest entry is older than ABANDONED_MS; a
// queue with no entry has nobody to deliver to.
export function isAbandoned(queue, now) {
  const newest = Math.max(...queue.map((entry) => Number(entry.admitted_at) || 0))
  return queue.length === 0 || now - newest > ABANDONED_MS
}

// cutBytes returns the first `limit` bytes of text, never ending inside a
// multi-byte character.
export function cutBytes(text, limit) {
  const bytes = new TextEncoder().encode(text)
  if (bytes.length <= limit) return { text, isCut: false }
  let end = limit
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end -= 1
  return { text: new TextDecoder().decode(bytes.subarray(0, end)), isCut: true }
}

const POSSIBLY_REPEATED = ' (possibly repeated)'

export function noticeText(job, output) {
  return noticeWith(job, output, '')
}

// repeatedNoticeText is the notice of a run whose notice may already have reached
// the session in an earlier load.
export function repeatedNoticeText(job, output) {
  return noticeWith(job, output, POSSIBLY_REPEATED)
}

function noticeWith(job, output, mark) {
  const outcome = job.outcome ?? job.state
  const head = 'agentcli job ' + job.run_id + ' (' + job.scenario + ', conversation ' + job.conversation_id + ') finished: ' + outcome + mark
  if (output === null) {
    return head + '\n\nThe output could not be read; it is at ' + job.output_path
  }
  if (output.trim() === '') return head
  const cut = cutBytes(output, NOTICE_OUTPUT_BYTES)
  if (!cut.isCut) return head + '\n\n' + output
  return head + '\n\n' + cut.text + '\n\n[output cut at 8 KiB; full output at ' + job.output_path + ']'
}

export function toastText(job) {
  return 'job ' + job.run_id + ' finished: ' + (job.outcome ?? job.state)
}

// resultNote closes a `jobs result` answer that was cut at 64 KiB.
export function resultNote(outputPath) {
  return outputPath ? '[output cut at 64 KiB; full output at ' + outputPath + ']' : '[output cut at 64 KiB]'
}

// jobsListing is what /agentcli-jobs prints.
export function jobsListing(jobs) {
  if (jobs.length === 0) return 'No jobs in this session.'
  const lines = jobs.map((job) => [job.run_id, job.state, job.scenario, 'conversation ' + job.conversation_id].join('  '))
  return 'Jobs in this session (' + jobs.length + '):\n' + lines.join('\n')
}
