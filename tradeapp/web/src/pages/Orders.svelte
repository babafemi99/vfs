<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Select from '$lib/components/ui/select/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Input } from '$lib/components/ui/input/index.js'
  import { Skeleton } from '$lib/components/ui/skeleton/index.js'
  import PageHeader from '../components/PageHeader.svelte'
  import StatusPill from '../components/StatusPill.svelte'
  import { api } from '../lib/api.js'
  import { live } from '../lib/live.svelte.js'
  import { formatMoney } from '../lib/money.js'
  import { when, outcomeLine, isFinal, shortId } from '../lib/format.js'

  const filters = { all: 'All statuses', open: 'Working', final: 'Finished' }

  let orders = $state([])
  let loaded = $state(false)
  let search = $state('')
  let filter = $state('all')

  $effect(() => {
    live.version
    api.orders('all').then((r) => {
      orders = r.orders
      loaded = true
    })
  })

  const shown = $derived(
    orders.filter((o) => {
      if (filter === 'open' && isFinal(o.status)) return false
      if (filter === 'final' && !isFinal(o.status)) return false
      const q = search.trim().toLowerCase()
      return !q || o.id.toLowerCase().includes(q) || o.symbol.toLowerCase().includes(q)
    }),
  )
</script>

<PageHeader eyebrow="Orders" title="Your orders" description="Every order with its honest outcome, updated live.">
  {#snippet actions()}<Button size="lg" href="#/trade">+ New order</Button>{/snippet}
</PageHeader>

<div class="mb-4 flex gap-3">
  <Input class="h-11 flex-1" placeholder="Search by order id or symbol" bind:value={search} />
  <Select.Root type="single" bind:value={filter}>
    <Select.Trigger class="h-11 w-48">{filters[filter]}</Select.Trigger>
    <Select.Content>
      {#each Object.entries(filters) as [value, label]}<Select.Item {value} {label}>{label}</Select.Item>{/each}
    </Select.Content>
  </Select.Root>
</div>

{#if !loaded}
  <div class="grid gap-2">{#each Array(4) as _}<Skeleton class="h-24 rounded-xl" />{/each}</div>
{:else if shown.length === 0}
  <Card.Root class="shadow-none"><Card.Content class="py-8 text-center text-sm text-muted-foreground">
    No orders{search || filter !== 'all' ? ' match' : ' yet'}. <a class="text-primary" href="#/trade">Place one</a>.
  </Card.Content></Card.Root>
{:else}
  <Card.Root class="gap-0 overflow-hidden py-0 shadow-none">
    {#each shown as o (o.id)}
      <a href="#/orders/{o.id}" class="flex items-center justify-between gap-4 border-b border-l-[3px] border-l-transparent px-6 py-4 text-foreground transition-colors last:border-b-0 hover:border-l-primary hover:bg-muted hover:no-underline">
        <div class="min-w-0">
          <div>
            <span class="text-xs font-bold tracking-wider {o.side === 'BUY' ? 'text-gain' : 'text-loss'}">{o.side}</span>
            <span class="font-semibold">{o.qty} {o.symbol}</span>
            <span class="text-sm text-muted-foreground">at {formatMoney(o.price)}</span>
          </div>
          <div class="num text-[13px] text-muted-foreground">{shortId(o.id)}</div>
          <div class="mt-2 flex flex-wrap items-center gap-2.5 text-[13px] text-muted-foreground">
            <StatusPill status={o.status} /><span>{outcomeLine(o)}</span><span>·</span><span>{when(o.created_at)}</span>
          </div>
        </div>
        <div class="flex items-center gap-4">
          <div class="w-36 text-right">
            <div class="num text-[17px] font-bold">{formatMoney(o.qty * o.price)}</div>
            <div class="text-xs text-muted-foreground">{o.filled_qty} / {o.qty} filled</div>
            <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-border"><div class="h-full rounded-full bg-primary transition-all" style="width: {(o.filled_qty / o.qty) * 100}%"></div></div>
          </div>
          <span class="text-xl text-muted-foreground">›</span>
        </div>
      </a>
    {/each}
  </Card.Root>
{/if}
