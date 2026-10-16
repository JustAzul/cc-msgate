// The hidden rows: the rows of a type backed with the follow command, each
// naming a run in its prompt. The engine registers a row when the plugin that
// backed its type spawns it, and the registry lives in the session's state, so
// an engine reload keeps it. Only the state calls touch the mods API.
//
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
