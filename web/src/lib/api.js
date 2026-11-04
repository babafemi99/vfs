// Thin fetch wrappers for the trade API. Errors carry the API's error code.

const TIMEOUT_MS = 10_000

async function request(method, path, { body, key } = {}) {
  const headers = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (key) headers['Idempotency-Key'] = key

  // A request that gets no answer is "outcome unknown", not "failed":
  // err.network / err.timeout let callers retry safely with the same key.
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS)
  let res
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    })
  } catch (cause) {
    const err = new Error(cause.name === 'AbortError' ? 'The server took too long to answer' : 'Network error')
    err.network = true
    err.timeout = cause.name === 'AbortError'
    throw err
  } finally {
    clearTimeout(timer)
  }
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    const err = new Error(data?.error?.message || res.statusText)
    err.code = data?.error?.code
    err.status = res.status
    throw err
  }
  return data
}

// A fresh idempotency key: created once per user intent (dialog opened,
// quote shown) and reused if the same request is retried.
export const newKey = () => crypto.randomUUID()

export const api = {
  account: () => request('GET', '/api/account'),
  fund: (amount, key) => request('POST', '/api/account/fund', { body: { amount }, key }),
  prices: () => request('GET', '/api/prices'),
  activity: (after = 0, limit = 100) => request('GET', `/api/activity?after=${after}&limit=${limit}`),

  quote: (symbol, side, qty) => request('POST', '/api/quotes', { body: { symbol, side, qty } }),
  placeOrder: (quoteId, key) => request('POST', '/api/orders', { body: { quote_id: quoteId }, key }),
  orders: (status = 'all') => request('GET', `/api/orders?status=${status}`),
  order: (id) => request('GET', `/api/orders/${encodeURIComponent(id)}`),
  cancel: (id) => request('DELETE', `/api/orders/${encodeURIComponent(id)}`),
  compare: (id) => request('GET', `/api/orders/${encodeURIComponent(id)}/compare`),

  jobStats: () => request('GET', '/api/admin/jobs/stats'),
  jobs: (status) => request('GET', `/api/admin/jobs?status=${status}`),
  retryJob: (id) => request('POST', `/api/admin/jobs/${id}/retry`),
  retryAllDead: () => request('POST', '/api/admin/jobs/retry'),
  invariants: () => request('GET', '/api/admin/invariants'),
  breaks: () => request('GET', '/api/breaks'),
  reconLatest: () => request('GET', '/api/recon/latest'),
  reconRun: () => request('POST', '/api/recon/run'),

  webhooks: (limit = 200) => request('GET', `/api/admin/webhooks?limit=${limit}`),
  webhook: (id) => request('GET', `/api/admin/webhooks/${id}`),
}
