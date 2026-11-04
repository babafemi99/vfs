<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Table from '$lib/components/ui/table/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import PageHeader from '../components/PageHeader.svelte'
  import { sim } from '../lib/sim.js'
  import { formatMoney } from '../lib/money.js'
  import { toast } from '../lib/toast.js'

  let orders = $state([])
  let deliveries = $state([])
  let engine = $state(null)
  let error = $state('')

  const working = (s) => ['OPEN', 'PARTIALLY_FILLED', 'PENDING_CANCEL'].includes(s)
  const tone = (s) => ({ FILLED: 'tone-completed', REJECTED: 'tone-failed', CANCELLED: 'tone-cancelled', EXPIRED: 'tone-cancelled', delivered: 'tone-completed', failed: 'tone-failed' })[s] ?? 'tone-processing'

  async function load() {
    try {
      const [o, d, e] = await Promise.all([sim.orders(), sim.deliveries(), sim.engine()])
      orders = o.orders ?? []
      deliveries = d.deliveries ?? []
      engine = e
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

  async function toggle(key) {
    try {
      engine = await sim.setEngine({ [key]: !engine[key] })
      toast(`${key.replaceAll('_', ' ')}: ${engine[key] ? 'on' : 'off'}`)
    } catch (err) {
      toast('Could not change the engine', err.message, 'error')
    }
  }
</script>

<PageHeader eyebrow="Provider console" title="The broker's side" description="brokersim: every action goes through its state machine and sends the same signed webhook its engine would.">
  {#snippet actions()}
    {#if engine}
      {#each ['auto_fill', 'auto_resolve_cancels', 'market_open'] as key}
        <Button variant={engine[key] ? 'default' : 'outline'} size="sm" onclick={() => toggle(key)}>{key.replaceAll('_', ' ')}: {engine[key] ? 'on' : 'off'}</Button>
      {/each}
    {/if}
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

<h2 class="mt-8 mb-3 text-xl font-bold">Webhook deliveries</h2>
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
