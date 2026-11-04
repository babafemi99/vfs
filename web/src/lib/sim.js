// brokersim's dev API, reached through the web app's /sim proxy.
async function call(method, path, body) {
  const res = await fetch('/sim/dev' + path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error?.message || res.statusText)
  return data
}

export const sim = {
  orders: () => call('GET', '/orders'),
  deliveries: () => call('GET', '/deliveries?limit=50'),
  prices: () => call('GET', '/prices'),
  engine: () => call('GET', '/engine'),
  setEngine: (patch) => call('PUT', '/engine', patch),
  act: (id, action, body) => call('POST', `/orders/${encodeURIComponent(id)}/${action}`, body),
  redeliver: (eventId) => call('POST', `/deliveries/${encodeURIComponent(eventId)}/redeliver`),
  reset: () => call('POST', '/reset'),
}
