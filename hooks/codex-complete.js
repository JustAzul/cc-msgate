// Completions answered by Codex, through `msgate complete`. Nothing here
// touches the mods API: the process comes in through a port, so the decisions
// can be read and tested on their own. The CLI writes the one model-call record
// of the call, so nothing here records it.
//
// The port is { bin, run }: `bin` the msgate executable, and `run(argv, init)`
// resolves { exitCode, stdout, stderr } once the process has exited. `init` is
// { stdin, signal }: the port stops the process when `signal` aborts, and
// resolves (or rejects) once it has. `spawnRun` makes such a `run` of a
// streaming spawn.

const PROVIDER = 'codex'
// The CLI's timeout when the caller asks for none, so a hung call always ends.
const DEFAULT_TIMEOUT_S = 600
const UNKNOWN = 'unknown'
// The kind of an API error that each class of failure `complete` reports reads as.
const ERRORS = new Map([
  ['rate-limit', 'rate_limit'],
  ['auth', 'authentication_failed'],
  ['quota', 'billing_error'],
])
// What `complete` says when it could not give an answer, and the call was cut.
const CUT = new Set(['timeout', 'cancelled'])

const isObject = (value) => typeof value === 'object' && value !== null

const count = (value) => (typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0)

const noUsage = () => ({ input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 })

const aborted = () => ({ isAnswered: false, reason: 'aborted', usage: noUsage() })

const apiError = (status, error, usage) => ({ isAnswered: false, reason: 'api-error', status, error, usage })

const isTextBlock = (block) => isObject(block) && typeof block.text === 'string' && (block.type === undefined || block.type === 'text')

// textOf is the text of a prompt or a system: a string as it is, a list of text
// blocks joined with nothing between them, and null for anything else.
function textOf(value) {
  if (typeof value === 'string') return value
  if (!Array.isArray(value) || !value.every(isTextBlock)) return null
  return value.map((block) => block.text).join('')
}

// isTextRequest tells a request Codex can answer: text alone, with a prompt
// that says something. Cache marks and the reply's token cap are not looked at.
export function isTextRequest(request) {
  if (request.tools !== undefined || request.images !== undefined) return false
  const prompt = textOf(request.prompt)
  if (prompt === null || prompt === '') return false
  return request.system === undefined || textOf(request.system) !== null
}

function pushFlag(argv, flag, value) {
  if (typeof value === 'string' && value !== '') argv.push(flag, value)
}

const hasTimeout = (request) => typeof request.timeoutMs === 'number' && request.timeoutMs > 0

// completeCommand is `msgate complete` for a request and the call it belongs to:
// { sessionId, runId?, source, callId, attrs }. The CLI takes a timeout in whole seconds.
export function completeCommand(bin, request, call) {
  const argv = [bin, 'complete', '--provider', PROVIDER, '--json']
  pushFlag(argv, '--model', request.model)
  pushFlag(argv, '--effort', request.effort)
  pushFlag(argv, '--session-id', call.sessionId)
  pushFlag(argv, '--run-id', call.runId)
  pushFlag(argv, '--source', call.source)
  pushFlag(argv, '--call-id', call.callId)
  for (const [key, value] of Object.entries(call.attrs)) argv.push('--attr', key + '=' + value)
  argv.push('--timeout', String(hasTimeout(request) ? Math.ceil(request.timeoutMs / 1000) : DEFAULT_TIMEOUT_S))
  return argv
}

// requestBody is what `msgate complete` reads on standard input: the prompt as
// the one user message, and the system text when there is any.
function requestBody(request) {
  const system = request.system === undefined ? '' : textOf(request.system)
  return { ...(system === '' ? {} : { system }), messages: [{ role: 'user', text: textOf(request.prompt) }] }
}

// usageOf is the usage of a completion in the service's own keys. Codex counts
// the cached tokens in its input and the reasoning ones in its output.
function usageOf(raw) {
  const reported = isObject(raw) ? raw : {}
  const cached = count(reported.cached_input_tokens)
  return {
    input_tokens: Math.max(0, count(reported.input_tokens) - cached),
    output_tokens: count(reported.output_tokens),
    cache_read_input_tokens: cached,
    cache_creation_input_tokens: 0,
  }
}

function parse(text) {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

const sdkResult = (status) => (CUT.has(status) ? aborted() : apiError(null, UNKNOWN, noUsage()))

const failureResult = (body, usage) => apiError(body.failure === 'rate-limit' ? 429 : null, ERRORS.get(body.failure) ?? UNKNOWN, usage)

// resultOf is the completion `msgate complete --json` printed. An exit code with
// no completion on the output, and an output no completion can be read from, are
// an API error with no status: the CLI's own failures say nothing of the API.
function resultOf(reply) {
  const body = parse(reply.stdout)
  if (!isObject(body)) return apiError(null, UNKNOWN, noUsage())
  if (typeof body.sdk_status === 'string') return sdkResult(body.sdk_status)
  const usage = usageOf(body.usage)
  if (reply.exitCode !== 0 || body.outcome === 'api-error') return failureResult(body, usage)
  if (body.outcome === 'answered' && typeof body.text === 'string') return { isAnswered: true, text: body.text, usage }
  if (body.outcome === 'empty-reply') return { isAnswered: false, reason: 'empty-reply', usage }
  return apiError(null, UNKNOWN, usage)
}

// codexComplete makes one completion by Codex and gives it as `$.model.complete`
// would: answered, empty-reply, api-error or aborted. `call` is { sessionId,
// runId?, source, callId, attrs, signal }: the run id, source and call id are
// the CLI's record of the call, and the signal stops the process. It rejects only when the
// process could not be run.
export async function codexComplete(ports, request, call) {
  if (call.signal.aborted) return aborted()
  const init = { stdin: JSON.stringify(requestBody(request)), signal: call.signal }
  let reply
  try {
    reply = await ports.run(completeCommand(ports.bin, request, call), init)
  } catch (error) {
    if (call.signal.aborted) return aborted()
    throw error
  }
  return call.signal.aborted ? aborted() : resultOf(reply)
}

// exitOf is the exit status of a spawned child; one a signal ended reads as 1, as
// it does for `$.process.run`.
const exitOf = (end) => (typeof end?.code === 'number' ? end.code : 1)

// drain reads a spawn's stream to its end: the pieces of both pipes, then the
// child's { code, signal }, which is the stream's return value.
async function drain(stream) {
  const out = { stdout: '', stderr: '' }
  for (;;) {
    const step = await stream.next()
    if (step.done) return { exitCode: exitOf(step.value), ...out }
    out[step.value.stream === 'stderr' ? 'stderr' : 'stdout'] += step.value.text
  }
}

// leave ends the loop of a stream, which stops the child.
async function leave(stream) {
  try {
    await stream.return()
  } catch {
    // The stream had ended already.
  }
}

// spawnRun makes the port's `run` of a streaming spawn, `$.process.spawn`'s
// shape: it collects the output, and when the signal aborts it leaves the
// stream and settles at once, without the output a stopped child had not
// finished. A signal that aborts after the stream ended leaves nothing to stop.
export function spawnRun(spawn) {
  return async function run(argv, init) {
    const stream = spawn({ argv, input: init.stdin })
    const draining = drain(stream)
    // A pull the stream settles after it was left must not go unhandled.
    draining.catch(() => {})
    let onAbort
    const cut = new Promise((resolve) => {
      onAbort = () => {
        void leave(stream)
        resolve({ exitCode: 1, stdout: '', stderr: '' })
      }
      if (init.signal.aborted) onAbort()
      else init.signal.addEventListener('abort', onAbort, { once: true })
    })
    try {
      return await Promise.race([draining, cut])
    } finally {
      init.signal.removeEventListener('abort', onAbort)
    }
  }
}
