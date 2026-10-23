// The runs each plugin admitted through the interface's run method, kept in the
// session's state so `jobs` can list them by id however many newer runs the
// session has, and so an engine reload keeps them. The reference is spelled at
// each call, which is how the engine lists the state a module touches.

// The most run ids kept for one caller; the oldest go first.
const RECORD_LIMIT = 200
// A write that another one beat is made again on the new value; this many tries.
const WRITE_ATTEMPTS = 20

const isRecord = (value) => typeof value === 'object' && value !== null && !Array.isArray(value)

// changeRecord applies `change` to the stored record and writes the result back,
// conditionally on the version read, so writers working together keep each
// other's changes.
async function changeRecord($, change) {
  for (let attempt = 0; attempt < WRITE_ATTEMPTS; attempt += 1) {
    const { value, version } = await $.state.get({ plugin: 'msgate', key: 'admittedRuns' })
    const written = await $.state.set({ plugin: 'msgate', key: 'admittedRuns' }, change(isRecord(value) ? value : {}), { ifVersion: version })
    if (written.isSet) return
  }
  throw new Error('the admitted runs were written by others ' + WRITE_ATTEMPTS + ' times in a row')
}

// admittedRunIds are the run ids a caller admitted, oldest first.
export async function admittedRunIds($, caller) {
  const { value } = await $.state.get({ plugin: 'msgate', key: 'admittedRuns' })
  if (!isRecord(value) || !Object.hasOwn(value, caller) || !Array.isArray(value[caller])) return []
  return value[caller].filter((id) => typeof id === 'string')
}

// recordAdmitted adds a run to the caller's record.
export function recordAdmitted($, caller, runId) {
  return changeRecord($, (record) => {
    const earlier = Object.hasOwn(record, caller) && Array.isArray(record[caller]) ? record[caller] : []
    return { ...record, [caller]: [...earlier.filter((id) => id !== runId), runId].slice(-RECORD_LIMIT) }
  })
}

// forgetRuns takes runs out of the caller's record; a caller left with none has
// no entry.
export function forgetRuns($, caller, runIds) {
  return changeRecord($, (record) => {
    const { [caller]: earlier, ...others } = record
    const kept = (Array.isArray(earlier) ? earlier : []).filter((id) => !runIds.includes(id))
    return kept.length === 0 ? others : { ...others, [caller]: kept }
  })
}
