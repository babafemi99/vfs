<script>
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Table from '$lib/components/ui/table/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Skeleton } from '$lib/components/ui/skeleton/index.js'
  import Plus from '@lucide/svelte/icons/plus'
  import PageHeader from '../components/PageHeader.svelte'
  import Stat from '../components/Stat.svelte'
  import FundDialog from '../components/FundDialog.svelte'
  import { api } from '../lib/api.js'
  import { live } from '../lib/live.svelte.js'
  import { formatMoney, formatSigned, pnlClass } from '../lib/money.js'
  import { when, orderLabel } from '../lib/format.js'

  let account = $state(null)
  let prices = $state([])
  let error = $state('')
  let funding = $state(false)

  async function loadAccount() {
    try {
      account = await api.account()
      error = ''
    } catch (err) {
      error = err.message
    }
  }

  async function loadPrices() {
    try {
      prices = (await api.prices()).prices
    } catch {
      // the broker may be down; the rest of the page still works
    }
  }

  // Reload when the activity stream says something happened.
  $effect(() => {
    live.version
    loadAccount()
  })

  // Prices move every tick at the broker, and positions value and
  // unrealized P&L move with them: refresh both together, so the tiles and
  // the positions table never disagree with the Prices table.
  $effect(() => {
    loadPrices()
    const timer = setInterval(() => {
      loadPrices()
      loadAccount()
    }, 2000)
    return () => clearInterval(timer)
  })
</script>

<PageHeader eyebrow="Portfolio" title="Your account" description="Cash, holdings and live prices from the broker.">
  {#snippet actions()}<Button size="lg" onclick={() => (funding = true)}><Plus class="size-4" />Fund account</Button>{/snippet}
</PageHeader>

{#if error}<p class="mb-4 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p>{/if}

{#if !account}
  <div class="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">{#each Array(4) as _}<Skeleton class="h-28 rounded-xl" />{/each}</div>
{:else}
  <div class="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">
    <Stat label="Available cash" value={formatMoney(account.cash.available)} hint="Ready to trade" />
    <Stat label="Held for orders" value={formatMoney(account.cash.held)} hint="Released when orders finish" />
    <Stat label="Positions value" value={formatMoney(account.positions_value)} hint="At current bid" />
    <Stat label="Unrealized P&L" value={formatSigned(account.unrealized_pnl)} valueClass={pnlClass(account.unrealized_pnl)} hint="Realized {formatSigned(account.realized_pnl)}" />
  </div>

  <div class="mt-8 grid gap-6 lg:grid-cols-[3fr_2fr]">
    <section>
      <div class="mb-3 flex items-center justify-between"><h2 class="text-xl font-bold">Positions</h2><Button variant="outline" size="sm" href="#/trade">Trade</Button></div>
      <Card.Root class="py-0 shadow-none">
        {#if account.positions.length === 0}
          <p class="py-10 text-center text-sm text-muted-foreground">No positions yet. Fund the account, then <a class="text-primary" href="#/trade">place a trade</a>.</p>
        {:else}
          <Table.Root>
            <Table.Header>
              <Table.Row><Table.Head class="pl-5">Symbol</Table.Head><Table.Head class="text-right">Qty</Table.Head><Table.Head class="text-right">Avg cost</Table.Head><Table.Head class="text-right">Value</Table.Head><Table.Head class="pr-5 text-right">P&L</Table.Head></Table.Row>
            </Table.Header>
            <Table.Body>
              {#each account.positions as p (p.symbol)}
                <Table.Row>
                  <Table.Cell class="pl-5"><span class="font-semibold">{p.symbol}</span>{#if p.held_qty > 0}<div class="text-xs text-muted-foreground">{p.held_qty} held for sells</div>{/if}</Table.Cell>
                  <Table.Cell class="num text-right">{p.qty}</Table.Cell>
                  <Table.Cell class="num text-right">{formatMoney(p.avg_cost)}</Table.Cell>
                  <Table.Cell class="num text-right">{formatMoney(p.market_value)}</Table.Cell>
                  <Table.Cell class="num pr-5 text-right {pnlClass(p.unrealized_pnl)}">{formatSigned(p.unrealized_pnl)}</Table.Cell>
                </Table.Row>
              {/each}
            </Table.Body>
          </Table.Root>
        {/if}
      </Card.Root>
    </section>

    <section>
      <div class="mb-3 flex items-center justify-between"><h2 class="text-xl font-bold">Prices</h2><span class="text-xs text-muted-foreground">Bid / ask · every 2s</span></div>
      <Card.Root class="py-0 shadow-none">
        {#if prices.length === 0}
          <p class="py-10 text-center text-sm text-muted-foreground">Prices unavailable: is the broker running?</p>
        {:else}
          <Table.Root>
            <Table.Header><Table.Row><Table.Head class="pl-5">Symbol</Table.Head><Table.Head class="text-right">Bid</Table.Head><Table.Head class="text-right">Ask</Table.Head><Table.Head></Table.Head></Table.Row></Table.Header>
            <Table.Body>
              {#each prices as p (p.symbol)}
                <Table.Row>
                  <Table.Cell class="pl-5 font-semibold">{p.symbol}</Table.Cell>
                  <Table.Cell class="num text-right">{formatMoney(p.bid)}</Table.Cell>
                  <Table.Cell class="num text-right">{formatMoney(p.ask)}</Table.Cell>
                  <Table.Cell class="pr-3 text-right"><Button variant="link" size="sm" href="#/trade?symbol={p.symbol}">Trade</Button></Table.Cell>
                </Table.Row>
              {/each}
            </Table.Body>
          </Table.Root>
        {/if}
      </Card.Root>
    </section>
  </div>

  <section class="mt-8">
    <div class="mb-3 flex items-center justify-between"><h2 class="text-xl font-bold">Recent activity</h2><a class="text-sm font-semibold text-primary" href="#/orders">All orders</a></div>
    <Card.Root class="shadow-none">
      <Card.Content>
        {#if live.recent.length === 0}
          <p class="py-6 text-center text-sm text-muted-foreground">Nothing yet.</p>
        {:else}
          <ul class="timeline">
            {#each live.recent.slice(0, 6) as item (item.id)}
              <li>
                <div>{#if orderLabel(item)}<span class="font-semibold">{orderLabel(item)}</span>&nbsp;·&nbsp;{/if}{item.message}</div>
                <div class="text-xs text-muted-foreground">
                  {when(item.created_at)}{#if item.order_id}{' · '}<a class="text-primary" href="#/orders/{item.order_id}">view order</a>{/if}
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </Card.Content>
    </Card.Root>
  </section>
{/if}

<FundDialog bind:open={funding} ondone={loadAccount} />
