<script>
  import { Toaster } from 'svelte-sonner'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import { route } from './lib/router.svelte.js'
  import { live, connect, onActivity } from './lib/live.svelte.js'
  import { toast } from './lib/toast.js'
  import Portfolio from './pages/Portfolio.svelte'
  import Trade from './pages/Trade.svelte'
  import Orders from './pages/Orders.svelte'
  import OrderDetail from './pages/OrderDetail.svelte'
  import Operations from './pages/Operations.svelte'
  import Webhooks from './pages/Webhooks.svelte'

  // Brokersim serves its own console; it opens in a new tab.
  const providerConsole = import.meta.env.VITE_PROVIDER_CONSOLE_URL || 'http://localhost:8090/dev'

  connect()

  // Tell the user when the broker moves one of their orders.
  onActivity((item) => {
    const who = item.data?.symbol ? `${item.data.side} ${item.data.qty} ${item.data.symbol}` : ''
    if (item.kind === 'order.fill') toast(item.message, who, 'success')
    else if (item.kind === 'order.status') toast(who || 'Order update', item.message, 'info')
    else if (item.kind === 'order.rejected') toast(who || 'Order rejected', item.message, 'error')
  })

  const tabs = [
    ['portfolio', '#/', 'Portfolio'],
    ['trade', '#/trade', 'Trade'],
    ['orders', '#/orders', 'Orders'],
    ['webhooks', '#/webhooks', 'Webhooks'],
    ['ops', '#/ops', 'Operations'],
  ]
</script>

<div class="shell">
  <header class="topbar-line flex items-center justify-between px-8 py-5">
    <a class="flex items-center gap-2.5 text-xl font-bold hover:no-underline" href="#/">
      <svg width="30" height="30" viewBox="0 0 32 32" aria-hidden="true">
        <defs><linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="#2563EB" /><stop offset="1" stop-color="#6D4AFF" /></linearGradient></defs>
        <rect width="32" height="32" rx="9" fill="url(#g)" />
        <path d="M8 21l6-6 4 4 6-8" stroke="white" stroke-width="3" fill="none" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      Tradelab
    </a>
    <div class="flex items-center gap-3 text-sm">
      <span class="flex items-center gap-1.5 text-muted-foreground">
        <span class="live-dot" class:on={live.connected}></span>{live.connected ? 'Live' : 'Reconnecting…'}
      </span>
      <Button variant="outline" size="sm" href={providerConsole} target="_blank" rel="noopener">Provider ↗</Button>
      <span>Developer</span>
      <Badge class="border-transparent bg-accent text-brand-purple">Demo</Badge>
    </div>
  </header>

  <nav class="flex gap-1 border-b px-7 pt-2.5" aria-label="Main">
    {#each tabs as [page, href, label]}
      <a
        {href}
        class="-mb-px border-b-2 px-3.5 py-2.5 text-sm font-semibold transition-colors hover:text-foreground hover:no-underline
          {route.page === page ? 'border-brand-purple text-foreground' : 'border-transparent text-muted-foreground'}"
        aria-current={route.page === page ? 'page' : undefined}>{label}</a>
    {/each}
  </nav>

  <main class="p-8 max-md:p-4">
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

  <footer class="flex justify-center gap-8 border-t bg-muted px-8 py-4 text-[13px] text-muted-foreground">
    <span>Signed webhooks</span><span>Replay protection</span><span>Idempotent APIs</span><span>Double-entry ledger</span><span>Outbox + DLQ</span>
  </footer>
</div>

<Toaster position="top-right" richColors closeButton />
