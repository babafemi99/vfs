// Money is integer minor units (cents) everywhere. No float maths.

const group = (digits) => digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')

// formatMoney(123456) → "$1,234.56"
export function formatMoney(cents) {
  if (cents === null || cents === undefined) return '—'
  const negative = cents < 0
  const abs = Math.abs(cents)
  const whole = group(String(Math.trunc(abs / 100)))
  const frac = String(abs % 100).padStart(2, '0')
  return `${negative ? '-' : ''}$${whole}.${frac}`
}

// formatSigned(500) → "+$5.00", for P&L
export function formatSigned(cents) {
  if (cents === null || cents === undefined) return '—'
  return cents > 0 ? `+${formatMoney(cents)}` : formatMoney(cents)
}

// parseDollars("1,000.5") → 100050 cents, or null if it isn't an amount.
export function parseDollars(text) {
  const clean = String(text).replace(/[$,\s]/g, '')
  const m = /^(\d+)(?:\.(\d{0,2}))?$/.exec(clean)
  if (!m) return null
  return Number(m[1]) * 100 + Number((m[2] || '').padEnd(2, '0'))
}

export const pnlClass = (cents) => (cents > 0 ? 'gain' : cents < 0 ? 'loss' : '')
