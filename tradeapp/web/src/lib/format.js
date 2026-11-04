// Status labels and tones (tones map to the brand's status colours).
const STATUS = {
  PENDING_SUBMIT: ['Submitting', 'pending'],
  OPEN: ['Open', 'processing'],
  PARTIALLY_FILLED: ['Partially filled', 'processing'],
  PENDING_CANCEL: ['Cancelling', 'processing'],
  FILLED: ['Filled', 'completed'],
  CANCELLED: ['Cancelled', 'cancelled'],
  EXPIRED: ['Expired', 'cancelled'],
  REJECTED: ['Rejected', 'failed'],
}
export const statusLabel = (s) => STATUS[s]?.[0] ?? s
export const statusTone = (s) => STATUS[s]?.[1] ?? 'cancelled'
export const isFinal = (s) => ['FILLED', 'CANCELLED', 'EXPIRED', 'REJECTED'].includes(s)
export const canCancel = (s) => ['PENDING_SUBMIT', 'OPEN', 'PARTIALLY_FILLED'].includes(s)

// The plain-English result of a final order (mirrors domain.OutcomeLine).
export function outcomeLine(o) {
  const verb = o.side === 'SELL' ? 'sold' : 'bought'
  switch (o.status) {
    case 'FILLED': return 'Filled'
    case 'CANCELLED': return o.filled_qty === 0 ? 'Cancelled' : `${o.filled_qty} of ${o.qty} ${verb}, ${o.qty - o.filled_qty} cancelled`
    case 'EXPIRED': return `Expired: ${o.filled_qty} of ${o.qty} ${verb}`
    case 'REJECTED': return o.reason ? `Rejected: ${o.reason.replaceAll('_', ' ')}` : 'Rejected'
    case 'PENDING_CANCEL': return `Cancelling… ${o.filled_qty} of ${o.qty} filled`
    case 'PARTIALLY_FILLED': return `${o.filled_qty} of ${o.qty} filled`
    case 'OPEN': return 'Working at the broker'
    case 'PENDING_SUBMIT': return 'Being sent to the broker'
  }
  return ''
}

const dateTime = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit' })
export const when = (iso) => (iso ? dateTime.format(new Date(iso)) : '—')

export function ago(seconds) {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

// orderLabel names the order an activity item is about ("BUY 8 DANGCEM"),
// unless the message already says it.
export function orderLabel(item) {
  const d = item.data
  if (!item.order_id || !d?.symbol || item.message.includes(d.symbol)) return ''
  return `${d.side} ${d.qty} ${d.symbol}`
}

export const shortId = (id) => (id && id.length > 18 ? `${id.slice(0, 10)}…${id.slice(-6)}` : id)
