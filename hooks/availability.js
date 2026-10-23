// Whether a provider can take work, and the ranking of the models for a summary,
// read from `msgate providers --json`: the answer `models()` carries beside the
// catalog. Nothing here fails a caller.

import { messageOf, shimPath } from './lib.js'

// How long a listing is held: the CLI's own freshness for an available entry,
// and the shorter one while any provider is down, so a recovery shows up soon.
const AVAILABLE_TTL_MS = 5 * 60 * 1000
const UNAVAILABLE_TTL_MS = 60 * 1000

// The listing a load has read: `held` is { at, providers, summaryModels } and
// `pending` the read under way, which calls made together share.
export function createAvailabilityCache() {
  return { held: null, pending: null }
}

// availabilityOf answers { available, reason?, checked_at? } for the provider.
// A provider the listing does not name, or a listing that cannot be read, counts
// as available: nothing has said otherwise.
export async function availabilityOf($, cache, provider) {
  const { providers } = await listingOf($, cache)
  const entry = providers.find((item) => item?.provider === provider)
  if (entry?.available !== false) return { available: true, ...checkedAtOf(entry) }
  return { available: false, reason: entry.reason, ...checkedAtOf(entry) }
}

// listingFor answers { providers, summary_models } as the one read of the
// command gave them, or null when it could not be read.
export async function listingFor($, cache) {
  const { providers, summaryModels, isRead } = await listingOf($, cache)
  return isRead ? { providers, summary_models: summaryModels } : null
}

const checkedAtOf = (entry) => (typeof entry?.checked_at === 'string' ? { checked_at: entry.checked_at } : {})

const ttlOf = (providers) => (providers.some((item) => item?.available === false) ? UNAVAILABLE_TTL_MS : AVAILABLE_TTL_MS)

async function listingOf($, cache) {
  const now = await $.clock.now()
  if (cache.held !== null && now - cache.held.at < ttlOf(cache.held.providers)) return cache.held
  if (cache.pending === null) {
    cache.pending = readListing($, cache, now).finally(() => {
      cache.pending = null
    })
  }
  return cache.pending
}

// readListing reads the providers and the ranking and holds them, or answers
// none and holds nothing when the listing cannot be read, so the next call reads
// it again.
async function readListing($, cache, now) {
  try {
    const reply = await $.process.run([shimPath($.plugin.root), 'providers', '--json'])
    if (reply.exitCode !== 0) throw new Error('exited ' + reply.exitCode)
    const { providers, summary_models: summaryModels } = JSON.parse(reply.stdout)
    if (!Array.isArray(providers)) throw new Error('printed no list of providers')
    cache.held = { at: now, providers, summaryModels: Array.isArray(summaryModels) ? summaryModels : [], isRead: true }
    return cache.held
  } catch (error) {
    cache.held = null
    await $.ui.log('could not read the providers: ' + messageOf(error), { to: 'debug' })
    return { providers: [], summaryModels: [], isRead: false }
  }
}
