// The command that records a model call with `usage add`. Nothing here touches
// the mods API, so it can be read and tested on its own.

// isoSeconds writes a time in milliseconds as the RFC 3339 instant `usage add --ts` takes.
function isoSeconds(ms) {
  return new Date(ms).toISOString().slice(0, 19) + 'Z'
}

// modelCallCommand records one billed completion, whose usage goes on standard
// input. Only the recording's session, model and source are always given; a
// call id, attrs, the time of the call and a run follow when it has them.
export function modelCallCommand(bin, recording) {
  const argv = [bin, 'usage', 'add', '--session-id', recording.sessionId, '--provider', 'anthropic', '--model', recording.model, '--source', recording.source]
  if (recording.callId !== undefined) argv.push('--call-id', recording.callId)
  for (const [key, value] of Object.entries(recording.attrs ?? {})) argv.push('--attr', key + '=' + value)
  if (recording.at !== undefined) argv.push('--ts', isoSeconds(recording.at))
  if (recording.runId !== undefined) argv.push('--run-id', recording.runId)
  argv.push('--json')
  return argv
}
