// Idempotent "intents": one user action (place this order, add this cash)
// with one idempotency key, kept in sessionStorage until the server gives a
// definite answer. That covers two cases memory alone cannot:
//
//   - The answer is lost (timeout, network drop, 5xx, or 409 "still
//     processing"): we resend with the SAME key. If the first attempt went
//     through, the server replays its stored response; nothing happens twice.
//   - The page reloads mid-request: the intent is still in sessionStorage,
//     so the page can offer to resume it with the same key.
//
// sessionStorage (not localStorage) because it is per tab and gone when the
// tab closes: a key from an old session must never be reused for a new
// action. Never the URL: keys would leak into history and shared links.
import { newKey } from './api.js'

const PREFIX = 'intent:'

export function loadIntent(scope) {
  try {
    return JSON.parse(sessionStorage.getItem(PREFIX + scope))
  } catch {
    return null
  }
}

export function startIntent(scope, data) {
  const intent = { key: newKey(), ...data, startedAt: Date.now() }
  try {
    sessionStorage.setItem(PREFIX + scope, JSON.stringify(intent))
  } catch {
    // storage unavailable (private mode): the in-memory key still protects retries
  }
  return intent
}

export function endIntent(scope) {
  try {
    sessionStorage.removeItem(PREFIX + scope)
  } catch {
    // nothing to do
  }
}

// Outcome unknown: safe and worth retrying with the same key.
export const unknownOutcome = (err) =>
  err.network || err.status >= 500 || err.code === 'request_in_progress'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// sendIdempotent runs send(), retrying while the outcome is unknown, with
// backoff and jitter: 0.5s, 1s, 2s (+ up to 30%). Any definite answer
// (success or a real refusal) returns or throws straight away.
export async function sendIdempotent(send, attempts = 4) {
  for (let i = 0; ; i++) {
    try {
      return await send()
    } catch (err) {
      if (!unknownOutcome(err) || i === attempts - 1) throw err
      const base = 500 * 2 ** i
      await sleep(base + Math.random() * base * 0.3)
    }
  }
}
