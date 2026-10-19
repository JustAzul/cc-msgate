import { expect, test } from 'claude-code/testing'
import { isProgressRegistered, registerTools } from '../hooks/register.js'
import { world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const

// A `$` whose tool registration refuses the named tools and records the rest.
function registry(refused: string[] = []) {
  const registered: string[] = []
  const $ = {
    tool: {
      register: async (tool: { name: string }) => {
        if (refused.includes(tool.name)) throw new Error('the host refused the tool')
        registered.push(tool.name)
      },
    },
  }
  return { $, registered }
}

const PROGRESS = 'mcp__msgate__progress'
const NO_TOOLS = 'msgate agents run no tools'

function subagent(w: any, id: string, type: string) {
  w.agents.push({ id, type })
  w.messages[id] = [{ role: 'user', text: 'q' }]
}

test('the progress tool is refused to the main loop', async ($, on) => {
  world(on)
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: PROGRESS } as any)

  expect(out.deny).toContain("Engine's agent rows")
})

test('an agent row that calls the progress tool gets an empty result, answered rows included', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-1', 'msgate-agents:delegation')
  await $.session.start(START)

  const out: any = await $.tool.call({ tool: PROGRESS, agentId: 'ag-1' } as any)

  expect(out).toEqual({ result: '' })
})

test('an answered row is still refused every tool but the hand-back and the progress marker', async ($, on) => {
  const w = world(on)
  subagent(w, 'ag-1', 'msgate-agents:delegation')
  await $.session.start(START)

  const bash: any = await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'ag-1' } as any)
  const read: any = await $.tool.call({ tool: 'Read', file_path: '/etc/passwd', agentId: 'ag-1' } as any)

  expect(bash.deny).toContain(NO_TOOLS)
  expect(read.deny).toContain(NO_TOOLS)
})

// The host's own drawing of a tool's lines, which the mod's hooks sit above: a
// line of text naming the tool.
function hostToolLines(on: any) {
  for (const component of ['ToolUse', 'ToolResult']) {
    on('ui.render', { component }, ($: any, e: any) => {
      const { Text } = $.ui.resolve(e)
      return h(Text, {}, component + ' ' + e.props.tool)
    })
  }
}

async function texts($: any, component: string, tool: string) {
  const ui = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component, props: { tool }, requestId: 'req-1' })
  const found = (await ui.findAll({ type: 'Text' })).map((t: any) => t.text)
  await ui.unmount()
  return found
}

test('the calls of the progress tool draw nothing, in the line of the call and in the line of its result', async ($, on) => {
  world(on)
  hostToolLines(on)
  await $.session.start(START)

  expect(await texts($, 'ToolUse', PROGRESS)).toEqual([])
  expect(await texts($, 'ToolResult', PROGRESS)).toEqual([])
})

test('the lines of any other tool are drawn as the host draws them', async ($, on) => {
  world(on)
  hostToolLines(on)
  await $.session.start(START)

  expect(await texts($, 'ToolUse', 'Bash')).toEqual(['ToolUse Bash'])
  expect(await texts($, 'ToolResult', 'mcp__msgate__run')).toEqual(['ToolResult mcp__msgate__run'])
})

test('the mod registers the run, jobs and progress tools', async ($, on) => {
  const w = world(on)

  await $.session.start(START)

  expect(w.tools.map((tool) => tool.name)).toEqual(['run', 'jobs', 'progress'])
})

test('progress counts as registered once the host accepted it', async () => {
  const { $ } = registry()

  await registerTools($)

  expect(isProgressRegistered()).toBe(true)
})

test('a progress registration that fails leaves the other tools registered and progress unregistered', async () => {
  const { $, registered } = registry(['progress'])

  await registerTools($)

  expect(registered).toEqual(['run', 'jobs'])
  expect(isProgressRegistered()).toBe(false)
})

test('a registration that fails for another tool leaves progress registered', async () => {
  const { $, registered } = registry(['run'])

  await registerTools($)

  expect(registered).toEqual(['jobs', 'progress'])
  expect(isProgressRegistered()).toBe(true)
})
