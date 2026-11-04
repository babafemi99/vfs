<script>
  import { Toaster } from 'svelte-sonner'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import Wallet from '@lucide/svelte/icons/wallet'
  import ArrowLeftRight from '@lucide/svelte/icons/arrow-left-right'
  import List from '@lucide/svelte/icons/list'
  import WebhookIcon from '@lucide/svelte/icons/webhook'
  import Activity from '@lucide/svelte/icons/activity'
  import ExternalLink from '@lucide/svelte/icons/external-link'
  import { route } from './lib/router.svelte.js'
  import { live, connect, onActivity } from './lib/live.svelte.js'
  import { toast } from './lib/toast.js'
  import Portfolio from './pages/Portfolio.svelte'
  import Trade from './pages/Trade.svelte'
  import Orders from './pages/Orders.svelte'
  import OrderDetail from './pages/OrderDetail.svelte'
  import Operations from './pages/Operations.svelte'
  import Webhooks from './pages/Webhooks.svelte'
  import Provider from './pages/Provider.svelte'

  // Brokersim serves its own console; it opens in a new tab.
  const providerConsole = import.meta.env.VITE_PROVIDER_CONSOLE_URL || '/#/provider'

  connect()

  // Tell the user when the broker moves one of their orders.
  onActivity((item) => {
    const who = item.data?.symbol ? `${item.data.side} ${item.data.qty} ${item.data.symbol}` : ''
    if (item.kind === 'order.fill') toast(item.message, who, 'success')
    else if (item.kind === 'order.status') toast(who || 'Order update', item.message, 'info')
    else if (item.kind === 'order.rejected') toast(who || 'Order rejected', item.message, 'error')
  })

  const tabs = [
    ['portfolio', '#/', 'Portfolio', Wallet],
    ['trade', '#/trade', 'Trade', ArrowLeftRight],
    ['orders', '#/orders', 'Orders', List],
    ['webhooks', '#/webhooks', 'Webhooks', WebhookIcon],
    ['ops', '#/ops', 'Operations', Activity],
  ]
  const features = ['Signed webhooks', 'Replay protection', 'Idempotent APIs', 'Double-entry ledger', 'Outbox + DLQ']
</script>

{#if route.page === 'provider'}
  <!-- The provider's own screen: brokersim branding, no trade navigation. -->
  <div class="shell">
    <header class="flex items-center justify-between border-b-2 border-brand-purple px-5 py-4 sm:px-8 sm:py-5">
      <div class="flex items-center gap-2.5 text-xl font-bold">
        <span class="grid size-[30px] place-items-center rounded-[9px] bg-foreground text-sm text-white">B</span>
        Brokersim
      </div>
      <div class="flex items-center gap-3 text-sm">
        <span>Provider</span>
        <Badge class="border-transparent bg-accent text-brand-purple">Simulator</Badge>
      </div>
    </header>
    <main class="p-4 sm:p-8"><Provider /></main>
    <footer class="flex flex-wrap justify-center gap-x-6 gap-y-1 border-t bg-muted px-5 py-4 text-xs text-muted-foreground">
      {#each ['Signed webhooks', 'Per-attempt nonces', 'Versioned snapshots', 'Idempotent accepts'] as f}<span>{f}</span>{/each}
    </footer>
  </div>
{:else}
<div class="shell">
  <header class="topbar-line flex items-center justify-between gap-3 px-5 py-4 sm:px-8 sm:py-5">
    <a class="flex items-center gap-2.5 text-xl font-bold hover:no-underline" href="#/">
      <svg width="30" height="30" viewBox="0 0 32 32" aria-hidden="true">
        <defs><linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="#2563EB" /><stop offset="1" stop-color="#6D4AFF" /></linearGradient></defs>
        <rect width="32" height="32" rx="9" fill="url(#g)" />
        <path d="M8 21l6-6 4 4 6-8" stroke="white" stroke-width="3" fill="none" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      Tradelab
    </a>
    <div class="flex items-center gap-3 text-sm">
      <span class="flex items-center gap-1.5 text-muted-foreground" title={live.connected ? 'Live updates connected' : 'Reconnecting to live updates'}>
        <span class="live-dot" class:on={live.connected}></span><span class="max-sm:sr-only">{live.connected ? 'Live' : 'Reconnecting…'}</span>
      </span>
      <Button variant="outline" size="sm" href={providerConsole} target="_blank" rel="noopener">
        Provider <ExternalLink class="size-3.5" />
      </Button>
    </div>
  </header>

  <nav class="no-scrollbar flex gap-1 overflow-x-auto border-b px-3 pt-2 sm:px-7" aria-label="Main">
    {#each tabs as [page, href, label, Icon]}
      <a
        {href}
        class="-mb-px flex shrink-0 items-center gap-2 border-b-2 px-3 py-2.5 text-sm font-semibold whitespace-nowrap transition-colors hover:text-foreground hover:no-underline
          {route.page === page ? 'border-brand-purple text-foreground' : 'border-transparent text-muted-foreground'}"
        aria-current={route.page === page ? 'page' : undefined}><Icon class="size-4 opacity-70" />{label}</a>
    {/each}
  </nav>

  <main class="p-4 sm:p-8">
    {#if route.page === 'portfolio'}
      <Portfolio />
    {:else if route.page === 'trade'}
      <Trade />
    {:else if route.page === 'orders' && route.id}
      {#key route.id}<OrderDetail id={route.id} />{/key}
    {:else if route.page === 'orders'}
      <Orders />
    {:else if route.page === 'webhooks'}
      <Webhooks />
    {:else if route.page === 'ops'}
      <Operations {providerConsole} />
    {:else}
      <p class="py-10 text-center text-muted-foreground">Page not found. <a class="text-primary" href="#/">Back to portfolio</a></p>
    {/if}
  </main>

  <footer class="flex flex-wrap justify-center gap-x-6 gap-y-1 border-t bg-muted px-5 py-4 text-xs text-muted-foreground">
    {#each features as f}<span>{f}</span>{/each}
  </footer>
</div>
{/if}

<Toaster position="top-right" richColors closeButton />
