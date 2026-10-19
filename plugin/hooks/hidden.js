// The hidden rows: the rows of a type backed with the follow command, each
// naming a run in its prompt. The engine registers a row when the plugin that
// backed its type spawns it, and the registry lives in the session's state, so
// an engine reload keeps it. Only the state calls touch the mods API.
//
import { backedTypeOf } from './backed.js'
import { asksEffort, bindEffort } from './efforts.js'
import { EXIT_RUN_NOT_FOUND, failureText, messageOf, shimPath } from './lib.js'
import { rowModeSettings } from './rowmode.js'
import { finishedRun, handoffFailure } from './subagent.js'

export const FOLLOW = 'follow'
// How long each of the two reads that fetch a run's prompt at a spawn may take.
export const SPAWN_READ_TIMEOUT_MS = 5000
// The most bytes of a run's prompt a row shows whole. The model a row runs on
// has a context window the host measures against the prompt, and the turn of a
// row whose prompt is over it fails at once without a word; 1 MiB is far under
// the size at which that was measured with a haiku row.
export const SHOWN_PROMPT_MAX_BYTES = 1024 * 1024
// What the spawn of a hidden row answers when MSGATE_ROW_MODE=auto refuses it.
export const AUTO_DENIAL = 'msgate: a hidden row does not open, since MSGATE_ROW_MODE=auto refuses it'
// A hidden row's prompt names its run on its first line.
const RUN_ID = '[A-Za-z0-9_][A-Za-z0-9._-]{0,127}'
const PROMPT_PATTERN = new RegExp('^msgate run (' + RUN_ID + ')$')
// The form the engine gives a prompt it shows whole: the first line, a blank
// line, and a body with something in it.
const REWRITTEN_PATTERN = new RegExp('^msgate run ' + RUN_ID + '[ \\t]*\\r?\\n[ \\t]*\\r?\\n\\s*\\S')

// The registry is the state value { plugin: 'msgate', key: 'hiddenRows' }: an
// object of run ids by agent id, oldest row first. The reference is spelled at
// each call, which is how the engine lists the state a module touches.

// The most rows kept; the oldest go first.
const RECORD_LIMIT = 200
// A write that another one beat is made again on the new value; this many tries.
const WRITE_ATTEMPTS = 20

const isRecord = (value) => typeof value === 'object' && value !== null && !Array.isArray(value)

async function readRegistry($) {
  const { value } = await $.state.get({ plugin: 'msgate', key: 'hiddenRows' })
  return isRecord(value) ? value : {}
}

// hiddenRunIds are the runs that have a hidden row registered, whatever became
// of the row.
export async function hiddenRunIds($) {
  return new Set(Object.values(await readRegistry($)).filter((runId) => typeof runId === 'string'))
}

// hiddenRunOf is the run a registered row follows, or null for an agent that is
// not a registered row.
export async function hiddenRunOf($, agentId) {
  const registry = await readRegistry($)
  return Object.hasOwn(registry, agentId) && typeof registry[agentId] === 'string' ? registry[agentId] : null
}

// registerHidden records the row of an agent as following a run. The write is
// conditional on the version read, so rows spawned together keep each other.
export async function registerHidden($, agentId, runId) {
  for (let attempt = 0; attempt < WRITE_ATTEMPTS; attempt += 1) {
    const { value, version } = await $.state.get({ plugin: 'msgate', key: 'hiddenRows' })
    const { [agentId]: _replaced, ...others } = isRecord(value) ? value : {}
    const kept = Object.entries({ ...others, [agentId]: runId }).slice(-RECORD_LIMIT)
    const written = await $.state.set({ plugin: 'msgate', key: 'hiddenRows' }, Object.fromEntries(kept), { ifVersion: version })
    if (written.isSet) return
  }
  throw new Error('the hidden rows were written by others ' + WRITE_ATTEMPTS + ' times in a row')
}

// runIdOfPrompt is the run a hidden row's prompt names on its first line, with
// a body after it or without, or null.
export function runIdOfPrompt(prompt) {
  const text = String(prompt ?? '')
  const lineEnd = text.indexOf('\n')
  const firstLine = lineEnd === -1 ? text : text.slice(0, lineEnd)
  return PROMPT_PATTERN.exec(firstLine.trim())?.[1] ?? null
}

// isRewrittenPrompt tells the prompt the engine rewrote: a hidden row's first
// line followed by a blank line and a body that is not empty.
export function isRewrittenPrompt(prompt) {
  return REWRITTEN_PATTERN.test(String(prompt ?? '').trimStart())
}

// The plugin that backed a full type: the part before its name.
const pluginOfType = (fullType) => fullType.slice(0, fullType.indexOf(':'))

// isEngineRow tells a type whose rows run an msgate job, not a follow.
const isEngineRow = (backed) => backed !== null && (backed.command === 'exec' || backed.command === 'review')

// admitSpawn is the engine's part of an `agent.spawn`, made by `spawner`, the
// plugin the host stamped on the call. Only the plugin that backed a type with
// the follow command may spawn its rows, and a row opens in no mode that cannot
// end it with plain text. A row that opens is registered with the run its prompt
// names, and shows the prompt of that run; any other spawn goes on as it is.
export async function admitSpawn($, e, spawner, next) {
  const fullType = e.subagentType
  const backed = typeof fullType === 'string' ? await backedTypeOf($, fullType) : null
  if (isEngineRow(backed) && asksEffort(e)) return bindEffort($, e, next)
  if (backed === null || backed.command !== FOLLOW || spawner !== pluginOfType(fullType)) return next(e)
  if ((await rowModeConfig($)).isRefused) return { deny: AUTO_DENIAL }

  const runId = runIdOfPrompt(e.prompt)
  const spawned = await spawnShowingPrompt($, e, runId, next)
  if (spawned.agentId !== undefined && runId !== null) await registerQuietly($, spawned.agentId, runId)
  return spawned
}

// spawnShowingPrompt spawns the row with the prompt of its run below the run id
// when the row's prompt is the run id alone and the prompt can be read whole.
// Whatever stops that leaves the spawn as it was, with a line in the debug log.
async function spawnShowingPrompt($, e, runId, next) {
  if (runId === null || String(e.prompt).trim() !== 'msgate run ' + runId) return next(e)
  const read = await readRunPrompt($, shimPath($.plugin.root), runId)
  if (read.text === undefined) {
    await $.ui.log('the row of ' + runId + ' opens without the prompt of its run: ' + read.reason, { to: 'debug' })
    return next(e)
  }
  return nextOrPlain($, e, 'msgate run ' + runId + '\n\n' + read.text, next)
}

// nextOrPlain spawns the row with the full prompt and, when the host refuses
// that prompt, once more with the plain one the call carried: a row without its
// prompt shown is better than a row that does not open.
async function nextOrPlain($, e, prompt, next) {
  let refusal
  try {
    const spawned = await next({ ...e, prompt })
    if (spawned.deny === undefined) return spawned
    refusal = spawned.deny
  } catch (error) {
    refusal = messageOf(error)
  }
  await $.ui.log('the host refused the full prompt of a hidden row, which opens with the plain one: ' + refusal, { to: 'debug' })
  return next(e)
}

// readRunPrompt reads the prompt of a run the engine answers for: { text }, or
// { reason } when the run is not one it answers for or the prompt cannot be had
// whole or is too long to show.
async function readRunPrompt($, bin, runId) {
  const checked = await checkRun($, bin, runId, { timeoutMs: SPAWN_READ_TIMEOUT_MS })
  if (checked.isPassed) return { reason: 'the engine does not answer for the run' }
  if (checked.error !== undefined) return { reason: checked.error }

  let reply
  try {
    reply = await $.process.run([bin, 'prompt', runId], { timeoutMs: SPAWN_READ_TIMEOUT_MS })
  } catch (error) {
    return { reason: 'msgate could not run: ' + messageOf(error) }
  }
  if (reply.exitCode !== 0) return { reason: failureText(reply) }
  if (reply.isStdoutTruncated === true) return { reason: 'the prompt is longer than the output msgate can return' }
  return shownPrompt(reply.stdout)
}

// shownPrompt is { text } for a prompt a row shows whole, or the { reason } it
// does not: empty, or over SHOWN_PROMPT_MAX_BYTES of UTF-8.
function shownPrompt(text) {
  if (text.trim() === '') return { reason: 'the prompt is empty' }
  if (new TextEncoder().encode(text).length > SHOWN_PROMPT_MAX_BYTES) return { reason: 'the prompt is longer than the ' + SHOWN_PROMPT_MAX_BYTES + ' bytes a row can show' }
  return { text }
}

// The notes of MSGATE_ROW_MODE already logged by this load.
const loggedNotes = new Set()

// noteOnce logs a note to the debug log the first time this load meets it.
async function noteOnce($, note) {
  if (loggedNotes.has(note)) return
  loggedNotes.add(note)
  await $.ui.log(note, { to: 'debug' })
}

// rowModeConfig is what MSGATE_ROW_MODE says of the row, read at each use so the
// variable can change while a session runs; a note is logged once per load. A
// variable that cannot be read forces nothing, and the failure is noted.
async function rowModeConfig($) {
  let env
  try {
    env = await $.env.get('MSGATE_ROW_MODE')
  } catch (error) {
    await noteOnce($, 'MSGATE_ROW_MODE could not be read: ' + messageOf(error) + '; it counts as not set')
    env = undefined
  }
  const settings = rowModeSettings({ env })
  for (const note of settings.notes) await noteOnce($, note)
  return settings
}

// registerQuietly registers a row that has opened. The row is open already, so a
// registry that cannot be written is logged: the row then is not a registered
// row, so a prompt shown whole ends its turn in plain text, and its run stays in
// the band.
async function registerQuietly($, agentId, runId) {
  try {
    await registerHidden($, agentId, runId)
  } catch (error) {
    await $.ui.log('could not register the hidden row of ' + runId + ': ' + messageOf(error), { to: 'debug' })
  }
}

// checkRun tells whether the engine answers the row that follows a run: the run
// must be one the engine knows, of this session, asking to be shown. It answers
// { run }, { isPassed: true } for a run the engine does not answer for, or
// { error } when the run could not be read.
export async function checkRun($, bin, runId, options = {}) {
  let reply
  try {
    reply = await $.process.run([bin, 'status', runId, '--json'], options)
  } catch (error) {
    return { error: 'msgate could not run: ' + messageOf(error) }
  }
  if (reply.exitCode === EXIT_RUN_NOT_FOUND) return { isPassed: true }
  if (reply.exitCode !== 0) return { error: failureText(reply) }

  const run = finishedRun(reply.stdout)
  if (run === null) return { error: 'msgate printed an unexpected status: ' + reply.stdout.trim().slice(0, 500) }
  return run.session_id === (await $.session.id()) && run.agent_feedback === true ? { run } : { isPassed: true }
}

// unavailableText is what a hidden row ends its turn with when the run it names
// is not one the engine answers for it: plain text, never the model beneath,
// which would be sent a prompt that can be megabytes.
export function unavailableText(runId) {
  return 'msgate run ' + runId + ' is not available to this row'
}

// checkHiddenRow tells how a hidden row's step goes on: { run } for a registered
// row whose run the engine answers for, { error } when that run could not be
// read, { failure } with the text that ends the turn when the row may not reach
// the model beneath, or { isPassed: true } for a row that was never registered
// and carries the plain prompt, which the spawner's check already vouched for.
// `markedRunId` is the run that the prompt the row's turn started with names
// when that prompt is the rewritten form, or null.
export async function checkHiddenRow($, bin, runId, markedRunId) {
  if (runId !== null) {
    const checked = await checkRun($, bin, runId)
    return checked.isPassed ? { failure: unavailableText(runId) } : checked
  }
  return markedRunId === null ? { isPassed: true } : { failure: unavailableText(markedRunId) }
}

// endedText is what a hidden row ends its turn with: plain text, never a
// hand-back, since the hook that started the run delivers its result.
export function endedText(run) {
  return 'msgate run ' + run.run_id + ' ended ' + (run.outcome ?? run.state)
}

// stoppedText ends the turn of a row that was stopped before its run ended.
export function stoppedText(runId) {
  return 'msgate run ' + runId + ' stopped'
}

// followFailureText is what a follow that threw ends the turn with: the stopped
// text when the row was stopped, since stopping aborts the reads the follow was
// waiting on, and the hand-off failure for any other error.
export function followFailureText(error, runId, signal) {
  return signal.aborted ? stoppedText(runId) : handoffFailure(messageOf(error))
}
