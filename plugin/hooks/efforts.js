// The effort an Agent call asked for. The model's Agent call names an effort for
// the agent it spawns; the row of a backed type runs on the engine, so its first
// turn admits the run with that effort instead of the type's. The call and its
// spawn are two events, joined by the call's tool_use_id, and the spawn's agent
// is only known once it started.
//
// An agent's effort is also kept in the session's state, so a reload between the
// spawn and the first turn loses nothing. It is the state value
// { plugin: 'msgate', key: 'agentEfforts' } with the agent id as the member's id:
// the effort until a first turn takes it, then null. The reference is spelled at
// each call, which is how the engine lists the state a module touches.

import { messageOf } from './lib.js'

// The efforts of the Agent calls still running, by tool_use_id.
const pending = new Map()
// The efforts of the agents the spawns started, by agent id, until a first turn
// takes them.
const bound = new Map()
// The spawns of effort-carrying Agent calls that have not answered yet. An agent
// can take its first turn before its spawn answers with its id.
const spawning = new Set()
// The longest a first turn waits for those spawns.
export const SPAWN_WAIT_MS = 5000

// forgetEfforts empties what this load remembers, as a reload does.
export function forgetEfforts() {
  pending.clear()
  bound.clear()
  spawning.clear()
}

const isText = (value) => typeof value === 'string' && value !== ''

// holdEffort runs an Agent call with its effort remembered, for the spawn it
// raises beneath. A call that names no effort, or a fork (which runs on the
// caller's own), goes on as it is. It always waits for the call: returning
// earlier would abort it.
export async function holdEffort(e, next) {
  if (e.tool !== 'Agent' || !isText(e.effort) || e.subagent_type === 'fork' || !isText(e.tool_use_id)) return next(e)
  pending.set(e.tool_use_id, e.effort)
  try {
    return await next(e)
  } finally {
    pending.delete(e.tool_use_id)
  }
}

// asksEffort tells a spawn that belongs to an Agent call with an effort and
// opens a row of the engine: not a fork, a resumed run, a workflow's agent or a
// teammate.
export function asksEffort(e) {
  return pending.has(e.tool_use_id) && e.fork !== true && e.resume === undefined && e.workflow === undefined && e.isTeammate !== true
}

// bindEffort starts the spawn and remembers the call's effort for the agent it
// started. A spawn that started none drops the effort, and says so.
export async function bindEffort($, e, next) {
  const effort = pending.get(e.tool_use_id)
  let answer
  const answered = new Promise((resolve) => {
    answer = resolve
  })
  spawning.add(answered)
  let spawned
  try {
    spawned = await next(e)
    // Before any other call, so a first turn that starts now finds it.
    if (typeof spawned.agentId === 'string') bound.set(spawned.agentId, effort)
  } finally {
    spawning.delete(answered)
    answer()
  }

  if (typeof spawned.agentId === 'string') {
    await keepQuietly($, spawned.agentId, effort)
    return spawned
  }

  // The call's effort has no agent to go to; a later spawn of the call is not bound.
  pending.delete(e.tool_use_id)
  await $.ui.log('the effort ' + effort + ' of an Agent call was dropped: its spawn started no agent', { to: 'debug' })
  return spawned
}

// keepQuietly writes an agent's effort to the state. The agent is open already,
// so a state that cannot be written is logged: this load still has the effort.
async function keepQuietly($, agentId, effort) {
  try {
    await $.state.set({ plugin: 'msgate', key: 'agentEfforts', id: agentId }, effort)
  } catch (error) {
    await $.ui.log('could not keep the effort of agent ' + agentId + ': ' + messageOf(error), { to: 'debug' })
  }
}

// takeEffort is the effort an agent's first turn admits its run with, or
// undefined. The turn takes it once: a first turn is never made again. Where no
// effort is found while a spawn is still in flight, the agent may be that
// spawn's: the turn waits for the spawns, for no longer than SPAWN_WAIT_MS.
export async function takeEffort($, agentId) {
  let effort = await readEffort($, agentId)
  const isAnswered = effort !== undefined || spawning.size === 0 || (await spawnsAnswered($))
  // A spawn of this load sets `bound` before it leaves `spawning`, whenever the
  // state read above returned; a spawn of an earlier load is in that read.
  effort ??= bound.get(agentId)
  if (effort === undefined) {
    // The spawn still out may be this agent's, so the type's effort may not be
    // the one asked for.
    if (!isAnswered) await $.ui.log('agent ' + agentId + ' waited ' + SPAWN_WAIT_MS + ' ms for the spawns of Agent calls with an effort, and runs with its type\'s effort', { to: 'debug' })
    return undefined
  }

  bound.delete(agentId)
  await dropQuietly($, agentId)
  return effort
}

async function readEffort($, agentId) {
  return bound.get(agentId) ?? (await keptEffort($, agentId))
}

// spawnsAnswered waits for the spawns in flight and tells whether they all
// answered before SPAWN_WAIT_MS ran out.
async function spawnsAnswered($) {
  let timer
  const timeout = new Promise((resolve) => {
    timer = $.clock.after(SPAWN_WAIT_MS, () => resolve(false))
  })
  const isAnswered = await Promise.race([Promise.all([...spawning]).then(() => true), timeout])
  timer.cancel()
  return isAnswered
}

async function keptEffort($, agentId) {
  const { value } = await $.state.get({ plugin: 'msgate', key: 'agentEfforts', id: agentId })
  return isText(value) ? value : undefined
}

async function dropQuietly($, agentId) {
  try {
    await $.state.set({ plugin: 'msgate', key: 'agentEfforts', id: agentId }, null)
  } catch (error) {
    await $.ui.log('could not clear the effort of agent ' + agentId + ': ' + messageOf(error), { to: 'debug' })
  }
}
