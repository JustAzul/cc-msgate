// What the environment says of the permission mode a hidden row refuses.
// Pure: the caller reads the environment and logs the notes.

// The permission modes the host names.
const HOST_MODES = ['default', 'acceptEdits', 'bypassPermissions', 'plan', 'dontAsk', 'auto']
// The one mode MSGATE_ROW_MODE can make the engine refuse.
export const REFUSED_MODE = 'auto'

// rowModeSettings answers whether the spawn of a hidden row is refused. `env` is
// the raw value of MSGATE_ROW_MODE: auto forces the refusal; a variable that is
// absent, empty or not text forces nothing; any other value is accepted and
// refuses nothing, with a note when the host knows no mode of that name.
export function rowModeSettings({ env }) {
  if (typeof env !== 'string' || env === '') return { isRefused: false, notes: [] }
  if (env === REFUSED_MODE) return { isRefused: true, notes: [] }

  const notes = HOST_MODES.includes(env) ? [] : ['MSGATE_ROW_MODE=' + env + ' is not a permission mode the host knows; only ' + REFUSED_MODE + ' refuses the row']
  return { isRefused: false, notes }
}
