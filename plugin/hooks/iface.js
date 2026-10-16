// The engine's interface `msgate`: the methods other plugins call, each
// answered by a hook on msgate.<method> with { value } or { deny }. The caller is
// the plugin the host stamps on the call, passed in by the hook.

import { backType, backedData } from './backed.js'
import { admitJob } from './admission.js'
import { NOUN as SERVICE } from './service.js'
import { RESULT_OUTPUT_BYTES, cutBytes, failureText, jobsCommand, messageOf, pluginSource, resultNote, shimPath, withPurpose } from './lib.js'

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
// { error } with why it did not. It never throws.
async function readReply($, argv) {
  let reply
  try {
    reply = await $.process.run(argv)
  } catch (error) {
    return { error: 'msgate could not run: ' + messageOf(error) }
  }
  return reply.exitCode === 0 ? { reply } : { error: failureText(reply) }
}

// ownRuns are the runs of a `status --json` listing the caller started.
function ownRuns(stdout, caller) {
  const runs = JSON.parse(stdout).runs
  if (!Array.isArray(runs)) throw new Error('the listing holds no runs')
  return runs.filter((run) => run && run.source === pluginSource(caller)).map(withPurpose)
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
async function jobsOf($, bin, built, args, caller) {
  if (args.action !== 'list') return jobsOnRun($, bin, args, caller)
  const read = await readReply($, built.argv)
  if (read.error !== undefined) return jobsDenial(read.error)
  return { value: { runs: ownRuns(read.reply.stdout, caller) } }
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
  return { value: { ...admitted.ids, continued: admitted.isContinuation } }
}

// serveJobs reads and cancels the runs the caller started, and no other.
export async function serveJobs($, args, caller) {
  const bin = shimPath($.plugin.root)
  const built = jobsCommand(bin, await $.session.id(), args)
  if (built.error) return jobsDenial(built.error)
  try {
    return await jobsOf($, bin, built, args, caller)
  } catch (error) {
    return jobsDenial('msgate printed something unexpected: ' + messageOf(error))
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
}
