// The hidden rows: the rows of a type backed with the follow command, each
// naming a run in its prompt. The engine registers a row when the plugin that
// backed its type spawns it, and the registry lives in the session's state, so
// an engine reload keeps it. Only the state calls touch the mods API.
//
import { backedTypeOf } from './backed.js'
import { asksEffort, bindEffort } from './efforts.js'
import { EXIT_RUN_NOT_FOUND, failureText, messageOf } from './lib.js'
import { rowModeSettings } from './rowmode.js'
import { finishedRun, handoffFailure } from './subagent.js'

export const FOLLOW = 'follow'
// What the spawn of a hidden row answers when MSGATE_ROW_MODE=auto refuses it.
export const AUTO_DENIAL = 'msgate: a hidden row does not open, since MSGATE_ROW_MODE=auto refuses it'
// A hidden row's prompt names its run.
const PROMPT_PATTERN = /^msgate run ([A-Za-z0-9_][A-Za-z0-9._-]{0,127})$/

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

// runIdOfPrompt is the run a hidden row's prompt names, or null.
export function runIdOfPrompt(prompt) {
  return PROMPT_PATTERN.exec(String(prompt ?? '').trim())?.[1] ?? null
}

// The plugin that backed a full type: the part before its name.
const pluginOfType = (fullType) => fullType.slice(0, fullType.indexOf(':'))

// isEngineRow tells a type whose rows run an msgate job, not a follow.
const isEngineRow = (backed) => backed !== null && (backed.command === 'exec' || backed.command === 'review')

// admitSpawn is the engine's part of an `agent.spawn`, made by `spawner`, the
// plugin the host stamped on the call. Only the plugin that backed a type with
// the follow command may spawn its rows, and a row opens in no mode that cannot
// end it with plain text. A row that opens is registered with the run its prompt
// names; any other spawn goes on as it is.
export async function admitSpawn($, e, spawner, next) {
  const fullType = e.subagentType
  const backed = typeof fullType === 'string' ? await backedTypeOf($, fullType) : null
  if (isEngineRow(backed) && asksEffort(e)) return bindEffort($, e, next)
  if (backed === null || backed.command !== FOLLOW || spawner !== pluginOfType(fullType)) return next(e)
  if ((await rowModeConfig($)).isRefused) return { deny: AUTO_DENIAL }

  const spawned = await next(e)
  const runId = runIdOfPrompt(e.prompt)
  if (spawned.agentId !== undefined && runId !== null) await registerQuietly($, spawned.agentId, runId)
  return spawned
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
// registry that cannot be written is logged: the row then passes to the model
// beneath, and its run stays in the band.
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
export async function checkRun($, bin, runId) {
  let reply
  try {
    reply = await $.process.run([bin, 'status', runId, '--json'])
  } catch (error) {
    return { error: 'msgate could not run: ' + messageOf(error) }
  }
  if (reply.exitCode === EXIT_RUN_NOT_FOUND) return { isPassed: true }
  if (reply.exitCode !== 0) return { error: failureText(reply) }

  const run = finishedRun(reply.stdout)
  if (run === null) return { error: 'msgate printed an unexpected status: ' + reply.stdout.trim().slice(0, 500) }
  return run.session_id === (await $.session.id()) && run.agent_feedback === true ? { run } : { isPassed: true }
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
