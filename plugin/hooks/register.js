import { admitJob } from './admission.js'
import {
  POLL_MS,
  EXIT_RUN_NOT_FOUND,
  DELIVERY_PREFIX,
  admittedJob,
  attemptedRuns,
  isAbandoned,
  isSameQueue,
  deliveryKey,
  nextBatch,
  queueEntry,
  readQueue,
  withAttempt,
  withEntry,
  withoutRun,
  purposeOf,
  withPurpose,
  failureText,
  jobsCommand,
  jobsListing,
  noticeText,
  repeatedNoticeText,
  oldestFirst,
  readNotified,
  remember,
  runningCount,
  sessionJobs,
  shimPath,
  isTerminal,
  JOB_SOURCE,
  messageOf,
  modJobs,
  RESULT_OUTPUT_BYTES,
  resultNote,
  cutBytes,
  statusText,
  toastText,
} from './lib.js'
import {
  ADMIT_TIMEOUT_MS,
  AGENT_SOURCE,
  CONVERSATIONS_KEY,
  LOST_CONVERSATION,
  HEARTBEAT_MS,
  STEP_LIMIT,
  SLICE_TIMEOUT_MS,
  answerText,
  conversationOf,
  costText,
  finishedRun,
  firstTurnCommand,
  followUpCommand,
  handoffFailure,
  hasAnswered,
  heartbeatLine,
  isWaitTimeout,
  missingPrices,
  progressCommand,
  progressLine,
  readConversations,
  readProgress,
  rememberConversation,
  sessionStatsCommand,
  sliceWaitCommand,
  stepNumber,
  statusLine,
  terminalRunIds,
  turnPrompt,
  typeNameOf,
} from './subagent.js'
import {
  FULL_REFRESH,
  REFRESH_TIMEOUT_MS,
  SESSION_START_REFRESH,
  isRefreshedByCompaction,
  mergeRefreshRequests,
  refreshCommand,
  refreshOutcome,
} from './prices.js'
import { BAND_REFRESH_MS, bandLine, flaggedRuns, followedRuns, labelled, withProgress } from './band.js'
import { createLimiter } from './limiter.js'
import { createRecorder, finishOnStop } from './recorder.js'
import { summarySettings } from './settings.js'
import { createSummarizer } from './summarizer.js'
import { backedTypeOf } from './backed.js'
import { forgetEfforts, holdEffort, takeEffort } from './efforts.js'
import { FOLLOW, admitSpawn, checkRun, endedText, followFailureText, hiddenRunIds, hiddenRunOf, stoppedText } from './hidden.js'
import { PROGRESS_SPEC, PROGRESS_TOOL, onProgressTool } from './progress.js'
import { createService } from './service.js'
import { INTERFACE, createModelsCache, createVersionCache, serveBackType, serveFlaggedRuns, serveJobs, serveModels, serveRun, serveVersion } from './iface.js'
import { summaryRecording } from './summary.js'
import { modelCallCommand } from './modelcall.js'
import { ETA_TIMEOUT_MS, ETA_UNAVAILABLE, etaCommand, etaText, endedSince, finishedTail, isAnnounceable, listedRuns, spawnNotice } from './spawn.js'

const COMMAND = 'msgate-jobs'
const NO_TOOLS = 'msgate agents run no tools: a msgate job answers for them.'
// The band's lines: one for each run another caller flagged with
// --agent-feedback that is still going. Session state, so the band redraws on
// each write and a reload keeps what it shows.
const HOOK_RUNS = { plugin: 'msgate', key: 'hookRuns' }
// The runs already announced as spawned, newest last, kept across reloads.
const SPAWN_NOTIFIED = { plugin: 'msgate', key: 'spawnNotified' }
// A step's response: what the run is doing as thinking, then the answer.
const THINKING_BLOCK = 0
const ANSWER_BLOCK = 1

const TURN_PROPERTIES = {
  provider: { type: 'string', description: 'Provider to run (default: codex).' },
  purpose: { type: 'string', description: 'Label for the purpose of the run, such as code-review or delegation; it is recorded with the run and groups its statistics and ETA. It sets no model, effort or sandbox.' },
  model: { type: 'string', description: 'Model (default: the provider\'s own).' },
  effort: { type: 'string', description: 'Reasoning effort (default: the provider\'s own).' },
  sandbox: { type: 'string', description: 'Sandbox mode, read-only or workspace-write (default: read-only).' },
  cwd: { type: 'string', description: 'Working directory for the run (default: the session directory).' },
}

// The poll timer and its guard live for one load of the module; a reload
// evaluates this file again and starts from scratch.
let timer = null
let isPolling = false
// Whether the progress tool registered in this load; false until it does.
let isProgressToolRegistered = false
// The flagged runs the band follows, what was read of each, and the lines it
// last wrote (null until this load writes). Until this load's first poll has
// read the session's runs, the band keeps what an earlier load wrote.
let bandTimer = null
let followed = []
let shownBand = null
let hasPolledRuns = false
let isRefreshingBand = false
let isBandFailureLogged = false
let shownStatus = null
// The terminal runs of the session when its cost was last read, and that cost.
// A run's telemetry record lands just after its terminal state, so the cost is
// read when another run ends and once more on the next poll.
let shownTerminal = ''
let shownCost = undefined
let isCostRefreshDue = false
let isCostFailureLogged = false
// The price refresh: at most one runs at a time, and a request that arrives
// meanwhile waits in a single queued one. The models this load has asked a
// refresh for are not asked for again, so a model no refresh can price does not
// keep the refresh going.
let isRefreshing = false
let queuedRefresh = null
let isRefreshFailureLogged = false
let isPricesChanged = false
// The summary calls recorded with `usage add`, and how many of them the session's
// cost had counted when it was last read. A failed recording is logged once until
// one succeeds.
let usageAdds = 0
let readUsageAdds = 0
let isRecordFailureLogged = false
// The recordings that failed, oldest first, each tried again on every poll. The
// list holds at most MAX_PENDING_RECORDINGS; a recording that finds it full pushes
// out the oldest, which is lost for good and shown in a toast once per load.
const MAX_PENDING_RECORDINGS = 100
const LOST_RECORDINGS_TOAST = 'some summary costs could not be recorded; the Claude cost is a lower bound'
const pendingRecordings = []
let isLossToastShown = false
const pricedModels = new Set()
// The spinners already logged, by request id.
const loggedSpinners = new Set()
// Runs whose progress could not be read, each logged once.
const loggedProgressFailures = new Set()
// The full type of each agent id this load has looked up, from $.agent.list().
// Whether the type is backed is asked of the registry at each step, so a type
// backed later is answered from its next row on.
const agentTypes = new Map()
// The agents this load has found to be rows of a backed type.
const rowAgents = new Set()
// The hidden rows whose latest step the engine answered, by agent id; one it
// passed to the model beneath is not in the set.
const answeredHiddenRows = new Set()
// The steps each row's latest turn has made, by agent id.
const stepCounts = new Map()
// The agents whose turn is running in this load, by agent id: the run each
// follows and its type name. The spinner of such an agent says what the run does.
const turnAgents = new Map()
// The directory the session runs in, for the ETA lookups.
let sessionCwd = ''
// When this load started: a finished run in its first listing is announced only
// if it ended since.
let loadedAt = 0
let spawnTimer = null
let isScanningSpawns = false
// The runs the scans of this load have seen, and whether a failed ETA lookup has
// been logged in this load.
let seenRuns = new Set()
let isEtaFailureLogged = false
// Whether this load has read its first listing, which announces only the runs
// still going or ended since the load.
let hasSpawnBaseline = false
let isScanFailureLogged = false
// Whether this load has logged a status read that failed at admission, a
// notice that could not be shown, and a listed run it had to skip.
let isTurnReadFailureLogged = false
let isNoticeFailureLogged = false
let isSkippedRunLogged = false
// Writes to the announced runs are read, changed and written back one at a time.
let spawnWrites = Promise.resolve()
// The requests this load's summaries may have out at once, live and recorded
// alike, and the options the load was given.
let limiter = createLimiter()
let loadOptions = {}
// The notes on the options that this load has logged.
const loggedNotes = new Set()
// The progress summaries of this load, which the config of the load decides.
let summaries = createSummarizer({}, limiter)
// The msgate.complete calls this load is serving.
let service = createService()
// The version this load has read for the interface.
let versionCache = createVersionCache()
// Conversation records are read, changed and written back; one write at a time
// keeps two agents admitted together from dropping each other's record.
let conversationWrites = Promise.resolve()

export function register(on, options) {
  loadOptions = options ?? {}
  limiter = createLimiter()
  loggedNotes.clear()
  summaries = createSummarizer({ ...loadOptions, liveEvery: summarySettings({ options: loadOptions }).liveEvery }, limiter)
  pendingRecordings.length = 0
  answeredTurns.clear()
  agentTypes.clear()
  rowAgents.clear()
  answeredHiddenRows.clear()
  stepCounts.clear()
  startTexts.clear()
  forgetEfforts()
  isLossToastShown = false
  service = createService()
  versionCache = createVersionCache()
  // Only the spread and the noun: a step that fails unloads the whole plugin.
  on('engine.create', async (_$, e, next) => ({ ...(await next(e)), msgate: INTERFACE }))
  // The caller reads the denial's prefix to know whether the model was called.
  on('msgate.complete', ($, e, next) => service.serve(servicePorts($), e, { signal: next.signal, caller: next.origin.plugin })).catch((_$, e, next) =>
    service.failed(e, next.error.kind),
  )
  // The other methods answer from the caller the host stamped on the call; a hook
  // that fails gives the caller the refusal of its method, not the engine's failure.
  on('msgate.backType', ($, e, next) => serveBackType($, e, next.origin.plugin)).catch(() => ({ deny: 'msgate:backType:failed' }))
  on('msgate.version', ($, e, next) => serveVersion($, versionCache)).catch(() => ({ deny: 'msgate:version:failed' }))
  on('msgate.run', ($, e, next) => serveRun($, e, next.origin.plugin)).catch(() => ({ deny: 'msgate:run:failed' }))
  on('msgate.jobs', ($, e, next) => serveJobs($, e, next.origin.plugin)).catch(() => ({ deny: 'msgate:jobs:failed' }))
  on('msgate.flaggedRuns', ($) => serveFlaggedRuns($)).catch(() => ({ deny: 'msgate:flaggedRuns:failed' }))
  // The catalogs this load has read, which `register` makes anew with the load.
  const catalogs = createModelsCache()
  on('msgate.models', ($, e) => serveModels($, e, catalogs)).catch(() => ({ deny: 'msgate:models:failed' }))
  // The engine answers a hidden row only when the plugin that backed its type
  // spawned it, which only this hook sees. A hook that fails lets the spawn go on.
  on('agent.spawn', ($, e, next) => admitSpawn($, e, next.origin.plugin, next)).catch(($, e, next) => next(e))
  on('session.start', async ($, e, next) => {
    await registerTools($)
    if (timer !== null) timer.cancel()
    timer = $.clock.every(POLL_MS, () => {
      void poll($)
    })
    if (bandTimer !== null) bandTimer.cancel()
    bandTimer = $.clock.every(BAND_REFRESH_MS, () => {
      void refreshBand($)
    })
    sessionCwd = e.cwd
    loadedAt = await $.clock.now()
    seenRuns = new Set()
    isEtaFailureLogged = false
    hasSpawnBaseline = false
    isScanFailureLogged = false
    isTurnReadFailureLogged = false
    isNoticeFailureLogged = false
    isSkippedRunLogged = false
    await loadDeliveryState($)
    if (spawnTimer !== null) spawnTimer.cancel()
    spawnTimer = $.clock.every(BAND_REFRESH_MS, () => {
      void scanSpawns($)
    })
    requestRefresh($, SESSION_START_REFRESH)
    // Last, because a taken command name makes the call throw.
    try {
      await $.command.register({
        name: COMMAND,
        description: 'List the msgate jobs of this session',
        immediate: true,
      })
    } catch (error) {
      // The tools and notices work without the command.
      $.ui.log('could not register /' + COMMAND + ': ' + String(error && error.message ? error.message : error), { to: 'debug' })
    }
    return next(e)
  })

  // A compaction can change what a session costs by way of a model it has not
  // used before, so the prices are looked at again. The refresh runs on a timer:
  // the compaction never waits for it.
  on('session.compact', async ($, e, next) => {
    if (isRefreshedByCompaction(e)) requestRefresh($, FULL_REFRESH)
    return next(e)
  }).catch(($, e, next) => next(e))

  on('tool.call', { tool: 'mcp__msgate__run' }, async ($, e) => startTurn($, e))
  on('tool.call', { tool: 'mcp__msgate__jobs' }, async ($, e) => runJobsAction($, e))

  on('command.run', { command: 'msgate-jobs' }, async ($) => listJobs($))

  // Every way the hooks below can fail ends in an answer that says the
  // hand-off failed, never in a Claude model's answer.
  // The text a row's turn started with: its prompt when a session that saves no
  // transcript has no rows to read it from.
  on('turn.start', async ($, e, next) => {
    if ((await rowOf($, e.agentId)) !== null) startTexts.set(e.agentId, { turnId: e.turnId, text: e.text })
    return next(e)
  }).catch(($, e, next) => next(e))

  on('turn.step', async function* ($, e, next) {
    const row = await rowOf($, e.agentId)
    if (row === null) return yield* next(e)
    if (row.backed.command === FOLLOW) return yield* hiddenStep($, e, next, row)
    if (await isOverStepLimit($, e)) return yield* next(e)
    // A later step of a turn that has answered (the tool is missing in the
    // default permission mode, or the request repeats) needs no new run.
    const answered = answeredText(e)
    if (answered !== null) return yield* answer(e, answered)
    yield* opening($, row)
    const { text, records } = yield* followTurn($, { agentId: e.agentId, turnId: e.turnId, row }, next.signal)
    return yield* deliver(e, text, records)
  }).catch(async function* ($, e, next) {
    if (next.called || !isKnownSubagent(e.agentId)) return yield* next(e)
    // Block 1 is free: the recorded summaries stream inside the hand-back or the
    // answer that ends a step, which a failed hook never reached, so a step that
    // failed has streamed at most the opening line, in block 0.
    return yield* deliver(e, handoffFailure('the plugin hook failed: ' + failureOf(next.error)))
  })

  // The hand-back call and the progress marker are the only tools they may make:
  // the first is how their answer reaches the caller, the second ends a step.
  // An Agent call that names an effort is held with it, for the spawn beneath
  // that its row's first turn admits.
  on('tool.call', async ($, e, next) => {
    if (isRowTool(e.tool) || !(await isAnsweredRow($, e.agentId))) return holdEffort(e, next)
    return { deny: NO_TOOLS }
  }).catch(($, e, next) => (next.called || isRowTool(e.tool) || !isKnownSubagent(e.agentId) ? next(e) : { deny: NO_TOOLS }))

  // Registered after the hook above, which resolves a call first: it must let the
  // marker through for a row it answers.
  onProgressTool(on)

  // The spinner of a msgate agent's turn says what its run is doing.
  on('ui.render', { component: 'Spinner' }, async ($, e, next) => {
    if (!loggedSpinners.has(e.requestId)) {
      loggedSpinners.add(e.requestId)
      $.ui.log('Spinner raised for ' + e.requestId + ' with the word ' + e.props.word, { to: 'debug' })
    }
    const agent = turnAgents.get(e.requestId)
    // A message the engine set is not the run's to replace.
    if (agent === undefined || e.props.message != null) return next(e)
    const message = summaries.shown(agent.runId) ?? 'msgate · ' + agent.type
    return next({ ...e, props: { ...e.props, message } })
  }).catch(($, e, next) => next(e))

  // The band above the prompt shows what the runs other callers flagged are
  // doing. Only the person sees it; their results stay with their callers.
  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const { value } = await $.state.get(HOOK_RUNS)
    const rows = Array.isArray(value) ? value : []
    if (rows.length === 0) return next(e)
    // Above whatever else the band holds (a survey, another plugin's line),
    // never in its place.
    const below = await next(e)
    const { Box, Text } = $.ui.resolve(e)
    const lines = rows.map((row) => h(Text, { dimColor: true, wrap: 'truncate-end' }, row.text))
    return h(Box, { flexDirection: 'column' }, ...lines, ...(below ? [below] : []))
  }).catch(($, e, next) => next(e))
}

export async function registerTools($) {
  const tools = [
    {
      name: 'run',
      description:
        'Run a prompt on an agent CLI as a background job and return at once with {conversation_id, run_id, continued}. ' +
        'Without conversation_id it starts a new conversation; with one it sends the next turn of that conversation. ' +
        'A notice arrives in this session when the job finishes, so do not poll for it. ' +
        'A plugin that opens agent rows for flagged runs shows this run as an agent row until it ends. ' +
        'When an installed plugin already has an agent type for the purpose and the model you want, start that type with the Agent tool instead; ' +
        'use this tool only for what the Agent tool cannot set: sandbox, cwd, provider, or conversation_id to continue a conversation.',
      inputSchema: {
        type: 'object',
        properties: {
          prompt: { type: 'string', description: 'The prompt for the agent.' },
          conversation_id: { type: 'string', description: 'The conversation to continue (a run id of that conversation also works); leave out to start a new one.' },
          ...TURN_PROPERTIES,
        },
        required: ['prompt'],
      },
    },
    {
      name: 'jobs',
      description:
        'Inspect msgate jobs: list this session\'s recent runs, read the state of one run, read the output of a finished run, or cancel a run.',
      inputSchema: {
        type: 'object',
        properties: {
          action: { type: 'string', enum: ['list', 'status', 'result', 'cancel'], description: 'What to do.' },
          run_id: { type: 'string', description: 'The run to act on; required for status, result and cancel.' },
        },
        required: ['action'],
      },
    },
    PROGRESS_SPEC,
  ]
  isProgressToolRegistered = false
  for (const tool of tools) {
    try {
      await $.tool.register(tool)
      if (tool === PROGRESS_SPEC) isProgressToolRegistered = true
    } catch {
      // A tool that fails to register must not keep the others or the poll from starting.
    }
  }
}

// isProgressRegistered tells whether the progress tool is registered in this load
// of the module; a step is ended with it only when it is.
export function isProgressRegistered() {
  return isProgressToolRegistered
}

// startTurn serves the run tool.
async function startTurn($, e) {
  const admitted = await admitJob($, e, JOB_SOURCE)
  if (admitted.error !== undefined) return { deny: admitted.error }
  const { ids, sessionId, bin } = admitted
  await enqueueDelivery($, sessionId, queueEntry(ids, e.purpose, await $.clock.now()))
  startNotice($, () => notifyTurn($, bin, ids.run_id))
  return { result: JSON.stringify({ ...ids, continued: admitted.isContinuation }) }
}

// The delivery queues are read, changed and written back one write at a time,
// so two jobs admitted together, or a poll and an admission, keep each other's
// entries.
let queueWrites = Promise.resolve()

// updateQueue applies `change` to the stored queue of a session and writes the
// result back; a queue left empty is removed, and one the change left as it was
// is not written.
function updateQueue($, sessionId, change) {
  const write = queueWrites.then(async () => {
    const key = deliveryKey(sessionId)
    const queue = readQueue(await $.store.get(key))
    const next = change(queue)
    if (next.length === 0) await $.store.delete(key)
    else if (!isSameQueue(queue, next)) await $.store.set(key, next)
  })
  queueWrites = write.catch(() => {})
  return write
}

// The entries admitted in this load whose queue write failed, by run id, with
// the session each belongs to. The poll writes them again, and an entry still
// here when its run ends is delivered from here like a queued one.
const unwritten = new Map()
let isUnwrittenReported = false

// enqueueDelivery records an admitted job to be delivered when it ends. The job
// is admitted already: an entry that cannot be written is held and written again
// by every poll, never denied and never lost.
async function enqueueDelivery($, sessionId, entry) {
  try {
    await updateQueue($, sessionId, (queue) => withEntry(queue, entry))
  } catch (error) {
    unwritten.set(entry.run_id, { sessionId, entry })
    await reportUnwritten($, entry.run_id, messageOf(error))
  }
}

// reportUnwritten logs every entry that could not be written and shows the
// first of the load in a toast. It never throws.
async function reportUnwritten($, runId, failure) {
  const text = 'the finish notice of run ' + runId + ' could not be queued yet and will be retried'
  $.ui.log(text + ': ' + failure, { to: 'debug' })
  if (isUnwrittenReported) return
  isUnwrittenReported = true
  try {
    await $.ui.toast(text)
  } catch {
    // The log has it.
  }
}

// flushUnwritten tries to write the held entries into the stored queues.
async function flushUnwritten($) {
  for (const held of [...unwritten.values()]) await writeHeld($, held)
}

// writeHeld writes one held entry. The entry stays held when the write fails, or
// when a submit changed it meanwhile (the next poll writes it as it is then); one
// that was delivered meanwhile is taken out of the queue again.
async function writeHeld($, { sessionId, entry }) {
  try {
    await updateQueue($, sessionId, (queue) => withEntry(queue, entry))
  } catch (error) {
    return logDeliveryFailure($, 'could not write the job notice queue: ' + messageOf(error))
  }
  const current = unwritten.get(entry.run_id)
  if (current === undefined) await updateQueue($, sessionId, (queue) => withoutRun(queue, entry.run_id))
  else if (current.entry === entry) unwritten.delete(entry.run_id)
}

// changeEntry applies a change to the entry of a run in every place a copy can
// be: in memory while the entry is held, and in the stored queue, where a copy
// may exist whenever a write of the held entry has landed. A stored queue that
// cannot be written is tolerated only while a held copy keeps the change.
async function changeEntry($, sessionId, runId, change) {
  const held = unwritten.get(runId)
  if (held !== undefined) changeHeld(held, runId, change)
  try {
    await updateQueue($, sessionId, change)
  } catch (error) {
    if (!unwritten.has(runId)) throw error
  }
}

function changeHeld(held, runId, change) {
  const [next] = change([held.entry])
  if (next === undefined) unwritten.delete(runId)
  else unwritten.set(runId, { ...held, entry: next })
}

// heldQueue is the queue of a session as the poll works it: the stored entries,
// those held in memory in place of their stored copy.
async function heldQueue($, sessionId) {
  let queue = readQueue(await $.store.get(deliveryKey(sessionId)))
  for (const held of unwritten.values()) {
    if (held.sessionId === sessionId) queue = withEntry(queue, held.entry)
  }
  return queue
}

// startNotice runs the work of a notice from a timer, so it outlives the hook
// that admitted the run and nothing in the hook waits for it.
function startNotice($, work) {
  $.clock.after(0, () => {
    void work()
  })
}

// notifyTurn announces the run of the run tool with the purpose and source
// the run itself reports. A run whose status cannot be read is left to the scan.
// It never throws.
async function notifyTurn($, bin, runId) {
  let failure
  try {
    const reply = await $.process.run([bin, 'status', runId, '--json'])
    if (reply.exitCode === 0) {
      const run = JSON.parse(reply.stdout)
      const purpose = purposeOf(run)
      if (purpose !== undefined && typeof run.source === 'string') return notifyAdmitted($, bin, runId, purpose, run.source)
      failure = 'the run reports no purpose or source'
    } else {
      failure = failureText(reply)
    }
  } catch (error) {
    failure = messageOf(error)
  }
  if (isTurnReadFailureLogged) return
  isTurnReadFailureLogged = true
  $.ui.log('could not read run ' + runId + ' to announce it: ' + failure + '; the next scan announces it', { to: 'debug' })
}

// scanSpawns reads the session's runs and announces the ones it has not seen.
// It never overlaps itself.
async function scanSpawns($) {
  if (isScanningSpawns) return
  isScanningSpawns = true
  try {
    const bin = shimPath($.plugin.root)
    const reply = await $.process.run([bin, 'status', '--json', '--session-id', await $.session.id()])
    if (reply.exitCode !== 0) return logScanFailure($, failureText(reply))
    const listed = listedRuns(reply.stdout)
    if (listed === null) return logScanFailure($, 'the listing could not be read')
    isScanFailureLogged = false
    const isBaseline = !hasSpawnBaseline
    const claimed = await claimListed($, listed)
    hasSpawnBaseline = true
    for (const run of claimed) {
      if (!isTerminal(run.state)) await showSpawn($, run, () => lookupEta($, bin, run.purpose))
      else if (!isBaseline || endedSince(run, loadedAt)) await showSpawn($, run, () => finishedTail(run))
    }
  } catch (error) {
    logScanFailure($, messageOf(error))
  } finally {
    isScanningSpawns = false
  }
}

// claimListed claims the listed runs a notice can be written for, oldest first,
// and returns the ones this call claimed. Every run is claimed before any is
// announced, so a notice that fails cannot leave a finished run of the baseline
// to be announced later.
async function claimListed($, listed) {
  const claimed = []
  for (const run of oldestFirst(listed)) {
    if (!isAnnounceable(run)) {
      logSkippedRun($, run.run_id)
      continue
    }
    if (await claimSpawn($, run.run_id)) claimed.push(run)
  }
  return claimed
}

// logSkippedRun logs the first listed run of the load that had to be skipped;
// a later listing that gives it what it lacked announces it.
function logSkippedRun($, runId) {
  if (isSkippedRunLogged) return
  isSkippedRunLogged = true
  $.ui.log('skipped run ' + runId + ' in the spawn scan: the listing gives it no purpose, source or state', { to: 'debug' })
}

// logScanFailure logs a failed scan once until a scan succeeds; the next period
// tries again either way.
function logScanFailure($, failure) {
  if (isScanFailureLogged) return
  isScanFailureLogged = true
  $.ui.log('could not list the runs of the session for spawn notices: ' + failure, { to: 'debug' })
}

// showSpawn shows the notice of a listed run. A notice that cannot be shown is
// logged and does not stop the scan.
async function showSpawn($, run, tail) {
  try {
    await announceSpawn($, run.purpose, run.source, await tail())
  } catch (error) {
    logNoticeFailure($, run.purpose, error)
  }
}

// logNoticeFailure logs the first notice of the load that could not be shown.
function logNoticeFailure($, purpose, error) {
  if (isNoticeFailureLogged) return
  isNoticeFailureLogged = true
  $.ui.log('could not show the spawn notice of ' + purpose + ': ' + messageOf(error), { to: 'debug' })
}

// claimSpawn records a run as announced and tells whether this call is the one
// that did: false when this load or an earlier one already has.
async function claimSpawn($, runId) {
  if (seenRuns.has(runId)) return false
  seenRuns.add(runId)
  let isNew = true
  const write = spawnWrites.then(async () => {
    const announced = readNotified((await $.state.get(SPAWN_NOTIFIED)).value)
    if (announced.includes(runId)) {
      isNew = false
      return
    }
    await $.state.set(SPAWN_NOTIFIED, remember(announced, runId))
  })
  spawnWrites = write.catch(() => {})
  try {
    await write
  } catch (error) {
    // Without the record the run is announced once for this load.
    $.ui.log('could not record the spawn notice of run ' + runId + ': ' + messageOf(error), { to: 'debug' })
  }
  return isNew
}

// notifyAdmitted announces a run this mod admitted itself, unless the scan has
// already. It never throws.
async function notifyAdmitted($, bin, runId, purpose, source) {
  try {
    if (!(await claimSpawn($, runId))) return
    await announceSpawn($, purpose, source, await lookupEta($, bin, purpose))
  } catch (error) {
    logNoticeFailure($, purpose, error)
  }
}

async function announceSpawn($, purpose, source, tail) {
  const text = spawnNotice(purpose, source, tail)
  await $.ui.toast(text)
  await $.ui.log(text)
}

// lookupEta is the ETA part of a notice, or the unavailable text when it could
// not be read (logged once per load).
async function lookupEta($, bin, purpose) {
  let failure
  try {
    const reply = await $.process.run(etaCommand(bin, purpose, sessionCwd), { timeoutMs: ETA_TIMEOUT_MS })
    const text = reply.exitCode === 0 ? etaText(reply.stdout) : null
    if (text !== null) return text
    failure = reply.exitCode === 0 ? 'unreadable output' : failureText(reply)
  } catch (error) {
    failure = messageOf(error)
  }
  if (!isEtaFailureLogged) {
    isEtaFailureLogged = true
    $.ui.log('could not read the ETA of ' + purpose + ': ' + failure, { to: 'debug' })
  }
  return ETA_UNAVAILABLE
}

// runJobsAction serves the jobs tool.
async function runJobsAction($, e) {
  const sessionId = await $.session.id()
  const built = jobsCommand(shimPath($.plugin.root), sessionId, e)
  if (built.error) return { deny: built.error }
  let reply
  try {
    reply = await $.process.run(built.argv)
  } catch (error) {
    return { deny: 'msgate could not run: ' + messageOf(error) }
  }
  if (reply.exitCode !== 0) return { deny: failureText(reply) }
  return jobsAnswer($, e, reply)
}

// jobsAnswer is the jobs tool's answer to an action whose command exited 0.
async function jobsAnswer($, e, reply) {
  if (e.action === 'cancel') return { result: reply.stdout.trim() || 'cancellation requested for ' + e.run_id }
  if (e.action !== 'result') return { result: reply.stdout }

  const cut = cutBytes(reply.stdout, RESULT_OUTPUT_BYTES)
  if (!cut.isCut) return { result: reply.stdout || reply.stderr.trim() || 'The run produced no output.' }
  return { result: cut.text + '\n' + resultNote(await outputPathOf($, shimPath($.plugin.root), e.run_id)) }
}

// listJobs serves /msgate-jobs.
async function listJobs($) {
  try {
    const sessionId = await $.session.id()
    const reply = await $.process.run([shimPath($.plugin.root), 'status', '--json', '--session-id', sessionId])
    if (reply.exitCode !== 0) return { text: 'Could not list jobs. ' + failureText(reply) }
    return { text: jobsListing(sessionJobs(reply.stdout)) }
  } catch (error) {
    return { text: 'Could not list jobs. ' + String(error && error.message ? error.message : error) }
  }
}

// poll runs once per period and never overlaps itself: a period that arrives
// while the previous one is still working is skipped.
async function poll($) {
  if (isPolling) return
  isPolling = true
  try {
    await quietly(() => flushUnwritten($))
    await quietly(() => checkJobs($))
    await quietly(() => deliverQueue($))
  } finally {
    isPolling = false
  }
}

// quietly runs one part of the poll; a part that throws leaves the others to
// run, and the next period tries again.
async function quietly(work) {
  try {
    await work()
  } catch {
    // The next period tries again.
  }
}

async function checkJobs($) {
  const bin = shimPath($.plugin.root)
  const sessionId = await $.session.id()
  const reply = await $.process.run([bin, 'status', '--json', '--session-id', sessionId])
  if (reply.exitCode !== 0) return
  const jobs = modJobs(sessionJobs(reply.stdout))

  const hidden = await hiddenRunIds($)
  setFollowed(followedRuns(followed, flaggedRuns(reply.stdout).filter((run) => !hidden.has(run.run_id))), hidden)
  hasPolledRuns = true
  await refreshBand($)
  // Before the costs are read, so a recording that lands now is counted by them.
  await retryRecordings($)
  await showStatus($, bin, sessionId, runningCount(jobs), terminalRunIds(reply.stdout))
}

// The runs whose notice may already have reached the session in an earlier load,
// and whether this load has logged a failed read of a run's status.
let possiblyRepeated = new Set()
let isDeliveryFailureLogged = false
// The last entry the previous poll read, which the next batch starts after.
let readCursor = null
// The runs whose pruning has been logged.
const loggedPruned = new Set()

// loadDeliveryState starts the delivery of a load: the runs the queue holds a
// tried delivery for were possibly announced by an earlier load, and the queues
// of other sessions that nobody has touched for long are removed. It never
// throws.
async function loadDeliveryState($) {
  isDeliveryFailureLogged = false
  isUnwrittenReported = false
  readCursor = null
  possiblyRepeated = new Set()
  try {
    const sessionId = await $.session.id()
    possiblyRepeated = attemptedRuns(readQueue(await $.store.get(deliveryKey(sessionId))))
    await removeAbandoned($, deliveryKey(sessionId))
  } catch (error) {
    $.ui.log('could not load the job notice queue: ' + messageOf(error), { to: 'debug' })
  }
}

// removeAbandoned removes every queue but `ownKey` whose newest entry was
// admitted too long ago, and logs how many there were.
async function removeAbandoned($, ownKey) {
  const now = await $.clock.now()
  let removed = 0
  for (const key of await $.store.keys()) {
    if (!key.startsWith(DELIVERY_PREFIX) || key === ownKey) continue
    if (!isAbandoned(readQueue(await $.store.get(key)), now)) continue
    await $.store.delete(key)
    removed += 1
  }
  if (removed > 0) $.ui.log('removed ' + removed + ' abandoned job notice queue' + (removed === 1 ? '' : 's'), { to: 'debug' })
}

// deliverQueue works the session's queue: the oldest pending entries, each by
// reading its run's status. An entry that fails stays for the next period.
async function deliverQueue($) {
  const bin = shimPath($.plugin.root)
  const sessionId = await $.session.id()
  const batch = nextBatch(await heldQueue($, sessionId), readCursor)
  if (batch.length > 0) readCursor = batch[batch.length - 1]
  for (const entry of batch) await deliverEntry($, bin, sessionId, entry)
}

// deliverEntry delivers the notice of one queued run once the run is terminal.
// It never throws.
async function deliverEntry($, bin, sessionId, entry) {
  if (submitting.has(entry.run_id)) return
  try {
    const reply = await $.process.run([bin, 'status', entry.run_id, '--json'])
    if (reply.exitCode === EXIT_RUN_NOT_FOUND) return await dropPruned($, sessionId, entry.run_id)
    if (reply.exitCode !== 0) return logDeliveryFailure($, failureText(reply))
    const run = withPurpose(JSON.parse(reply.stdout))
    if (isTerminal(run.state)) await deliverRun($, bin, sessionId, run)
  } catch (error) {
    logDeliveryFailure($, messageOf(error))
  }
}

// The runs whose notice is being submitted: the session takes a prompt only
// when Claude's turn ends, so a submit can stay pending for as long as the turn
// lasts, and no tick waits for it or submits the same run again meanwhile.
const submitting = new Set()

// deliverRun marks the attempt before it submits the notice, so a load that
// dies in between leaves the mark for the next load to read. The submit runs on
// its own: the entry keeps its mark until the submit settles.
async function deliverRun($, bin, sessionId, run) {
  const attemptedAt = await $.clock.now()
  await changeEntry($, sessionId, run.run_id, (queue) => withAttempt(queue, run.run_id, attemptedAt))
  const noticeOf = possiblyRepeated.has(run.run_id) ? repeatedNoticeText : noticeText
  const text = noticeOf(run, await readOutput($, bin, run.run_id))
  submitting.add(run.run_id)
  void settleSubmit($, sessionId, run, text)
}

// settleSubmit ends the submit of a notice. An accepted notice ends the entry; any
// other end puts the entry back to pending for the next tick. It never throws.
async function settleSubmit($, sessionId, run, text) {
  try {
    if (await isSubmitted($, text)) {
      await changeEntry($, sessionId, run.run_id, (queue) => withoutRun(queue, run.run_id))
      await $.ui.toast(toastText(run))
    } else {
      await changeEntry($, sessionId, run.run_id, (queue) => withAttempt(queue, run.run_id, null))
    }
  } catch (error) {
    logDeliveryFailure($, messageOf(error))
  } finally {
    submitting.delete(run.run_id)
  }
}

// isSubmitted tells whether the prompt entered or was queued; a refusal and an
// error both mean it did not.
async function isSubmitted($, text) {
  try {
    const result = await $.prompt.submit({ text })
    return typeof result?.text === 'string'
  } catch {
    return false
  }
}

// dropPruned removes the entry of a run msgate no longer has, and logs it once.
async function dropPruned($, sessionId, runId) {
  await changeEntry($, sessionId, runId, (queue) => withoutRun(queue, runId))
  if (loggedPruned.has(runId)) return
  loggedPruned.add(runId)
  $.ui.log('run ' + runId + ' is no longer known to msgate; its notice is dropped', { to: 'debug' })
}

// logDeliveryFailure logs the first failed read of the load.
function logDeliveryFailure($, failure) {
  if (isDeliveryFailureLogged) return
  isDeliveryFailureLogged = true
  $.ui.log('could not deliver a job notice: ' + failure + '; the next period tries again', { to: 'debug' })
}

async function showStatus($, bin, sessionId, running, terminal) {
  const hasRunEnded = terminal !== shownTerminal
  if (hasRunEnded || isCostRefreshDue || isPricesChanged || usageAdds !== readUsageAdds) {
    // Taken before the read: a refresh that changes the prices, or a call that is
    // recorded, during it is read by the next poll.
    const hadPricesChanged = isPricesChanged
    const addsBeforeRead = usageAdds
    isPricesChanged = false
    const cost = await sessionCost($, bin, sessionId)
    // A failed read leaves the key, so the next poll reads again.
    if (cost === null) {
      isPricesChanged = isPricesChanged || hadPricesChanged
    } else {
      isCostRefreshDue = hasRunEnded
      shownTerminal = terminal
      readUsageAdds = addsBeforeRead
      shownCost = cost
    }
  }
  const text = statusLine(statusText(running), shownCost)
  if (text !== shownStatus) {
    shownStatus = text
    $.ui.status(text)
  }
}

// sessionCost is the status line's cost text, undefined when no run could be
// priced, or null when the cost could not be read (logged once), the run of
// msgate itself failing included.
async function sessionCost($, bin, sessionId) {
  let failure
  try {
    const reply = await $.process.run(sessionStatsCommand(bin, sessionId))
    if (reply.exitCode === 0) {
      isCostFailureLogged = false
      askForPrices($, missingPrices(reply.stdout))
      return costText(reply.stdout)
    }
    failure = failureText(reply)
  } catch (error) {
    failure = messageOf(error)
  }
  if (!isCostFailureLogged) {
    isCostFailureLogged = true
    $.ui.log('could not read the session cost: ' + failure, { to: 'debug' })
  }
  return null
}

// refreshBand reads what each followed run did since the last read and shows
// the latest of it in the band, dropping a run once it ends. A failed read
// leaves the run as it was, and a failed refresh is logged once until one
// succeeds; the next refresh tries again either way.
async function refreshBand($) {
  if (!isBandDue()) return
  isRefreshingBand = true
  try {
    await dropHidden($)
    const read = await readFollowed($)
    // Applied to the list as it is now: a poll may have changed it meanwhile.
    setFollowed(withProgress(followed, read))
    const labels = await bandLabels($, read)
    const now = await $.clock.now()
    await showBand($, followed.map((run) => ({ run_id: run.run_id, text: bandLine(labelled(run, labels.get(run.run_id)), now) })))
    isBandFailureLogged = false
  } catch (error) {
    logBandFailure($, error)
  } finally {
    isRefreshingBand = false
  }
}

// dropHidden lets the band go of the runs that have a hidden row: the row shows
// them from then on.
async function dropHidden($) {
  const hidden = await hiddenRunIds($)
  if (followed.some((run) => hidden.has(run.run_id))) setFollowed(followed.filter((run) => !hidden.has(run.run_id)), hidden)
}

// isBandDue tells that a refresh has work: the first poll has read the session's
// runs, no refresh is running, and the band follows a run or still shows one.
function isBandDue() {
  return hasPolledRuns && !isRefreshingBand && (followed.length > 0 || shownBand !== '[]')
}

// readFollowed reads what each followed run did since its last read.
async function readFollowed($) {
  const bin = shimPath($.plugin.root)
  const read = new Map()
  for (const run of followed) read.set(run.run_id, await newProgress($, bin, run.run_id, run.from))
  return read
}

// bandLabels is what each followed run shows in place of its newest raw step
// while summaries are active: its label, or null before one. A run whose
// summaries are not active has no entry and keeps its raw step.
async function bandLabels($, read) {
  const labels = new Map()
  for (const run of followed) {
    if (!(await summaries.observe(summaryPorts($), run.run_id, read.get(run.run_id)?.text ?? []))) continue
    labels.set(run.run_id, summaries.shown(run.run_id))
  }
  return labels
}

function logBandFailure($, error) {
  if (isBandFailureLogged) return
  isBandFailureLogged = true
  $.ui.log('could not update the band above the prompt: ' + messageOf(error), { to: 'debug' })
}

// setFollowed replaces the runs the band follows; a run it lets go of keeps no
// summaries, unless it passed to a hidden row (`handedOver` holds those), whose
// follow carries them on.
function setFollowed(next, handedOver = new Set()) {
  for (const run of followed) {
    if (!next.some((kept) => kept.run_id === run.run_id) && !handedOver.has(run.run_id)) summaries.forget(run.run_id)
  }
  followed = next
}

async function showBand($, rows) {
  const text = JSON.stringify(rows)
  if (text === shownBand) return
  await $.state.set(HOOK_RUNS, rows)
  shownBand = text
}

// askForPrices asks for one refresh for the models that have no price in the
// cache yet, each model once per load.
function askForPrices($, models) {
  const unasked = models.filter((model) => !pricedModels.has(model))
  if (unasked.length === 0) return
  for (const model of unasked) pricedModels.add(model)
  requestRefresh($, FULL_REFRESH)
}

// requestRefresh asks for a price refresh and returns at once: the refresh runs
// from a timer, never inside the hook or the poll that asked. Requests that
// reach it before it starts, or while one runs, come to a single queued one.
function requestRefresh($, request) {
  queuedRefresh = mergeRefreshRequests(queuedRefresh, request)
  if (isRefreshing) return
  isRefreshing = true
  $.clock.after(0, () => {
    void runRefreshes($)
  })
}

async function runRefreshes($) {
  try {
    while (queuedRefresh !== null) {
      const request = queuedRefresh
      queuedRefresh = null
      await refreshPrices($, request)
    }
  } finally {
    isRefreshing = false
  }
}

// refreshPrices runs one refresh. A change to the prices makes the next poll
// read the session's cost again; a failure is logged once until one succeeds.
async function refreshPrices($, request) {
  let outcome
  try {
    const reply = await $.process.run(refreshCommand(shimPath($.plugin.root), request), { timeoutMs: REFRESH_TIMEOUT_MS })
    outcome = refreshOutcome(reply)
  } catch (error) {
    outcome = { failure: messageOf(error), hasChanged: false }
  }

  if (outcome.failure === null) {
    isRefreshFailureLogged = false
    if (outcome.hasChanged) isPricesChanged = true
    return
  }
  if (!isRefreshFailureLogged) {
    isRefreshFailureLogged = true
    $.ui.log('could not refresh the price list: ' + outcome.failure, { to: 'debug' })
  }
}

async function readOutput($, bin, runId) {
  try {
    const reply = await $.process.run([bin, 'result', runId])
    return reply.exitCode === 0 ? reply.stdout : null
  } catch {
    return null
  }
}

// outputPathOf looks up where a run's output file is, or null when it cannot.
async function outputPathOf($, bin, runId) {
  try {
    const reply = await $.process.run([bin, 'status', runId, '--json'])
    if (reply.exitCode !== 0) return null
    const path = JSON.parse(reply.stdout).output_path
    return typeof path === 'string' && path !== '' ? path : null
  } catch {
    return null
  }
}

// rowOf describes the agent whose loop an event runs in when it is a row of a
// backed type, whoever spawned it: its full type, the type's own name and what
// it is backed with. It is null for the main loop and for any other agent.
async function rowOf($, agentId) {
  if (agentId === undefined) return null
  const fullType = await typeOfAgent($, agentId)
  if (fullType === null) return null
  const backed = await backedTypeOf($, fullType)
  if (backed === null) return null
  rowAgents.add(agentId)
  return { fullType, name: typeNameOf(fullType), backed }
}

// isAnsweredRow tells that the engine answers the agent's turns, so the agent has
// no tools: a row of a backed type, except a hidden row the engine passed to the
// model beneath, which keeps its tools.
async function isAnsweredRow($, agentId) {
  const row = await rowOf($, agentId)
  return row !== null && (row.backed.command !== FOLLOW || answeredHiddenRows.has(agentId))
}

// typeOfAgent is the full type of an agent, or null when the session lists no
// such agent (asked again at its next event).
async function typeOfAgent($, agentId) {
  if (agentTypes.has(agentId)) return agentTypes.get(agentId)
  const listed = (await $.agent.list()).find((agent) => agent.id === agentId)
  if (listed === undefined || typeof listed.type !== 'string') return null
  agentTypes.set(agentId, listed.type)
  return listed.type
}

// isOverStepLimit counts the step of a row's turn and tells whether it is past
// the limit; the first step past it is logged, once for the turn.
async function isOverStepLimit($, e) {
  const number = stepNumber(stepCounts, e.agentId, e.turnId)
  if (number === STEP_LIMIT + 1) {
    await $.ui.log(
      'agent ' + e.agentId + ' reached the step limit of ' + STEP_LIMIT + ' in turn ' + e.turnId + ': the model beneath the plugin answers the rest of the turn',
      { to: 'debug' },
    )
  }
  return number > STEP_LIMIT
}

// summaryConfig is what the options and the environment say of the summaries,
// read at each use so the variable can change while a session runs; a note is
// logged once per load. A variable that cannot be read leaves the options in force.
async function summaryConfig($) {
  let env
  try {
    env = await $.env.get('MSGATE_SUMMARIES')
  } catch {
    env = undefined
  }
  const settings = summarySettings({ options: loadOptions, env })
  for (const note of settings.notes) {
    if (loggedNotes.has(note)) continue
    loggedNotes.add(note)
    await $.ui.log(note, { to: 'debug' })
  }
  return settings
}

// summaryPorts is what the summaries use of the engine.
function summaryPorts($) {
  return {
    isEnabled: async () => (await summaryConfig($)).isEnabled,
    surfaces: () => $.session.surfaces(),
    now: () => $.clock.now(),
    complete: (request) => $.model.complete(request),
    record: (usage, runId) => recordSummary($, usage, runId),
    log: (text) => $.ui.log(text, { to: 'debug' }),
    labelSettled: (runId) => {
      if ([...turnAgents.values()].some((agent) => agent.runId === runId)) $.ui.invalidate('ui.render')
    },
  }
}

// servicePorts is what the msgate.complete service uses of the engine.
function servicePorts($) {
  return {
    sessionId: () => $.session.id(),
    now: () => $.clock.now(),
    haikuPin: () => $.env.get('ANTHROPIC_DEFAULT_HAIKU_MODEL'),
    complete: (request, signal) => $.model.complete(request, { signal }),
    record: (recording) => recordCall($, recording),
  }
}

// recordSummary hands a billed summary call to `usage add`, for the session that
// is running it now: the recording keeps that session, whatever a retry meets.
async function recordSummary($, usage, runId) {
  let sessionId
  try {
    sessionId = await $.session.id()
  } catch (error) {
    return logRecordFailure($, messageOf(error))
  }
  await recordCall($, summaryRecording(usage, sessionId, runId))
}

// recordCall hands a recording to `usage add`. It never throws; a recording that
// fails is kept to be tried again, and its failure is logged once until one
// recording succeeds.
async function recordCall($, recording) {
  const failure = await addUsage($, recording)
  if (failure === null) return
  keepRecording($, recording)
  logRecordFailure($, failure)
}

// addUsage runs `usage add` once for the recording and returns null when it
// recorded the call, else why it did not.
async function addUsage($, recording) {
  try {
    const argv = modelCallCommand(shimPath($.plugin.root), recording)
    const reply = await $.process.run(argv, { stdin: JSON.stringify(recording.usage) })
    if (reply.exitCode !== 0) return failureText(reply)
    isRecordFailureLogged = false
    usageAdds += 1
    return null
  } catch (error) {
    return messageOf(error)
  }
}

function logRecordFailure($, failure) {
  if (isRecordFailureLogged) return
  isRecordFailureLogged = true
  $.ui.log('could not record the usage of a summary call: ' + failure, { to: 'debug' })
}

// keepRecording adds a failed recording at the end of the list, pushing out the
// oldest when the list is full.
function keepRecording($, recording) {
  if (pendingRecordings.length >= MAX_PENDING_RECORDINGS) {
    pendingRecordings.shift()
    reportLostRecording($)
  }
  pendingRecordings.push(recording)
}

function reportLostRecording($) {
  $.ui.log('dropped the oldest pending summary recording: the list is full', { to: 'debug' })
  if (isLossToastShown) return
  isLossToastShown = true
  $.ui.toast(LOST_RECORDINGS_TOAST)
}

// retryRecordings tries the pending recordings again, oldest first, and stops at
// the first that fails again, which stays first.
async function retryRecordings($) {
  for (const recording of [...pendingRecordings]) {
    const failure = await addUsage($, recording)
    if (failure !== null) return logRecordFailure($, failure)
    // A recording kept meanwhile may have pushed this one out of the list.
    const index = pendingRecordings.indexOf(recording)
    if (index !== -1) pendingRecordings.splice(index, 1)
  }
}

function isKnownSubagent(agentId) {
  return agentId !== undefined && rowAgents.has(agentId)
}

// opening is the line that tells what a row's step is, unless the summaries are
// off: then the row shows only what the provider emitted.
async function* opening($, row) {
  if ((await summaryConfig($)).isEnabled) yield thinking('msgate · ' + row.name)
}

function thinking(line) {
  return { kind: 'thinking', index: THINKING_BLOCK, text: line + '\n' }
}

// The tool that delivers a subagent's final report to its caller. In the auto
// permission mode plain text is not delivered, and the call ends the subagent.
const HANDBACK_TOOL = 'SubagentHandback'

// isRowTool tells the tools a row whose turns the engine answers may call: the
// hand-back, and the marker that ends a step.
function isRowTool(tool) {
  return tool === HANDBACK_TOOL || tool === PROGRESS_TOOL
}

// deliver remembers the text a turn answered with, then hands it back, behind
// the recorded summaries of the turn's run.
async function* deliver(e, text, records = []) {
  rememberAnswer(e, text)
  return yield* handback(e, text, records)
}

// The text each agent's latest turn answered with, by agent id. A new turn of
// the agent replaces the entry, so the map holds one turn per agent.
const answeredTurns = new Map()

function rememberAnswer(e, text) {
  answeredTurns.set(e.agentId, { turnId: e.turnId, index: e.index, text })
}

// answeredText is what the turn of a step has already answered with, or null
// when the step is the turn's first.
function answeredText(e) {
  const earlier = answeredTurns.get(e.agentId)
  return earlier !== undefined && earlier.turnId === e.turnId && e.index > earlier.index ? earlier.text : null
}

// The prompt each msgate agent's latest turn started with, by agent id, as
// the turn.start event carried it. A new turn replaces the entry.
const startTexts = new Map()

// startTextOf is the prompt the given turn started with, or '' when no start was
// heard for that turn: a text kept for an earlier turn is never its prompt.
function startTextOf(agentId, turnId) {
  const started = startTexts.get(agentId)
  return started !== undefined && started.turnId === turnId ? started.text : ''
}

// handback ends the step with a call to the hand-back tool carrying the answer.
// The recorded summaries of the turn's run, when it has any, come before it, so
// the call is the step's last block.
async function* handback(e, text, records = []) {
  const input = { message: text }
  const streamed = yield* recordBlocks(records)
  const block = ANSWER_BLOCK + records.length
  yield { kind: 'tool', index: block, id: 'msgate-handback-' + e.turnId + '-' + e.index, name: HANDBACK_TOOL }
  yield { kind: 'input', index: block, json: JSON.stringify(input) }
  yield { kind: 'stop', stopReason: 'tool_use', usage: null }
  return { turnId: e.turnId, index: e.index, answer: streamed, toolUses: [{ name: HANDBACK_TOOL, input }], stopReason: 'tool_use', usage: null }
}

// recordBlocks streams the recorded summaries of the step's run as text, one
// block each from ANSWER_BLOCK on, and returns the text it streamed. The step's
// answer or hand-back follows them.
async function* recordBlocks(records) {
  let streamed = ''
  for (const [i, record] of records.entries()) {
    yield { kind: 'text', index: ANSWER_BLOCK + i, text: record.text }
    streamed += record.text
  }
  return streamed
}

// answer streams the answer block and ends the step, as a model's reply would.
// The recorded summaries of the step's run, when it has any, come before it.
async function* answer(e, text, records = []) {
  const streamed = yield* recordBlocks(records)
  yield { kind: 'text', index: ANSWER_BLOCK + records.length, text }
  yield { kind: 'stop', stopReason: 'end_turn', usage: null }
  return { turnId: e.turnId, index: e.index, answer: streamed + text, toolUses: [], stopReason: 'end_turn', usage: null }
}

// hiddenStep answers a step of a hidden row: it follows the run the row was
// registered with and ends the turn with plain text, without admitting a run. A
// later step of the turn, which the host calls again after plain text in the
// auto permission mode, ends with the stop alone so the transcript does not
// repeat the answer. A row that is not registered, or whose run the engine does
// not answer for, passes to the model beneath.
async function* hiddenStep($, e, next, row) {
  if (answeredText(e) !== null) {
    yield { kind: 'stop', stopReason: 'end_turn', usage: null }
    return { turnId: e.turnId, index: e.index, answer: '', toolUses: [], stopReason: 'end_turn', usage: null }
  }

  const runId = await hiddenRunOf($, e.agentId)
  const bin = shimPath($.plugin.root)
  const checked = runId === null ? { isPassed: true } : await checkRun($, bin, runId)
  if (checked.isPassed) {
    answeredHiddenRows.delete(e.agentId)
    return yield* next(e)
  }

  answeredHiddenRows.add(e.agentId)
  yield* opening($, row)
  const { text, records } =
    checked.error === undefined ? yield* followHidden($, bin, { agentId: e.agentId, row }, runId, next.signal) : { text: handoffFailure(checked.error), records: [] }
  rememberAnswer(e, text)
  return yield* answer(e, text, records)
}

// followHidden shows a hidden row's run as it works and returns the text that
// ends the turn, with the summaries recorded for the run. Stopping the row ends
// the turn and leaves the run going.
async function* followHidden($, bin, turn, runId, signal) {
  turnAgents.set(turn.agentId, { runId, type: turn.row.name })
  let records = []
  try {
    records = yield* watchRun($, bin, runId, signal)
    if (signal.aborted) return { text: stoppedText(runId), records }
    const read = await readFinished($, bin, runId)
    return { text: read.failure === undefined ? endedText(read.run) : read.failure, records }
  } catch (error) {
    return { text: followFailureText(error, runId, signal), records }
  } finally {
    turnAgents.delete(turn.agentId)
  }
}

// followTurn shows the turn's run as it works and returns the answer, with the
// summaries recorded for the run. `turn` names the agent, the turn and the
// agent's row.
async function* followTurn($, turn, signal) {
  try {
    return yield* runSubagentTurn($, turn, signal)
  } catch (error) {
    return { text: handoffFailure(messageOf(error)), records: [] }
  } finally {
    turnAgents.delete(turn.agentId)
  }
}

async function* runSubagentTurn($, turn, signal) {
  const rows = await $.session.messages({ agentId: turn.agentId })
  if (!Array.isArray(rows)) return failedTurn('could not read the prompt: ' + rows.deny)

  const bin = shimPath($.plugin.root)
  const built = await subagentCommand($, bin, turn, rows)
  if (built.error) return failedTurn(built.error)

  const reply = await $.process.run(built.argv, { stdin: built.stdin, timeoutMs: ADMIT_TIMEOUT_MS })
  if (reply.exitCode !== 0) return failedTurn(failureText(reply))
  const ids = admittedJob(reply.stdout)
  if (ids === null) return failedTurn('msgate printed an unexpected admission result: ' + reply.stdout.trim().slice(0, 500))

  startNotice($, () => notifyAdmitted($, bin, ids.run_id, turn.row.name, AGENT_SOURCE))
  turnAgents.set(turn.agentId, { runId: ids.run_id, type: turn.row.name })
  await recordConversation($, turn.agentId, ids.conversation_id)
  const records = yield* followOwnRun($, bin, ids.run_id, signal)
  try {
    return { text: await finalAnswer($, bin, ids.run_id), records }
  } catch (error) {
    return { text: handoffFailure(messageOf(error)), records }
  }
}

// failedTurn is the end of a turn that could not start its run: the hand-off
// failure, with nothing recorded.
function failedTurn(detail) {
  return { text: handoffFailure(detail), records: [] }
}

// subagentCommand starts the conversation on the agent's first turn and
// continues it on every later one (a SendMessage to the agent).
async function subagentCommand($, bin, turn, rows) {
  const sessionId = await $.session.id()
  const prompt = turnPrompt(rows, startTextOf(turn.agentId, turn.turnId))
  const conversationId = conversationOf(readConversations(await $.store.get(CONVERSATIONS_KEY)), turn.agentId)
  if (conversationId !== null) return followUpCommand(bin, sessionId, conversationId, prompt)
  if (hasAnswered(rows)) return { error: LOST_CONVERSATION }
  return firstTurnCommand(turn.row, bin, sessionId, prompt, await takeEffort($, turn.agentId))
}

async function recordConversation($, agentId, conversationId) {
  const write = conversationWrites.then(async () => {
    const records = readConversations(await $.store.get(CONVERSATIONS_KEY))
    await $.store.set(CONVERSATIONS_KEY, rememberConversation(records, agentId, conversationId))
  })
  conversationWrites = write.catch(() => {})
  try {
    await write
  } catch (error) {
    // The run is already admitted: answer it, and only a follow-up loses out.
    $.ui.log('could not record the conversation of agent ' + agentId + ': ' + messageOf(error), { to: 'debug' })
  }
}

// followOwnRun watches a run the agent itself started. Interrupting the turn
// (Esc, or TaskStop on the agent) cancels the run, including one interrupted
// before the wait began.
async function* followOwnRun($, bin, runId, signal) {
  const cancel = () => {
    $.process.run([bin, 'cancel', runId]).catch(() => {})
  }
  if (signal.aborted) {
    cancel()
    return []
  }

  signal.addEventListener('abort', cancel, { once: true })
  try {
    return yield* watchRun($, bin, runId, signal)
  } finally {
    signal.removeEventListener('abort', cancel)
  }
}

// watchRun waits for the run in short slices and, after each, streams what the
// provider did since the last as thinking, or a heartbeat when it has been
// quiet, until the run ends or the turn is interrupted. It never stops the run.
// It returns the summaries recorded for the run's windows when it ends and the
// turn was not interrupted, and none otherwise.
export async function* watchRun($, bin, runId, signal) {
  const follow = await createFollow($, bin, runId, signal)
  try {
    let step
    do {
      step = await follow.advance()
      for (const line of step.lines) yield thinking(line)
    } while (!step.isDone && !signal.aborted)
    return await follow.finish()
  } finally {
    follow.release()
  }
}

// createFollow is the follow of one run, kept between its slices: `advance`
// waits one slice and returns what to show for it, and `finish` ends the follow
// with the records. A follow that ends any other way is let go with `release`.
export async function createFollow($, bin, runId, signal) {
  // The recorder while the follow still feeds it: it is let go of when the
  // summaries are switched off, and a follow cut short lets go of it too.
  let recording = await recorderOf($, runId, await summaryConfig($))
  const startedAt = await $.clock.now()
  let shownAt = startedAt
  let from = 0

  async function advance() {
    const reply = await $.process.run(sliceWaitCommand(bin, runId), { timeoutMs: SLICE_TIMEOUT_MS })
    const progress = await newProgress($, bin, runId, from)
    from = progress.next
    const config = await summaryConfig($)
    if (recording !== null && !config.isEnabled) recording = abandon($, recording)
    if (recording !== null && progress.text.length > 0) recording.add(progress.text)
    // With summaries active the row shows labels, not the entries.
    const lines = (await summaries.observe(summaryPorts($), runId, progress.text)) ? summaries.take(runId) : progress.text

    const now = await $.clock.now()
    const shown = [...lines]
    if (lines.length > 0) shownAt = now
    else if (now - shownAt >= HEARTBEAT_MS && config.isEnabled) {
      shown.push(heartbeatLine(now - startedAt))
      shownAt = now
    }
    return { lines: shown, isDone: !(isWaitTimeout(reply) && !signal.aborted) }
  }

  async function finish() {
    const ending = recording
    recording = null
    try {
      return ending === null ? [] : await finishRecords($, runId, ending, signal)
    } finally {
      release()
    }
  }

  function release() {
    summaries.forget(runId)
    if (recording !== null) recording = abandon($, recording)
  }

  return { advance, finish, release }
}

// abandon tells a recorder that no more entries come and that it is to start no
// request; the records it holds are not collected.
function abandon($, recorder) {
  void recorder.finish({ isAborted: () => true, ports: summaryPorts($) }).catch(() => {})
  return null
}

// recorderOf is the recorder of one follow of a run, or null when the summaries
// are off, record nothing, or have no surface to draw on.
async function recorderOf($, runId, config) {
  if (!config.isEnabled || config.recordedEvery === 0 || (await $.session.surfaces()).length === 0) return null
  return createRecorder({ runId, every: config.recordedEvery, limiter, ports: summaryPorts($) })
}

// finishRecords ends a follow's recorder, handing it the engine ports its last
// window needs.
function finishRecords($, runId, recorder, signal) {
  const ports = { ...summaryPorts($), after: (ms, callback) => $.clock.after(ms, callback) }
  return finishOnStop({ recorder, runId, signal, ports })
}

// newProgress reads the run's progress entries from `from` on, and the run's
// state and cost so far (no cost key when the read failed). A read that fails
// shows nothing new, leaves `from` where it was and is logged once per run: the
// wait goes on, and the answer does not depend on progress.
async function newProgress($, bin, runId, from) {
  let failure
  try {
    const reply = await $.process.run(progressCommand(bin, runId, from))
    const progress = reply.exitCode === 0 ? readProgress(reply.stdout) : null
    if (progress !== null) {
      return { next: progress.next, text: progress.entries.map(progressLine), state: progress.state, cost: progress.cost }
    }
    failure = failureText(reply)
  } catch (error) {
    failure = messageOf(error)
  }

  if (!loggedProgressFailures.has(runId)) {
    loggedProgressFailures.add(runId)
    $.ui.log('could not read the progress of run ' + runId + ': ' + failure, { to: 'debug' })
  }
  return { next: from, text: [], state: null }
}

// readFinished reads a run that should have ended: { run }, or { failure } with
// the text that says why it cannot be answered for.
async function readFinished($, bin, runId) {
  const status = await $.process.run([bin, 'status', runId, '--json'])
  const run = status.exitCode === 0 ? finishedRun(status.stdout) : null
  if (run === null) return { failure: handoffFailure('could not read run ' + runId + '. ' + failureText(status)) }
  if (!isTerminal(run.state)) return { failure: handoffFailure('run ' + runId + ' is still ' + run.state + '; read it later with the msgate jobs tool.') }
  return { run }
}

async function finalAnswer($, bin, runId) {
  const read = await readFinished($, bin, runId)
  if (read.failure !== undefined) return read.failure
  const run = read.run

  const output = await $.process.run([bin, 'result', runId])
  if (output.exitCode !== 0) return handoffFailure('could not read the output of run ' + runId + '. ' + failureText(output))
  return answerText(run, output.stdout)
}

// failureOf names why a hook failed, from the engine's HookFailure.
function failureOf(failure) {
  return failure?.message ?? failure?.kind ?? 'unknown'
}
