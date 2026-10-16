// Pure helpers for the agent types the mod answers. Each type is a subagent
// whose every model request is answered by an agentcli job, never by a Claude
// model. Nothing here touches the mods API, so these functions can be read and
// tested on their own.

import { RESULT_OUTPUT_BYTES, cutBytes, isTerminal, resultNote } from './lib.js'
import { formatCost, formatLowerBound } from './prices.js'

export const AGENT_SOURCE = 'agent'
export const CONVERSATIONS_KEY = 'agent-conversations'
export const ADMIT_TIMEOUT_MS = 30000
export const CONVERSATIONS_LIMIT = 500

// The first line every failed hand-off answers with, so an answer that did not
// come from the agent can never pass for one.
export const HANDOFF_FAILED = 'agentcli: the hand-off to the agent failed, so this is not its answer.'

const DELIVERED_OUTCOMES = ['ok', 'clean']
const REMINDER = /<system-reminder>[\s\S]*?<\/system-reminder>/g
// A branch, tag or commit for `review`: no leading "-" or ".", so it can never
// read as a flag. The mod passes it as one argv entry, never through a shell.
const REVISION_PATTERN = /^[A-Za-z0-9_][A-Za-z0-9._/~^-]{0,254}$/
const REVIEW_TARGETS = "'uncommitted', 'base <branch>' or 'commit <sha>'"

// The agent types whose subagents the mod answers.
const TYPE_NAMES = ['second-opinion', 'code-review', 'cross-check', 'expert-persona', 'delegation', 'adhoc']

export function agentTypeNames() {
  return [...TYPE_NAMES]
}

// typeOf returns the agent type name for a full type ("<plugin>:<name>") this
// plugin answers, or null.
export function typeOf(pluginName, fullType) {
  const prefix = pluginName + ':'
  if (typeof fullType !== 'string' || !fullType.startsWith(prefix)) return null
  const name = fullType.slice(prefix.length)
  return agentTypeNames().includes(name) ? name : null
}

// turnPrompt is the newest user message of a subagent's conversation: the
// Agent call's prompt on the first turn, a SendMessage on a later one. A
// session that saves no transcript has no rows, and then the text the turn
// started with answers. The rows come first: on a turn a SendMessage woke, the
// start text wraps the message in the engine's own framing.
export function turnPrompt(rows, startText) {
  const last = [...rows].reverse().find((row) => row && row.role === 'user')
  return cleanPrompt(last?.text) || cleanPrompt(startText)
}

function cleanPrompt(text) {
  return String(text ?? '').replace(REMINDER, '').trim()
}

// hasAnswered tells a later turn from the first: the agent already replied.
export function hasAnswered(rows) {
  return rows.some((row) => row && row.role === 'assistant')
}

function fail(error) {
  return { error }
}

function jobFlags(sessionId) {
  return ['--background', '--json', '--source', AGENT_SOURCE, '--session-id', sessionId]
}

// reviewFlags reads a code-review prompt as exactly one review target.
export function reviewFlags(prompt) {
  const words = prompt.trim().split(/\s+/)
  if (words.length === 1 && words[0] === 'uncommitted') return { flags: ['--uncommitted'] }

  const [kind, revision] = words
  const isTarget = words.length === 2 && (kind === 'base' || kind === 'commit') && REVISION_PATTERN.test(revision)
  if (!isTarget) return fail('a code-review prompt is the review target and nothing else: ' + REVIEW_TARGETS + '; got: ' + prompt.slice(0, 200))

  return { flags: ['--' + kind, revision] }
}

// firstTurnCommand starts the conversation of a new subagent. The prompt goes
// on standard input so no prompt text ever becomes part of an argument.
export function firstTurnCommand(type, bin, sessionId, prompt) {
  if (prompt === '') return fail('the prompt is empty')
  if (type !== 'code-review') {
    return { argv: [bin, 'exec', '--scenario', type, ...jobFlags(sessionId), '-'], stdin: prompt }
  }

  const target = reviewFlags(prompt)
  if (target.error) return target
  return { argv: [bin, 'review', '--scenario', type, ...target.flags, ...jobFlags(sessionId)] }
}

// followUpCommand sends a SendMessage to the subagent's conversation.
export function followUpCommand(bin, sessionId, conversationId, prompt) {
  if (prompt === '') return fail('the prompt is empty')
  return { argv: [bin, 'send', conversationId, ...jobFlags(sessionId), '-'], stdin: prompt }
}

// isWaitTimeout tells a wait that gave up while the run goes on from a run
// that ended: `wait` exits with the run's own exit code, which can be 5 too.
export function isWaitTimeout(reply) {
  try {
    return JSON.parse(reply.stdout).sdk_status === 'wait_timeout'
  } catch {
    return false
  }
}

// finishedRun reads `status <run_id> --json`, or null when it is not a run.
export function finishedRun(stdout) {
  try {
    const run = JSON.parse(stdout)
    return run && typeof run.run_id === 'string' ? run : null
  } catch {
    return null
  }
}

function outcomeOf(run) {
  return run.outcome ?? run.state
}

function trailer(run) {
  return '[agentcli run ' + run.run_id + ' · conversation ' + run.conversation_id + ' · ' + outcomeOf(run) + ']'
}

function body(output, outputPath) {
  const cut = cutBytes(String(output ?? '').trim(), RESULT_OUTPUT_BYTES)
  return cut.isCut ? cut.text + '\n' + resultNote(outputPath) : cut.text
}

function endedLine(run) {
  const hasExcerpt = typeof run.error_excerpt === 'string' && run.error_excerpt !== ''
  return 'agentcli run ' + run.run_id + ' ended ' + outcomeOf(run) + (hasExcerpt ? ': ' + run.error_excerpt : '')
}

// answerText is what the subagent answers once its run has finished: the
// output, cut at 64 KiB, and a trailer naming the run, its conversation and its
// outcome. A run that did not deliver leads with how it ended.
export function answerText(run, output) {
  const text = body(output, run.output_path)
  if (!DELIVERED_OUTCOMES.includes(run.outcome)) {
    return [endedLine(run), text, trailer(run)].filter((part) => part !== '').join('\n\n')
  }

  return (text === '' ? 'The run produced no output.' : text) + '\n\n' + trailer(run)
}

export function handoffFailure(detail) {
  return HANDOFF_FAILED + ' ' + detail
}

// What a later turn answers when its conversation is no longer recorded: it
// is never started afresh, which would drop the context the follow-up needs.
export const LOST_CONVERSATION =
  "this agent's agentcli conversation is no longer recorded, so its context cannot be resumed here. " +
  "Continue it with the agentcli send tool and the conversation id from an earlier answer's trailer."

// readConversations returns the persisted agentId → conversation records, or
// an empty list when the stored value is missing or damaged.
export function readConversations(stored) {
  if (!Array.isArray(stored)) return []
  return stored.filter((entry) => entry && typeof entry.agent_id === 'string' && typeof entry.conversation_id === 'string')
}

// rememberConversation records an agent's conversation, replacing an older
// record of the same agent and keeping the most recent CONVERSATIONS_LIMIT.
export function rememberConversation(records, agentId, conversationId) {
  const others = records.filter((entry) => entry.agent_id !== agentId)
  return [...others, { agent_id: agentId, conversation_id: conversationId }].slice(-CONVERSATIONS_LIMIT)
}

export function conversationOf(records, agentId) {
  return records.find((entry) => entry.agent_id === agentId)?.conversation_id ?? null
}

// Following a run while it works. The step hook waits in short slices and,
// after each, shows what the provider did since the last one as thinking:
// drawn in the agent's row, never recorded in its transcript.
export const SLICE_SECONDS = 5
export const SLICE_TIMEOUT_MS = 60000
export const HEARTBEAT_MS = 30000

export function sliceWaitCommand(bin, runId) {
  return [bin, 'wait', runId, '--timeout', String(SLICE_SECONDS), '--json']
}

export function progressCommand(bin, runId, from) {
  return [bin, 'progress', runId, '--from', String(from), '--json']
}

// readProgress reads `progress --json`: the entries, where the next read
// starts, the run's state and its cost so far (null when it has none yet), or
// null when it is not a progress listing.
export function readProgress(stdout) {
  try {
    const parsed = JSON.parse(stdout)
    if (!parsed || !Array.isArray(parsed.entries) || typeof parsed.next !== 'number') return null
    return {
      next: parsed.next,
      entries: parsed.entries,
      state: typeof parsed.state === 'string' ? parsed.state : null,
      cost: typeof parsed.cost_usd === 'string' ? parsed.cost_usd : null,
    }
  } catch {
    return null
  }
}

export function progressLine(entry) {
  return entry.kind === 'command' ? '$ ' + entry.text : String(entry.text ?? '')
}

export function formatElapsed(ms) {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  const minutes = Math.floor(seconds / 60)
  return minutes === 0 ? seconds + 's' : minutes + 'm ' + (seconds % 60) + 's'
}

export function heartbeatLine(elapsedMs) {
  return 'still working · ' + formatElapsed(elapsedMs)
}

// terminalRunIds is a key that changes whenever one more run of the session
// ends, so the session's cost is read again only then.
export function terminalRunIds(stdout) {
  try {
    const parsed = JSON.parse(stdout)
    const runs = parsed && Array.isArray(parsed.runs) ? parsed.runs : []
    return runs.filter((run) => run && isTerminal(run.state)).map((run) => run.run_id).sort().join(' ')
  } catch {
    return ''
  }
}

export function sessionStatsCommand(bin, sessionId) {
  return [bin, 'stats', '--session-id', sessionId, '--all', '--json']
}

// providerCost is one provider's cost for the status line, "<label> <amount>",
// or undefined when it has none.
function providerCost(label, usage) {
  const cost = usage?.cost_complete === true ? formatCost(usage.cost_usd) : formatLowerBound(usage?.cost_usd)
  return cost === undefined ? undefined : label + ' ' + cost
}

// costText is the session's costs for the status line, the Codex one then the
// Claude one, or undefined when neither could be priced. An answer without
// usage_by_provider holds only the Codex runs' usage, in usage_totals.
export function costText(stdout) {
  try {
    const stats = JSON.parse(stdout)
    const providers = stats.usage_by_provider ?? { codex: stats.usage_totals }
    return [providerCost('Codex', providers.codex), providerCost('Claude', providers.anthropic)].filter((part) => part !== undefined).join(' | ') || undefined
  } catch {
    return undefined
  }
}

// missingPrices are the models of the session's runs that the price cache knows
// nothing about, not even as unpriced; a refresh may find them.
export function missingPrices(stdout) {
  try {
    const missing = JSON.parse(stdout).missing_prices
    return Array.isArray(missing) ? missing.filter((model) => typeof model === 'string') : []
  } catch {
    return []
  }
}

export function compactCount(n) {
  const value = Number(n) || 0
  if (value >= 1e6) return (value / 1e6).toFixed(1).replace(/\.0$/, '') + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(1).replace(/\.0$/, '') + 'k'
  return String(value)
}

// STATUS_MARK leads the plugin's status line.
export const STATUS_MARK = '💸'

export function statusLine(jobsText, cost) {
  const parts = [jobsText, cost].filter((part) => typeof part === 'string' && part !== '')
  return parts.length === 0 ? undefined : `${STATUS_MARK} ${parts.join(' · ')}`
}
