// The engine's interface `msgate`: the methods other plugins call, each
// answered by a hook on msgate.<method> with { value } or { deny }. The caller is
// the plugin the host stamps on the call, passed in by the hook.

import { backType, backedData } from './backed.js'
import { admitJob } from './admission.js'
import { admittedRunIds, forgetRuns, recordAdmitted } from './admitted.js'
import { flaggedRuns } from './band.js'
import { hiddenRunIds } from './hidden.js'
import { NOUN as SERVICE } from './service.js'
import { EXIT_RUN_NOT_FOUND, RESULT_OUTPUT_BYTES, cutBytes, failureText, jobsCommand, messageOf, pluginSource, resultNote, shimPath, withPurpose } from './lib.js'

const VERSION_UNAVAILABLE = 'msgate:version:unavailable'

// releaseVersion asks the binary for the version it was built with, which is the
// release's own: the manifest carries none. It throws when it cannot say.
async function releaseVersion($) {
  const reply = await $.process.run([shimPath($.plugin.root), 'version', '--json'])
  if (reply.exitCode !== 0) throw new Error('exited ' + reply.exitCode)
  const version = JSON.parse(reply.stdout).version
  if (typeof version !== 'string' || version === '') throw new Error('printed no version')
  return version
}

const jobsDenial = (reason) => ({ deny: 'msgate:jobs:' + reason })

// readReply runs a command of the binary: { reply } for one that exited 0, or
// { error } with why it did not, plus `isNotFound` when the run asked for is not
// known to it. It never throws.
async function readReply($, argv) {
  let reply
  try {
    reply = await $.process.run(argv)
  } catch (error) {
    return { error: 'msgate could not run: ' + messageOf(error) }
  }
  return reply.exitCode === 0 ? { reply } : { error: failureText(reply), isNotFound: reply.exitCode === EXIT_RUN_NOT_FOUND }
}

// The runs of a record read together; each read is a process of its own.
const READ_BATCH = 10

// readRecorded reads the runs with the given ids: { runs } in the order given for
// those the binary knows, { pruned } for the ids it does not, or { error } for
// the first read that failed otherwise.
async function readRecorded($, bin, ids) {
  const runs = []
  const pruned = []
  for (let from = 0; from < ids.length; from += READ_BATCH) {
    const batch = ids.slice(from, from + READ_BATCH)
    const reads = await Promise.all(batch.map((id) => readReply($, [bin, 'status', id, '--json'])))
    for (const [index, read] of reads.entries()) {
      if (read.isNotFound) pruned.push(batch[index])
      else if (read.error !== undefined) return { error: read.error }
      else runs.push(withPurpose(JSON.parse(read.reply.stdout)))
    }
  }
  return { runs, pruned }
}

// listOwn answers `list`: the runs the caller admitted through run, newest first,
// each read by id. A run the binary no longer knows leaves the record.
async function listOwn($, bin, caller) {
  const ids = (await admittedRunIds($, caller)).reverse()
  const read = await readRecorded($, bin, ids)
  if (read.error !== undefined) return jobsDenial(read.error)
  if (read.pruned.length > 0) await forgetQuietly($, caller, read.pruned)
  return { value: { runs: read.runs } }
}

// forgetQuietly drops pruned runs from the record. A write that fails is logged:
// the next list drops them again.
async function forgetQuietly($, caller, runIds) {
  try {
    await forgetRuns($, caller, runIds)
  } catch (error) {
    await $.ui.log('could not drop pruned runs from the record of ' + caller + ': ' + messageOf(error), { to: 'debug' })
  }
}

// ownedRun reads the status of a run and answers it only when the caller started it.
async function ownedRun($, bin, runId, caller) {
  const read = await readReply($, [bin, 'status', runId, '--json'])
  if (read.error !== undefined) return jobsDenial(read.error)
  const run = JSON.parse(read.reply.stdout)
  if (run?.source !== pluginSource(caller)) return jobsDenial('not-yours')
  return { run: withPurpose(run) }
}

// jobsOnRun does a status, result or cancel on a run the caller owns.
async function jobsOnRun($, bin, args, caller) {
  const owned = await ownedRun($, bin, args.run_id, caller)
  if (owned.deny !== undefined) return owned
  if (args.action === 'status') return { value: owned.run }

  const done = await readReply($, [bin, args.action, args.run_id])
  if (done.error !== undefined) return jobsDenial(done.error)
  if (args.action === 'cancel') return { value: done.reply.stdout.trim() || 'cancellation requested for ' + args.run_id }
  const cut = cutBytes(done.reply.stdout, RESULT_OUTPUT_BYTES)
  return { value: cut.isCut ? cut.text + '\n' + resultNote(owned.run.output_path) : done.reply.stdout }
}

// jobsOf answers a jobs call whose arguments were accepted.
async function jobsOf($, bin, args, caller) {
  return args.action === 'list' ? listOwn($, bin, caller) : jobsOnRun($, bin, args, caller)
}

// The version a load has read, shared by every call of the load. A read that
// failed is forgotten, so the next call tries again.
export function createVersionCache() {
  return { pending: null }
}

function versionOnce($, cache) {
  if (cache.pending === null) {
    cache.pending = releaseVersion($).catch((error) => {
      cache.pending = null
      throw error
    })
  }
  return cache.pending
}

// serveBackType marks the caller's agent type as one the engine answers.
export async function serveBackType($, args, caller) {
  const checked = backedData(args)
  if (checked.field !== undefined) return { deny: 'msgate:backType:' + checked.field }
  const fullType = caller + ':' + args.type
  await backType($, fullType, checked.data)
  return { value: { type: fullType } }
}

// serveVersion is the engine's release version.
export async function serveVersion($, cache) {
  try {
    return { value: await versionOnce($, cache) }
  } catch (error) {
    await $.ui.log('could not read the msgate version: ' + messageOf(error), { to: 'debug' })
    return { deny: VERSION_UNAVAILABLE }
  }
}

// serveRun admits a job like the run tool, labelled with the caller's source.
// Its result reaches the caller through `jobs`, never Claude.
export async function serveRun($, args, caller) {
  const admitted = await admitJob($, args, pluginSource(caller))
  if (admitted.error !== undefined) return { deny: 'msgate:run:' + admitted.error }
  await recordQuietly($, caller, admitted.ids.run_id)
  return { value: { ...admitted.ids, continued: admitted.isContinuation } }
}

// recordQuietly adds an admitted run to the caller's record. The run is admitted
// already, so a record that cannot be written is logged and the caller still gets
// the ids; it reads that run with a status.
async function recordQuietly($, caller, runId) {
  try {
    await recordAdmitted($, caller, runId)
  } catch (error) {
    await $.ui.log('could not record run ' + runId + ' for ' + caller + ': ' + messageOf(error), { to: 'debug' })
  }
}

// serveJobs reads and cancels the runs the caller started, and no other.
export async function serveJobs($, args, caller) {
  const bin = shimPath($.plugin.root)
  const built = jobsCommand(bin, await $.session.id(), args)
  if (built.error) return jobsDenial(built.error)
  try {
    return await jobsOf($, bin, args, caller)
  } catch (error) {
    return jobsDenial('msgate printed something unexpected: ' + messageOf(error))
  }
}

// serveFlaggedRuns answers the session's runs that ask to be shown and have no
// hidden row, so a plugin that opens rows takes each run once.
export async function serveFlaggedRuns($) {
  try {
    const bin = shimPath($.plugin.root)
    const read = await readReply($, [bin, 'status', '--json', '--session-id', await $.session.id()])
    if (read.error !== undefined) throw new Error(read.error)
    const hidden = await hiddenRunIds($)
    const runs = flaggedRuns(read.reply.stdout).filter((run) => !hidden.has(run.run_id))
    return { value: runs.map((run) => ({ run_id: run.run_id, purpose: run.purpose ?? '', source: run.source, started_at: run.started_at ?? null })) }
  } catch (error) {
    await $.ui.log('could not list the flagged runs: ' + messageOf(error), { to: 'debug' })
    return { deny: 'msgate:flaggedRuns:failed' }
  }
}

const unserved = (method) => async () => {
  throw new Error('msgate:' + method + ':unserved')
}

// What engine.create adds to `$`: an object of functions and nothing else, one
// for each method. The call itself is answered by the hook on msgate.<method>,
// which has a `$` to work with; a body only runs when that hook and its .catch
// both failed to answer, and then the call was not served.
export const INTERFACE = {
  complete: SERVICE.complete,
  backType: unserved('backType'),
  version: unserved('version'),
  run: unserved('run'),
  jobs: unserved('jobs'),
  flaggedRuns: unserved('flaggedRuns'),
}
