// The agent types the engine answers: a plugin backs one of its own types with
// the data the engine needs to run it, and the registry lives in the session's
// state, so an engine reload keeps it. Only the state calls touch the mods API;
// the validation is pure.

// The registry is the state value { plugin: 'msgate', key: 'backedTypes' }: an
// object of backed types by full type ("<plugin>:<type>"). The reference is
// spelled at each call, which is how the engine lists the state a module touches.

const TYPE_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/
// The prompt mode each command reads its prompt in: a review reads a target, a
// follow reads the id of a run, and only those do.
const PROMPT_MODE_OF = { exec: 'free', review: 'review-target', follow: 'run-id' }
const SANDBOXES = ['read-only', 'workspace-write']
// A write that another one beat is made again on the new value; this many tries.
const WRITE_ATTEMPTS = 20

const isText = (value) => typeof value === 'string' && value !== ''
const isOptionalText = (value) => value === undefined || isText(value)

// The first field of a backType call that is not acceptable, or null.
function invalidField(args) {
  const call = typeof args === 'object' && args !== null ? args : {}
  if (typeof call.type !== 'string' || !TYPE_PATTERN.test(call.type)) return 'type'
  if (typeof call.command !== 'string' || !Object.hasOwn(PROMPT_MODE_OF, call.command)) return 'command'
  if (call.promptMode !== PROMPT_MODE_OF[call.command]) return 'promptMode'
  if (!isOptionalText(call.model)) return 'model'
  if (!isOptionalText(call.effort)) return 'effort'
  if (call.sandbox !== undefined && !SANDBOXES.includes(call.sandbox)) return 'sandbox'
  if (!isOptionalText(call.purpose)) return 'purpose'
  if (call.stepped !== undefined && typeof call.stepped !== 'boolean') return 'stepped'
  return null
}

// backedData checks a backType call and returns the data the registry keeps for
// it (the settings it did not name left out), or the field that is refused.
export function backedData(args) {
  const field = invalidField(args)
  if (field !== null) return { field }
  const data = { command: args.command, promptMode: args.promptMode }
  for (const name of ['model', 'effort', 'sandbox', 'purpose', 'stepped']) {
    if (args[name] !== undefined) data[name] = args[name]
  }
  return { data }
}

const isRegistry = (value) => typeof value === 'object' && value !== null && !Array.isArray(value)

// backedTypeOf is the data a full type is backed with, or null when it is not
// backed. A registry that is damaged backs nothing.
export async function backedTypeOf($, fullType) {
  const { value } = await $.state.get({ plugin: 'msgate', key: 'backedTypes' })
  if (!isRegistry(value) || !Object.hasOwn(value, fullType)) return null
  return value[fullType]
}

// backType records a full type with its data, replacing an earlier record of the
// same type. The write is conditional on the version read, so calls made
// together keep each other's types.
export async function backType($, fullType, data) {
  for (let attempt = 0; attempt < WRITE_ATTEMPTS; attempt += 1) {
    const { value, version } = await $.state.get({ plugin: 'msgate', key: 'backedTypes' })
    const types = isRegistry(value) ? value : {}
    const written = await $.state.set({ plugin: 'msgate', key: 'backedTypes' }, { ...types, [fullType]: data }, { ifVersion: version })
    if (written.isSet) return
  }
  throw new Error('the backed types were written by others ' + WRITE_ATTEMPTS + ' times in a row')
}
