import { expect, test } from 'claude-code/testing'
import { answered, ok, step, world } from './kit'

const START = { surface: 'terminal', isInteractive: true, cwd: '/work' } as const
const WAIT_TIMEOUT = { exitCode: 5, stdout: JSON.stringify({ sdk_status: 'wait_timeout', exit_code: 5 }), stderr: '' }
const SPINNER_PROPS = { word: 'Sauteing', message: null, suffix: '…', mode: 'requesting' } as any

function gate() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => {
    release = resolve
  })
  return { promise, release }
}

// until lets the pending work of the mod settle: the condition is checked
// between microtask turns.
async function until(condition: () => boolean) {
  for (let i = 0; i < 2000 && !condition(); i++) await Promise.resolve()
  expect(condition()).toBe(true)
}

// A turn of agent `ag-1` that stands still at two points of its run: at its
// first wait (admitted, nothing read yet) and at its second (one raw entry
// read). Each is left by releasing its gate.
function startTurn($: any, w: any) {
  const atFirstWait = gate()
  const leaveFirstWait = gate()
  const atSecondWait = gate()
  const leaveSecondWait = gate()
  let waits = 0
  let reads = 0
  w.respond = async (argv: string[]) => {
    switch (argv[1]) {
      case 'exec':
        return ok(JSON.stringify({ conversation_id: 'conv-1', run_id: 'run-1' }))
      case 'wait':
        if (++waits === 1) {
          atFirstWait.release()
          await leaveFirstWait.promise
          return WAIT_TIMEOUT
        }
        atSecondWait.release()
        await leaveSecondWait.promise
        return ok(JSON.stringify({ run_id: 'run-1', state: 'done', outcome: 'ok' }))
      case 'progress': {
        const entries = reads++ === 0 ? [{ seq: 0, at: 't', kind: 'message', text: 'I will read auth.go' }] : []
        return ok(JSON.stringify({ run_id: 'run-1', state: 'running', next: 1, entries }))
      }
      case 'status':
        return ok(JSON.stringify({ run_id: 'run-1', conversation_id: 'conv-1', state: 'done', outcome: 'ok', output_path: '/o' }))
      default:
        return ok('Codex: done.')
    }
  }
  w.agents.push({ id: 'ag-1', type: 'msgate-agents:second-opinion' })
  w.agents.push({ id: 'ag-2', type: 'Explore' })
  w.messages['ag-1'] = [{ role: 'user', text: 'Is auth.go safe?' }]
  return {
    turn: $.session.start(START).then(() => step($, 'ag-1')),
    atFirstWait: atFirstWait.promise,
    leaveFirstWait: leaveFirstWait.release,
    atSecondWait: atSecondWait.promise,
    leaveSecondWait: leaveSecondWait.release,
  }
}

async function spinnerText($: any, requestId: string, props: any = SPINNER_PROPS) {
  const ui = await $.ui.mount({ plugin: 'msgate', surface: 'terminal', component: 'Spinner', props, requestId })
  const texts = (await ui.findAll({ type: 'Text' })).map((t: any) => t.text)
  await ui.unmount()
  return texts
}

test('the spinner of an msgate agent says its type until a label settles', { options: { recordedSummaryEvery: 0 } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  w.respondModel = () => new Promise(() => {})
  const run = startTurn($, w)
  await run.atFirstWait

  expect(await spinnerText($, 'ag-1')).toEqual(['msgate · second-opinion'])

  run.leaveFirstWait()
  await run.atSecondWait
  expect(await spinnerText($, 'ag-1')).toEqual(['msgate · second-opinion'])
  run.leaveSecondWait()
  await run.turn
})

// The label that settles while the turn stands at its second wait.
async function settleLabel(w: any, run: ReturnType<typeof startTurn>, label: string) {
  const request = gate()
  w.respondModel = async () => {
    await request.promise
    return answered(label)
  }
  run.leaveFirstWait()
  await run.atSecondWait
  request.release()
  await until(() => w.invalidations.length > 0)
}

test('the spinner of an msgate agent shows the label once one settles', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = startTurn($, w)
  await run.atFirstWait

  await settleLabel(w, run, 'Reading auth.go')

  expect(await spinnerText($, 'ag-1')).toEqual(['Reading auth.go'])
  run.leaveSecondWait()
  await run.turn
})

test('a label that settles for an agent asks for the spinner to be drawn again', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = startTurn($, w)
  await run.atFirstWait
  expect(w.invalidations).toEqual([])

  await settleLabel(w, run, 'Reading auth.go')

  expect(w.invalidations).toEqual(['ui.render'])
  run.leaveSecondWait()
  await run.turn
})

test('a message the engine already put on the spinner of the agent stays', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = startTurn($, w)
  await run.atFirstWait

  expect(await spinnerText($, 'ag-1', { ...SPINNER_PROPS, message: 'Compacting conversation' })).toEqual(['Compacting conversation'])
  run.leaveFirstWait()
  await run.atSecondWait
  run.leaveSecondWait()
  await run.turn
})

test('the spinner of another agent or the main loop is drawn untouched', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = startTurn($, w)
  await run.atFirstWait

  expect(await spinnerText($, 'ag-2')).toEqual(['Sauteing'])
  expect(await spinnerText($, 'main')).toEqual(['Sauteing'])
  run.leaveFirstWait()
  await run.atSecondWait
  run.leaveSecondWait()
  await run.turn
})

test('the spinner of the agent is drawn untouched once its turn has ended', async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = startTurn($, w)
  await run.atFirstWait
  run.leaveFirstWait()
  await run.atSecondWait
  run.leaveSecondWait()
  await run.turn

  expect(await spinnerText($, 'ag-1')).toEqual(['Sauteing'])
})

test('without summaries the spinner of the agent says its type, whatever the run did', { options: { summaries: false } }, async ($, on) => {
  const w = world(on)
  w.surfaces = ['terminal']
  const run = startTurn($, w)
  await run.atFirstWait
  run.leaveFirstWait()
  await run.atSecondWait

  expect(await spinnerText($, 'ag-1')).toEqual(['msgate · second-opinion'])
  expect(w.modelRequests).toEqual([])
  run.leaveSecondWait()
  await run.turn
})

test('the request id and word of a spinner go to the debug log once per request id', async ($, on) => {
  const w = world(on)
  const run = startTurn($, w)
  await run.atFirstWait

  await spinnerText($, 'ag-1')
  await spinnerText($, 'ag-1')
  await spinnerText($, 'main')

  expect(w.debugLogs.filter((line) => line.includes('Spinner'))).toEqual([
    'Spinner raised for ag-1 with the word Sauteing',
    'Spinner raised for main with the word Sauteing',
  ])
  run.leaveFirstWait()
  await run.atSecondWait
  run.leaveSecondWait()
  await run.turn
})
