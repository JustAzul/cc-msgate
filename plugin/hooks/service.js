// The msgate.complete service: other plugins ask msgate for a model
// completion, and msgate makes it and records what it cost. Nothing here
// touches the mods API: the engine comes in through ports, so the decisions
// can be read and tested on their own.

import { isBilled } from './summary.js'

const PRE = 'msgate:pre-model:'
const AFTER = 'msgate:after-model:'
const UNKNOWN = 'msgate:unknown-state:'
const CONTRACT = 1
const PROVIDER = 'anthropic'
const CALL_ID = /^[A-Za-z0-9][A-Za-z0-9:._-]{0,199}$/
// What the record of a call takes of the caller's attrs; the rest is left out.
const ATTR_KEY = /^[a-z_]{1,32}$/
const ATTR_VALUE_CHARS = 200
const ATTRS_MAX = 16
// The alias the checks ask for, and the id it is priced under when the
// environment pins none.
const HAIKU = 'haiku'
const HAIKU_PRICED = 'claude-haiku-5-5'

// What engine.create adds to `$`. An interface is an object of methods and
// nothing else; the call itself is answered by the hook on msgate.complete,
// which has a `$` to make the completion with. This body only runs when that
// hook and its .catch both failed, and then nobody knows whether the model was
// reached.
export const NOUN = {
  complete: async () => {
    throw new Error('msgate:unserved')
  },
}

const isObject = (value) => typeof value === 'object' && value !== null

// why names the first thing wrong with a call, as the <why> of a pre-model
// denial, or null when there is none.
function why(e) {
  const meta = e?.meta
  if (!isObject(meta) || meta.contract !== CONTRACT) return 'contract'
  if (typeof meta.callId !== 'string' || !CALL_ID.test(meta.callId)) return 'call-id'
  if (meta.provider !== undefined && meta.provider !== PROVIDER) return 'unsupported-provider'
  if (!isObject(e.request)) return 'request'
  return null
}

// recordedAttrs keeps the caller's attrs the record can take: a key and a text
// value of the allowed size, at most ATTRS_MAX of them. An attr it cannot take
// is left out and nothing else: metadata never decides whether a call is served.
function recordedAttrs(attrs) {
  const kept = {}
  if (!isObject(attrs)) return kept
  for (const [key, value] of Object.entries(attrs)) {
    if (Object.keys(kept).length === ATTRS_MAX) break
    if (ATTR_KEY.test(key) && typeof value === 'string' && value.length <= ATTR_VALUE_CHARS) kept[key] = value
  }
  return kept
}

// pricedModel is the id a call is recorded under: haiku is an alias, so the id
// the environment pins for it, else the one it is priced as, marked as inferred
// by the attrs that say where it came from. Any other model is recorded as named.
async function pricedModel(ports, requested) {
  if (requested !== HAIKU) return { id: requested, attrs: {} }
  const pinned = await ports.haikuPin()
  const isPinned = typeof pinned === 'string' && pinned !== ''
  return {
    id: isPinned ? pinned : HAIKU_PRICED,
    attrs: { model_inferred: isPinned ? 'env' : 'table', model_requested: requested },
  }
}

// kindWhy writes the kind of a hook failure as a <why>: [a-z0-9-]+.
function kindWhy(kind) {
  return String(kind).toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '') || 'unknown'
}

// createService is one load's service: the calls in flight, by call id.
export function createService() {
  const calls = new Map()

  // finish forgets the call, unless a failure handler already did and the id
  // has been taken by another call since.
  function finish(callId, call, answer) {
    if (calls.get(callId) === call) calls.delete(callId)
    return answer
  }

  async function serve(ports, e, ctx) {
    const problem = why(e)
    if (problem !== null) return { deny: PRE + problem }
    const { callId } = e.meta
    if (calls.has(callId)) return { deny: PRE + 'duplicate' }
    const call = { phase: 'validating', isCancelled: false }
    calls.set(callId, call)

    const sessionId = await ports.sessionId()
    const at = await ports.now()
    const model = await pricedModel(ports, e.request.model)

    // The proof that the model was not called when this denies: nothing awaits
    // between this check and the dispatch, so a handler that cancelled the call
    // first wins for good.
    if (call.isCancelled || ctx.signal.aborted) return finish(callId, call, { deny: PRE + 'cancelled' })
    call.phase = 'dispatched'
    let result
    try {
      result = await ports.complete(e.request, ctx.signal)
    } catch {
      // That the engine sent nothing is only the type's word, so not pre-model.
      return finish(callId, call, { deny: AFTER + 'refused' })
    }
    // Nothing after the model call may reach the caller, whose answer this is.
    try {
      if (isBilled(result)) {
        const attrs = { ...recordedAttrs(e.meta.attrs), ...model.attrs }
        const source = 'mod:' + ctx.caller
        void ports.record({ usage: result.usage, sessionId, model: model.id, source, callId, attrs, at }).catch(() => {})
      }
    } catch {
      // The recording is best effort.
    }
    return finish(callId, call, { value: result })
  }

  // failed answers for a hook that failed (its .catch): pre-model only when the
  // call is known not to have reached the model and can no longer, else
  // after-model, and with no record of the call, neither.
  function failed(e, kind) {
    const reason = kindWhy(kind)
    // The hook was not run at all, so it holds no call of its own.
    if (kind === 're-entry') return { deny: PRE + reason }
    const callId = e?.meta?.callId
    const call = typeof callId === 'string' ? calls.get(callId) : undefined
    if (call === undefined) return { deny: UNKNOWN + reason }
    calls.delete(callId)
    if (call.phase === 'dispatched') return { deny: AFTER + reason }
    call.isCancelled = true
    return { deny: PRE + 'abandoned-' + reason }
  }

  return { serve, failed }
}
