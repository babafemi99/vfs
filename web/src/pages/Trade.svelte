<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Select from '$lib/components/ui/select/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Input } from '$lib/components/ui/input/index.js'
  import { Label } from '$lib/components/ui/label/index.js'
  import Receipt from '@lucide/svelte/icons/receipt'
  import PageHeader from '../components/PageHeader.svelte'
  import FundDialog from '../components/FundDialog.svelte'
  import { api } from '../lib/api.js'
  import { loadIntent, startIntent, endIntent, sendIdempotent, unknownOutcome } from '../lib/intent.js'
  import { route, go } from '../lib/router.svelte.js'
  import { formatMoney } from '../lib/money.js'
  import { toast } from '../lib/toast.js'

  const fallbackSymbols = ['AAPL', 'MSFT', 'TSLA', 'DANGCEM', 'MTNN']

  let prices = $state([])
  let symbol = $state(route.query.get('symbol') || 'AAPL')
  let side = $state('BUY')
  let qty = $state('10')

  let quote = $state(null)
  // An order placed before a reload that never got a definite answer.
  let pending = $state(loadIntent('order'))
  let now = $state(Date.now())
  let busy = $state(false)
  let error = $state(null)
  let funding = $state(false)

  const symbols = $derived(prices.length ? prices.map((p) => p.symbol) : fallbackSymbols)
  const price = $derived(prices.find((p) => p.symbol === symbol))
  const secondsLeft = $derived(quote ? Math.max(0, Math.ceil((new Date(quote.expires_at).getTime() - now) / 1000)) : 0)
  const expired = $derived(quote !== null && secondsLeft === 0)

  $effect(() => {
    api.prices().then((r) => (prices = r.prices)).catch(() => {})
    const tick = setInterval(() => (now = Date.now()), 250)
    return () => clearInterval(tick)
  })

  // Changing the order throws the quote away.
  $effect(() => {
    symbol; side; qty
    quote = null
  })

  async function getQuote(e) {
    e.preventDefault()
    busy = true
    error = null
    try {
      quote = await api.quote(symbol, side, Number(qty))
    } catch (err) {
      error = err
    } finally {
      busy = false
    }
  }

  // place sends an order intent, retrying with the same key while the
  // outcome is unknown. A definite answer ends the intent; an unknown one
  // keeps it (in sessionStorage) so a later retry or a reload is still safe.
  async function place(intent) {
    busy = true
    error = null
    try {
      const order = await sendIdempotent(() => api.placeOrder(intent.quoteId, intent.key))
      endIntent('order')
      pending = null
      toast('Order placed', `${order.side} ${order.qty} ${order.symbol} is being sent to the broker.`, 'success')
      go(`#/orders/${order.id}`)
    } catch (err) {
      if (unknownOutcome(err)) {
        error = { code: 'unknown', message: err.message }
      } else {
        endIntent('order')
        pending = null
        error = err
      }
    } finally {
      busy = false
    }
  }

  function confirm() {
    // One key per quote: confirming the same quote again reuses it.
    let intent = loadIntent('order')
    if (!intent || intent.quoteId !== quote.id) {
      intent = startIntent('order', { quoteId: quote.id, side: quote.side, qty: quote.qty, symbol: quote.symbol })
    }
    place(intent)
  }

  function discardPending() {
    endIntent('order')
    pending = null
  }
</script>

<PageHeader eyebrow="Trade" title="New order" description="Get a guaranteed quote, then confirm before it expires." />

{#if pending && !busy}
  <div class="mb-6 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
    <div>
      <strong>An order didn't get a confirmation:</strong> {pending.side} {pending.qty} {pending.symbol}.
      Resuming is safe: it uses the same idempotency key, so it can't place a second order.
    </div>
    <div class="flex gap-2">
      <Button size="sm" onclick={() => place(pending)}>Resume</Button>
      <Button size="sm" variant="outline" onclick={discardPending}>Discard</Button>
    </div>
  </div>
{/if}

<div class="grid gap-6 lg:grid-cols-[3fr_2fr]">
  <Card.Root class="shadow-none">
    <Card.Content>
      <form onsubmit={getQuote} class="grid gap-5">
        <div class="grid grid-cols-2 gap-1 rounded-xl bg-muted p-1" role="radiogroup" aria-label="Side">
          {#each [['BUY', 'Buy'], ['SELL', 'Sell']] as [value, label]}
            <button
              type="button"
              role="radio"
              aria-checked={side === value}
              onclick={() => (side = value)}
              class="rounded-lg py-2.5 text-sm font-semibold transition-all
                {side === value
                  ? `bg-card shadow-sm ring-1 ring-border ${value === 'BUY' ? 'text-gain' : 'text-loss'}`
                  : 'text-muted-foreground hover:text-foreground'}">{label}</button>
          {/each}
        </div>

        <div class="grid gap-2">
          <Label>Symbol</Label>
          <Select.Root type="single" bind:value={symbol}>
            <Select.Trigger class="h-11 w-full">{symbol}</Select.Trigger>
            <Select.Content>
              {#each symbols as s}
                {@const p = prices.find((x) => x.symbol === s)}
                <Select.Item value={s} label={s}>
                  <span class="font-semibold">{s}</span>
                  {#if p}<span class="ml-auto text-xs text-muted-foreground num">{formatMoney(p.bid)} / {formatMoney(p.ask)}</span>{/if}
                </Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>

        <div class="grid gap-2">
          <Label for="qty">Quantity (shares)</Label>
          <Input id="qty" class="h-11" type="number" min="1" max="1000000" step="1" inputmode="numeric" bind:value={qty} required />
        </div>

        {#if price}
          <p class="rounded-r-lg border-l-[3px] border-primary bg-muted px-4 py-3 text-sm text-muted-foreground">
            Indicative {side === 'BUY' ? 'ask' : 'bid'} {formatMoney(side === 'BUY' ? price.ask : price.bid)} ·
            about {formatMoney((side === 'BUY' ? price.ask : price.bid) * Number(qty || 0))} before fees
          </p>
        {/if}

        <Button type="submit" size="lg" disabled={busy || !Number(qty)}>{busy && !quote ? 'Getting quote…' : 'Get quote'}</Button>
      </form>
    </Card.Content>
  </Card.Root>

  <Card.Root class="shadow-none">
    <Card.Header><Card.Title>Quote</Card.Title></Card.Header>
    <Card.Content class="grid gap-3">
      {#if !quote}
        <div class="flex flex-col items-center gap-3 py-10 text-center">
          <span class="grid size-11 place-items-center rounded-full bg-accent text-primary"><Receipt class="size-5" /></span>
          <p class="max-w-64 text-sm text-muted-foreground">Choose a symbol and quantity, then get a quote. It's guaranteed for a few seconds.</p>
        </div>
      {:else}
        {#each [['Order', `${quote.side} ${quote.qty} ${quote.symbol}`], ['Price per share', formatMoney(quote.price)], ['Fee', formatMoney(quote.fee)]] as [k, v]}
          <div class="flex justify-between border-b pb-2.5 text-sm"><span class="text-muted-foreground">{k}</span><span class="num font-semibold">{v}</span></div>
        {/each}
        <div class="flex items-baseline justify-between"><span class="text-sm text-muted-foreground">{quote.side === 'BUY' ? 'Total to pay' : 'You receive'}</span><span class="num text-xl font-bold">{formatMoney(quote.total)}</span></div>

        {#if expired}
          <p class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">Quote expired, get a new one.</p>
        {:else}
          <p class="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800">
            Valid for <strong class="num">{secondsLeft}s</strong>. {quote.side === 'BUY' ? 'The total is held from your cash' : 'The shares are held'} until the order finishes.
          </p>
          <Button size="lg" onclick={confirm} disabled={busy}>{busy ? 'Placing…' : `Confirm ${quote.side.toLowerCase()}`}</Button>
        {/if}
      {/if}

      {#if error}
        <div class="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
          {#if error.code === 'insufficient_funds'}
            Not enough available cash for this order.
            <Button variant="outline" size="sm" class="ml-2" onclick={() => (funding = true)}>Fund account</Button>
          {:else if error.code === 'insufficient_shares'}
            You don't hold enough {symbol} shares to sell.
          {:else if error.code === 'quote_expired'}
            Quote expired, get a new one.
          {:else if error.code === 'unknown'}
            We couldn't confirm the order ({error.message}). Confirming again is safe: it reuses the same key, so it won't place a second order.
          {:else if error.code === 'rate_limited'}
            {error.message}
          {:else}
            {error.message}
          {/if}
        </div>
      {/if}
    </Card.Content>
  </Card.Root>
</div>

<FundDialog bind:open={funding} />
