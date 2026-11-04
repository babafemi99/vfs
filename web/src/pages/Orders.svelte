<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Select from '$lib/components/ui/select/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Input } from '$lib/components/ui/input/index.js'
  import { Skeleton } from '$lib/components/ui/skeleton/index.js'
  import Plus from '@lucide/svelte/icons/plus'
  import Search from '@lucide/svelte/icons/search'
  import ChevronRight from '@lucide/svelte/icons/chevron-right'
  import PageHeader from '../components/PageHeader.svelte'
  import StatusPill from '../components/StatusPill.svelte'
  import { api } from '../lib/api.js'
  import { live } from '../lib/live.svelte.js'
  import { formatMoney } from '../lib/money.js'
  import { when, outcomeLine, isFinal, shortId, statusLabel } from '../lib/format.js'

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

  // The outcome line only when it says more than the status badge
  // ("2 of 5 bought, 3 cancelled", "Rejected: market closed").
  function detail(o) {
    if (o.status === 'OPEN' || o.status === 'PENDING_SUBMIT') return ''
    const line = outcomeLine(o)
    return line === statusLabel(o.status) ? '' : line
  }

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
  {#snippet actions()}<Button size="lg" href="#/trade"><Plus class="size-4" />New order</Button>{/snippet}
</PageHeader>

<div class="mb-4 flex flex-col gap-3 sm:flex-row">
  <div class="relative flex-1">
    <Search class="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-muted-foreground" />
    <Input class="h-11 pl-10" placeholder="Search by symbol or order id" bind:value={search} />
  </div>
  <Select.Root type="single" bind:value={filter}>
    <Select.Trigger class="h-11 w-full sm:w-48">{filters[filter]}</Select.Trigger>
    <Select.Content>
      {#each Object.entries(filters) as [value, label]}<Select.Item {value} {label}>{label}</Select.Item>{/each}
    </Select.Content>
  </Select.Root>
</div>

{#if !loaded}
  <div class="grid gap-2">{#each Array(4) as _}<Skeleton class="h-16 rounded-xl" />{/each}</div>
{:else if shown.length === 0}
  <Card.Root class="shadow-none"><Card.Content class="py-8 text-center text-sm text-muted-foreground">
    No orders{search || filter !== 'all' ? ' match' : ' yet'}. <a class="text-primary" href="#/trade">Place one</a>.
  </Card.Content></Card.Root>
{:else}
  <Card.Root class="gap-0 overflow-hidden py-0 shadow-none">
    {#each shown as o (o.id)}
      {@const extra = detail(o)}
      {@const filling = !isFinal(o.status) && o.filled_qty > 0}
      <a href="#/orders/{o.id}" class="group flex items-center gap-3 border-b px-4 py-3.5 text-foreground transition-colors last:border-b-0 hover:bg-muted/70 hover:no-underline sm:gap-4 sm:px-5">
        <span class="w-11 shrink-0 rounded-md py-1 text-center text-[11px] font-bold tracking-wide {o.side === 'BUY' ? 'bg-emerald-50 text-gain' : 'bg-red-50 text-loss'}">{o.side}</span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span class="font-semibold">{o.qty} {o.symbol}</span>
            <StatusPill status={o.status} />
          </div>
          <div class="mt-0.5 truncate text-xs text-muted-foreground">
            {#if extra}<span class="text-foreground/80">{extra}</span>{' · '}{/if}{when(o.created_at)}<span class="max-sm:hidden">{' · '}<span class="font-mono">{shortId(o.id)}</span></span>
          </div>
        </div>
        <div class="shrink-0 text-right">
          <div class="num font-semibold">{formatMoney(o.qty * o.price)}</div>
          {#if filling}
            <div class="mt-1 ml-auto h-1 w-20 overflow-hidden rounded-full bg-border" title="{o.filled_qty} of {o.qty} filled">
              <div class="h-full rounded-full bg-primary transition-all" style="width: {(o.filled_qty / o.qty) * 100}%"></div>
            </div>
          {:else}
            <div class="num text-xs text-muted-foreground">@ {formatMoney(o.price)}</div>
          {/if}
        </div>
        <ChevronRight class="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 max-sm:hidden" />
      </a>
    {/each}
  </Card.Root>
{/if}
