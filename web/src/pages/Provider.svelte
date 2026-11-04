<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Table from '$lib/components/ui/table/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import * as Dialog from '$lib/components/ui/dialog/index.js'
  import PageHeader from '../components/PageHeader.svelte'
  import { sim } from '../lib/sim.js'
  import { formatMoney } from '../lib/money.js'
  import { toast } from '../lib/toast.js'

  let orders = $state([])
  let deliveries = $state([])
  let engine = $state(null)
  let prices = $state([])
  let error = $state('')
  let resetting = $state(false)

  const working = (s) => ['OPEN', 'PARTIALLY_FILLED', 'PENDING_CANCEL'].includes(s)
  const tone = (s) => ({ FILLED: 'tone-completed', REJECTED: 'tone-failed', CANCELLED: 'tone-cancelled', EXPIRED: 'tone-cancelled', delivered: 'tone-completed', failed: 'tone-failed' })[s] ?? 'tone-processing'

  async function load() {
    try {
      const [o, d, e, p] = await Promise.all([sim.orders(), sim.deliveries(), sim.engine(), sim.prices()])
      orders = o.orders ?? []
      deliveries = d.deliveries ?? []
      engine = e
      prices = p.prices ?? []
      error = ''
    } catch (err) {
      error = `Cannot reach brokersim: ${err.message}`
    }
  }

  $effect(() => {
    load()
    const timer = setInterval(load, 1000)
    return () => clearInterval(timer)
  })

  async function act(o, action, label, body) {
    try {
      await sim.act(o.id, action, body)
      toast(`${label}: ${o.side} ${o.qty} ${o.symbol}`, 'Signed webhook sent to the trade app.', 'success')
      load()
    } catch (err) {
      toast(`${label} failed`, err.message, 'error')
    }
  }

  async function reset() {
    try {
      await sim.reset()
      resetting = false
      toast('Broker reset', 'Every quote, order and delivery at the broker was cleared.', 'warning')
      load()
    } catch (err) {
      toast('Reset failed', err.message, 'error')
    }
  }

  async function toggle(key) {
    try {
      engine = await sim.setEngine({ [key]: !engine[key] })
      toast(`${key.replaceAll('_', ' ')}: ${engine[key] ? 'on' : 'off'}`)
    } catch (err) {
      toast('Could not change the engine', err.message, 'error')
    }
  }
</script>

<PageHeader eyebrow="Provider console" title="The broker's side" description="Every action goes through brokersim's state machine and sends the same signed webhook its engine would. The trade app only learns about it from that webhook, or from its sweeper.">
  {#snippet actions()}
    {#if engine}
      {#each ['auto_fill', 'auto_resolve_cancels', 'market_open'] as key}
        <Button variant={engine[key] ? 'default' : 'outline'} size="sm" onclick={() => toggle(key)}>{key.replaceAll('_', ' ')}: {engine[key] ? 'on' : 'off'}</Button>
      {/each}
    {/if}
    <Button variant="outline" size="sm" class="border-red-200 bg-red-50 text-loss hover:bg-red-100" onclick={() => (resetting = true)}>Reset broker</Button>
  {/snippet}
</PageHeader>

{#if error}<p class="mb-4 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p>{/if}

<h2 class="mb-3 text-xl font-bold">Orders</h2>
<Card.Root class="py-0 shadow-none">
  {#if orders.length === 0}
    <p class="py-8 text-center text-sm text-muted-foreground">No orders at the broker yet. Place one in the trade console.</p>
  {:else}
    <Table.Root>
      <Table.Header><Table.Row><Table.Head class="pl-5">Order</Table.Head><Table.Head>Status</Table.Head><Table.Head>Filled</Table.Head><Table.Head>Version</Table.Head><Table.Head class="pr-5">Actions</Table.Head></Table.Row></Table.Header>
      <Table.Body>
        {#each orders as o (o.id)}
          <Table.Row>
            <Table.Cell class="pl-5"><span class="text-xs font-bold {o.side === 'BUY' ? 'text-gain' : 'text-loss'}">{o.side}</span> <span class="font-semibold">{o.qty} {o.symbol}</span> <span class="text-muted-foreground">@ {formatMoney(o.price)}</span><div class="num text-xs text-muted-foreground">{o.id}</div></Table.Cell>
            <Table.Cell><Badge class="border-transparent {tone(o.status)}">{o.status.replace('_', ' ').toLowerCase()}</Badge></Table.Cell>
            <Table.Cell class="num">{o.filled_qty} / {o.qty}</Table.Cell>
            <Table.Cell class="num">v{o.version}</Table.Cell>
            <Table.Cell class="pr-5">
              <div class="flex flex-wrap gap-1.5">
                {#if working(o.status)}
                  <Button size="sm" variant="outline" onclick={() => act(o, 'fill', 'Fill 1', { qty: 1 })}>Fill 1</Button>
                  <Button size="sm" onclick={() => act(o, 'fill', 'Fill all')}>Fill all</Button>
                {/if}
                {#if o.status === 'OPEN'}<Button size="sm" variant="outline" class="text-loss" onclick={() => act(o, 'reject', 'Reject')}>Reject</Button>{/if}
                {#if o.status === 'OPEN' || o.status === 'PARTIALLY_FILLED'}<Button size="sm" variant="outline" onclick={() => act(o, 'expire', 'Expire')}>Expire</Button>{/if}
                {#if o.status === 'PENDING_CANCEL'}
                  <Button size="sm" onclick={() => act(o, 'cancel/confirm', 'Confirm cancel')}>Confirm cancel</Button>
                  <Button size="sm" variant="outline" class="text-loss" onclick={() => act(o, 'cancel/reject', 'Reject cancel')}>Reject cancel</Button>
                {/if}
                {#if !working(o.status)}<span class="text-xs text-muted-foreground">Final</span>{/if}
              </div>
            </Table.Cell>
          </Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  {/if}
</Card.Root>

<div class="mt-8 grid gap-6 lg:grid-cols-[2fr_1fr]">
<section>
<h2 class="mb-3 text-xl font-bold">Webhook deliveries</h2>
<Card.Root class="max-h-[420px] overflow-y-auto py-0 shadow-none">
  {#if deliveries.length === 0}
    <p class="py-8 text-center text-sm text-muted-foreground">No webhooks sent yet.</p>
  {:else}
    <Table.Root>
      <Table.Header><Table.Row><Table.Head class="pl-5">Event</Table.Head><Table.Head>Type</Table.Head><Table.Head>State</Table.Head><Table.Head>Attempts</Table.Head><Table.Head class="pr-5"></Table.Head></Table.Row></Table.Header>
      <Table.Body>
        {#each deliveries as d (d.event_id)}
          <Table.Row>
            <Table.Cell class="num pl-5 text-xs">{d.event_id}</Table.Cell>
            <Table.Cell class="text-xs">{d.type}</Table.Cell>
            <Table.Cell><Badge class="border-transparent {tone(d.state)}">{d.state}</Badge></Table.Cell>
            <Table.Cell class="num">{d.attempts}</Table.Cell>
            <Table.Cell class="pr-5 text-right">{#if d.state !== 'pending'}<Button size="sm" variant="outline" onclick={async () => { await sim.redeliver(d.event_id); toast('Redelivered', d.event_id, 'success') }}>Redeliver</Button>{/if}</Table.Cell>
          </Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  {/if}
</Card.Root>
</section>

<section>
  <div class="mb-3 flex items-center justify-between"><h2 class="text-xl font-bold">Market</h2><span class="text-xs text-muted-foreground">Bid / ask</span></div>
  <Card.Root class="py-0 shadow-none">
    <Table.Root>
      <Table.Header><Table.Row><Table.Head class="pl-5">Symbol</Table.Head><Table.Head class="text-right">Bid</Table.Head><Table.Head class="pr-5 text-right">Ask</Table.Head></Table.Row></Table.Header>
      <Table.Body>
        {#each prices as p (p.symbol)}
          <Table.Row><Table.Cell class="pl-5 font-semibold">{p.symbol}</Table.Cell><Table.Cell class="num text-right">{formatMoney(p.bid)}</Table.Cell><Table.Cell class="num pr-5 text-right">{formatMoney(p.ask)}</Table.Cell></Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  </Card.Root>
</section>
</div>

<Dialog.Root bind:open={resetting}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>Reset the broker?</Dialog.Title>
      <Dialog.Description>Clears every quote, order and webhook delivery at brokersim. The trade app keeps its own records, so its open orders will show as missing at the provider in reconciliation.</Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => (resetting = false)}>Keep everything</Button>
      <Button variant="destructive" onclick={reset}>Reset broker</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
