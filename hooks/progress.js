// The progress tool is the marker that ends a step of an agent row: the host
// shows a tool call and its result, and nobody should see either.

// The name the model's loop sees for the tool the mod registers as `progress`.
export const PROGRESS_TOOL = 'mcp__msgate__progress'

export const PROGRESS_SPEC = {
  name: 'progress',
  description: 'Internal marker the engine uses between the steps of an agent row. Never call it.',
  inputSchema: { type: 'object', properties: {} },
}

const MAIN_LOOP_DENIAL = "msgate progress is a marker for the Engine's agent rows; the main loop never calls it."

// progressCall answers a call of the progress tool: the marker of a row has no
// result to give, and the main loop has no business with it.
export function progressCall(e) {
  return e.agentId === undefined ? { deny: MAIN_LOOP_DENIAL } : { result: '' }
}

// onProgressTool hooks the marker's calls and lines: a call is answered here,
// and its lines draw as an empty box so the person sees none of them; every
// other tool is drawn as it was.
export function onProgressTool(on) {
  on('tool.call', { tool: PROGRESS_TOOL }, async (_$, e) => progressCall(e))
  for (const component of ['ToolUse', 'ToolResult']) {
    on('ui.render', { component }, async ($, e, next) => {
      if (e.props.tool !== PROGRESS_TOOL) return next(e)
      const { Box } = $.ui.resolve(e)
      return h(Box, {})
    }).catch(($, e, next) => next(e))
  }
}
