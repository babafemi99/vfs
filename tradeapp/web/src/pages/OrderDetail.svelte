<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Dialog from '$lib/components/ui/dialog/index.js'
  import * as Table from '$lib/components/ui/table/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import { Skeleton } from '$lib/components/ui/skeleton/index.js'
  import StatusPill from '../components/StatusPill.svelte'
  import { api } from '../lib/api.js'
  import { live } from '../lib/live.svelte.js'
  import { formatMoney } from '../lib/money.js'
  import { when, outcomeLine, isFinal, canCancel } from '../lib/format.js'
  import { toast } from '../lib/toast.js'

  let { id } = $props()

  let order = $state(null)
  let error = $state('')
  let confirming = $state(false)
  let cancelling = $state(false)
  let comparison = $state(null)
  let comparing = $state(false)

  async function load() {
    try {
      order = await api.order(id)
      error = ''
    } catch (err) {
      error = err.status === 404 ? 'No such order.' : err.message
    }
  }

  $effect(() => {
    live.version
    load()
  })

  // Step tracker: Placed → Sent to broker → outcome.
  const steps = $derived.by(() => {
    if (!order) return []
    const s = order.status
    const sent = order.provider_order_id || order.filled_qty > 0
    const final = isFinal(s)
    const bad = s === 'REJECTED'
    const finalLabel = final ? ({ FILLED: 'Filled', REJECTED: 'Rejected', EXPIRED: 'Expired' })[s] ?? 'Cancelled' : 'Final outcome'
    return [
      { label: 'Placed', state: 'done' },
      { label: 'Sent to broker', state: sent ? 'done' : bad ? 'bad' : final ? '' : 'now' },
      { label: finalLabel, state: final ? (bad ? 'bad' : 'done') : sent ? 'now' : '' },
    ]
  })

  async function cancel() {
    cancelling = true
    try {
      const o = await api.cancel(id)
      if (o.status === 'CANCELLED') toast('Order cancelled', 'It had not been sent yet; your hold was returned.', 'success')
      else toast('Cancel requested', 'The broker decides. Fills can still land meanwhile.', 'info')
      confirming = false
      load()
    } catch (err) {
      toast('Could not cancel', err.message, 'error')
    } finally {
      cancelling = false
    }
  }

  async function compare() {
    comparing = true
    try {
      comparison = await api.compare(id)
      toast(comparison.agree ? 'In agreement with the broker' : 'Differences found', comparison.agree ? '' : `${comparison.mismatches.length} difference(s).`, comparison.agree ? 'success' : 'warning')
    } catch (err) {
      toast('Compare failed', err.message, 'error')
    } finally {
      comparing = false
    }
  }

  async function copy(text) {
    await navigator.clipboard?.writeText(text)
    toast('Copied', text)
  }
</script>

<a href="#/orders" class="text-sm text-primary">← Back to orders</a>

{#if error}
  <p class="mt-4 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p>
{:else if !order}
  <Skeleton class="mt-4 h-64 rounded-xl" />
{:else}
  <div class="mt-4 mb-6 flex flex-wrap items-end justify-between gap-4">
    <div>
      <p class="eyebrow mb-1.5">Order details</p>
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-3xl font-bold"><span class="text-lg {order.side === 'BUY' ? 'text-gain' : 'text-loss'}">{order.side}</span> {order.qty} {order.symbol}</h1>
        <StatusPill status={order.status} />
      </div>
      <p class="num mt-1 text-[13px] text-muted-foreground">{order.id} · <button class="text-primary" onclick={() => copy(order.id)}>Copy</button></p>
    </div>
    <div class="flex gap-2">
      <Button variant="outline" onclick={compare} disabled={comparing}>{comparing ? 'Checking…' : 'Compare with broker'}</Button>
      {#if canCancel(order.status)}<Button variant="outline" class="border-red-200 bg-red-50 text-loss hover:bg-red-100" onclick={() => (confirming = true)}>Cancel order</Button>{/if}
    </div>
  </div>

  <Card.Root class="bg-gradient-to-b from-muted to-card text-center shadow-none">
    <Card.Content>
      <div><span class="mr-1.5 font-bold text-primary">USD</span><span class="num text-5xl font-bold">{formatMoney(order.qty * order.price)}</span></div>
      <p class="mt-2 text-muted-foreground">{order.qty} shares at {formatMoney(order.price)} · {outcomeLine(order)}</p>
      <div class="mx-auto mt-3 max-w-sm">
        <div class="h-1.5 overflow-hidden rounded-full bg-border"><div class="h-full rounded-full bg-primary transition-all" style="width: {(order.filled_qty / order.qty) * 100}%"></div></div>
        <div class="mt-1.5 text-xs text-muted-foreground">{order.filled_qty} of {order.qty} filled</div>
      </div>
    </Card.Content>
  </Card.Root>

  <div class="steps mt-6">
    {#each steps as step, i}
      {#if i > 0}<div class="step-line" class:done={step.state === 'done' || step.state === 'bad'}></div>{/if}
      <div class="step {step.state}"><div class="dot">{step.state === 'done' ? '✓' : step.state === 'bad' ? '✕' : step.state === 'now' ? '…' : ''}</div>{step.label}</div>
    {/each}
  </div>

  {#if comparison}
    <section class="mt-8">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="text-xl font-bold">Our books vs the broker</h2>
        <Badge class="border-transparent {comparison.agree ? 'tone-completed' : 'tone-failed'}">{comparison.agree ? 'In agreement' : `${comparison.mismatches.length} difference(s)`}</Badge>
      </div>
      <Card.Root class="py-0 shadow-none">
        <Table.Root>
          <Table.Header><Table.Row><Table.Head class="pl-5"></Table.Head><Table.Head>Our order</Table.Head><Table.Head>Broker</Table.Head></Table.Row></Table.Header>
          <Table.Body>
            <Table.Row><Table.Cell class="pl-5 text-muted-foreground">Status</Table.Cell><Table.Cell>{comparison.order.status}</Table.Cell><Table.Cell>{comparison.provider?.status ?? '— (not at the broker)'}</Table.Cell></Table.Row>
            <Table.Row><Table.Cell class="pl-5 text-muted-foreground">Filled</Table.Cell><Table.Cell class="num">{comparison.order.filled_qty} / {comparison.order.qty}</Table.Cell><Table.Cell class="num">{comparison.provider ? `${comparison.provider.filled_qty} / ${comparison.provider.qty}` : '—'}</Table.Cell></Table.Row>
            <Table.Row><Table.Cell class="pl-5 text-muted-foreground">Fills</Table.Cell><Table.Cell class="num">{comparison.fills.length}</Table.Cell><Table.Cell class="num">{comparison.provider?.fill_ids.length ?? '—'}</Table.Cell></Table.Row>
          </Table.Body>
        </Table.Root>
      </Card.Root>
      {#if comparison.mismatches.length}
        <div class="mt-3 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900">
          {#each comparison.mismatches as m}<div><strong>{m.kind.replaceAll('_', ' ')}</strong>: ours {m.local || '—'}, broker {m.provider || '—'}</div>{/each}
          <div class="mt-1.5 text-xs">Usually a webhook still on its way; the sweeper repairs anything that stays different.</div>
        </div>
      {/if}
    </section>
  {/if}

  <div class="mt-8 grid gap-6 lg:grid-cols-[3fr_2fr]">
    <div class="grid gap-6">
      <section>
        <h2 class="mb-3 text-xl font-bold">Order information</h2>
        <Card.Root class="gap-0 py-0 shadow-none">
          {#each [
            ['Price per share', formatMoney(order.price)],
            ['Average fill price', order.avg_fill_price ? formatMoney(order.avg_fill_price) : '—'],
            [order.side === 'BUY' ? 'Cash still held' : 'Shares still held', order.side === 'BUY' ? formatMoney(order.hold_remaining) : String(order.hold_remaining)],
            ['Broker order id', order.provider_order_id ?? 'Not yet assigned'],
            ['Quote', order.quote_id],
            ['Placed', when(order.created_at)],
            ['Last updated', when(order.updated_at)],
          ] as [k, v]}
            <div class="flex justify-between gap-4 border-b px-5 py-3.5 text-sm last:border-b-0"><span class="text-muted-foreground">{k}</span><span class="num text-right font-semibold break-all">{v}</span></div>
          {/each}
        </Card.Root>
      </section>

      <section>
        <h2 class="mb-3 text-xl font-bold">Fills</h2>
        <Card.Root class="py-0 shadow-none">
          {#if order.fills.length === 0}
            <p class="py-8 text-center text-sm text-muted-foreground">No fills yet.</p>
          {:else}
            <Table.Root>
              <Table.Header><Table.Row><Table.Head class="pl-5">Fill</Table.Head><Table.Head class="text-right">Qty</Table.Head><Table.Head class="text-right">Price</Table.Head><Table.Head class="text-right">Fee</Table.Head><Table.Head class="pr-5">When</Table.Head></Table.Row></Table.Header>
              <Table.Body>
                {#each order.fills as f (f.id)}
                  <Table.Row><Table.Cell class="num pl-5 text-xs">{f.id}</Table.Cell><Table.Cell class="num text-right">{f.qty}</Table.Cell><Table.Cell class="num text-right">{formatMoney(f.price)}</Table.Cell><Table.Cell class="num text-right">{formatMoney(f.fee)}</Table.Cell><Table.Cell class="pr-5 text-xs">{when(f.created_at)}</Table.Cell></Table.Row>
                {/each}
              </Table.Body>
            </Table.Root>
          {/if}
        </Card.Root>
      </section>
    </div>

    <section>
      <div class="mb-3 flex items-center justify-between"><h2 class="text-xl font-bold">Activity</h2><span class="flex items-center gap-1.5 text-xs text-muted-foreground"><span class="live-dot" class:on={live.connected}></span>Live</span></div>
      <Card.Root class="shadow-none">
        <Card.Content>
          <ul class="timeline">
            {#each [...order.activity].reverse() as a (a.id)}
              <li><div class="text-sm">{a.message}</div><div class="text-xs text-muted-foreground">{when(a.created_at)}</div></li>
            {/each}
          </ul>
        </Card.Content>
      </Card.Root>
    </section>
  </div>

  <Dialog.Root bind:open={confirming}>
    <Dialog.Content class="sm:max-w-md">
      <Dialog.Header>
        <Dialog.Title>Cancel this order?</Dialog.Title>
        <Dialog.Description>
          {#if order.status === 'PENDING_SUBMIT'}
            It hasn't reached the broker yet, so it can be cancelled straight away.
          {:else}
            The broker decides. Shares can still fill while the cancel is in flight; your cash is only released once the order is final.
          {/if}
        </Dialog.Description>
      </Dialog.Header>
      <Dialog.Footer>
        <Button variant="outline" onclick={() => (confirming = false)}>Keep order</Button>
        <Button variant="destructive" onclick={cancel} disabled={cancelling}>{cancelling ? 'Cancelling…' : 'Cancel order'}</Button>
      </Dialog.Footer>
    </Dialog.Content>
  </Dialog.Root>
{/if}
