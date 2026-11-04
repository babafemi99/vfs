// One SSE connection to /api/stream for the whole app. Pages watch
// live.version and reload their data when it changes; live.recent keeps the
// latest activity for the feed.

export const live = $state({ connected: false, version: 0, recent: [] })

let source
const listeners = new Set()

// onActivity registers a callback for items that arrive after page load.
export function onActivity(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function connect() {
  if (source) return
  const startedAt = Date.now()
  source = new EventSource('/api/stream')
  source.onopen = () => { live.connected = true }
  source.onerror = () => { live.connected = false } // EventSource reconnects by itself, resuming from the last id
  source.addEventListener('activity', (msg) => {
    const item = JSON.parse(msg.data)
    live.recent = [item, ...live.recent].slice(0, 30)
    live.version++
    if (new Date(item.created_at).getTime() >= startedAt - 1000) {
      listeners.forEach((fn) => fn(item))
    }
  })
}
