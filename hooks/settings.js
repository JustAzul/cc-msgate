// What the plugin's options and the environment say of the progress summaries.
// Pure: the caller reads the config and the environment and logs the notes.

import { DEFAULT_RECORDED_EVERY, RECORD_WINDOW_ENTRIES } from './recorder.js'
import { ENTRIES_PER_REQUEST, WINDOW_ENTRIES } from './summary.js'

// cadence reads an option as an integer from `min` to `max`, or the fallback
// with a note that names the option. An option left out is not an error.
function cadence(options, name, { min, max, fallback }) {
  const value = options[name]
  if (value === undefined) return { value: fallback, notes: [] }
  if (Number.isInteger(value) && value >= min && value <= max) return { value, notes: [] }
  return { value: fallback, notes: [name + ' must be an integer from ' + min + ' to ' + max + '; ' + fallback + ' applies'] }
}

// summarySettings answers whether summaries are on and at what cadence. `env`
// is the raw value of MSGATE_SUMMARIES: 0 and 1 win over the summaries option;
// any other value leaves the option in force, with a note.
export function summarySettings({ options, env }) {
  const config = options ?? {}
  const live = cadence(config, 'liveSummaryEvery', { min: 1, max: WINDOW_ENTRIES, fallback: ENTRIES_PER_REQUEST })
  const recorded = cadence(config, 'recordedSummaryEvery', { min: 0, max: RECORD_WINDOW_ENTRIES, fallback: DEFAULT_RECORDED_EVERY })
  const notes = [...live.notes, ...recorded.notes]

  const isOdd = env !== undefined && env !== '0' && env !== '1'
  if (isOdd) notes.push('MSGATE_SUMMARIES=' + env + ' is not 0 or 1; the summaries option applies')

  const isEnabled = env === '0' ? false : env === '1' ? true : config.summaries !== false
  return { isEnabled, liveEvery: live.value, recordedEvery: recorded.value, notes }
}
