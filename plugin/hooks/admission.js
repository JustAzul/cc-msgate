// Admitting a background job: the one path the run tool and the interface's run
// method share. It runs the plugin's own binary and reads the ids it prints.

import { admittedJob, failureText, messageOf, shimPath, turnCommand } from './lib.js'

// admitJob starts a job for `input` (the arguments of the run tool) labelled with
// `source`. It resolves { ids, isContinuation, sessionId, bin } for an admitted
// job, or { error } with the reason it was not. It never throws.
export async function admitJob($, input, source) {
  const sessionId = await $.session.id()
  const bin = shimPath($.plugin.root)
  const built = turnCommand(bin, sessionId, input, source)
  if (built.error) return { error: built.error }
  let reply
  try {
    reply = await $.process.run(built.argv, { stdin: built.stdin })
  } catch (error) {
    return { error: 'msgate could not run: ' + messageOf(error) }
  }
  if (reply.exitCode !== 0) return { error: failureText(reply) }
  const ids = admittedJob(reply.stdout)
  if (ids === null) return { error: 'msgate printed an unexpected admission result: ' + reply.stdout.trim().slice(0, 500) }
  return { ids, isContinuation: built.isContinuation, sessionId, bin }
}
