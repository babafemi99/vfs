<script>
  import ExternalLink from '@lucide/svelte/icons/external-link'
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Table from '$lib/components/ui/table/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import PageHeader from '../components/PageHeader.svelte'
  import Stat from '../components/Stat.svelte'
  import { api } from '../lib/api.js'
  import { live } from '../lib/live.svelte.js'
  import { when, ago, shortId } from '../lib/format.js'
  import { toast } from '../lib/toast.js'

  let { providerConsole } = $props()

  let stats = $state(null)
  let dead = $state([])
  let invariants = $state(null)
  let breaks = $state([])
  let recon = $state(null)
  let reconRunning = $state(false)

  const ALERTS = {
    dead_letter_queue_not_empty: 'Jobs gave up and are waiting in the dead-letter queue.',
    queue_lag: 'A job has been waiting longer than the alert threshold.',
    provider_circuit_open: 'Calls to the broker are paused after repeated failures.',
  }
  const circuitTone = (c) => (c === 'closed' ? 'tone-completed' : c === 'open' ? 'tone-failed' : 'tone-pending')

  async function load() {
    const [s, d, i, b] = await Promise.allSettled([api.jobStats(), api.jobs('dead'), api.invariants(), api.breaks()])
    if (s.status === 'fulfilled') stats = s.value
    if (d.status === 'fulfilled') dead = d.value.jobs
    if (i.status === 'fulfilled') invariants = i.value
    if (b.status === 'fulfilled') breaks = b.value.breaks
  }

  $effect(() => {
    live.version
    load()
  })
  $effect(() => {
    api.reconLatest().then((r) => (recon = r)).catch(() => {})
    const timer = setInterval(load, 3000)
    return () => clearInterval(timer)
  })

  async function retry(job) {
    try {
      await api.retryJob(job.id)
      toast('Job requeued', `Job ${job.id} has a fresh retry budget.`, 'success')
      load()
    } catch (err) {
      toast('Retry failed', err.message, 'error')
    }
  }

  async function retryAll() {
    try {
      const r = await api.retryAllDead()
      toast('Dead letters replayed', `${r.retried} job(s) requeued.`, 'success')
      load()
    } catch (err) {
      toast('Retry failed', err.message, 'error')
    }
  }

  async function runRecon() {
    reconRunning = true
    try {
      recon = await api.reconRun()
      toast(recon.ok ? 'Books reconciled' : 'Reconciliation found differences', `${recon.orders_checked} order(s) checked, ${recon.mismatches.length} mismatch(es).`, recon.ok ? 'success' : 'warning')
    } catch (err) {
      toast('Reconciliation failed', err.message, 'error')
    } finally {
      reconRunning = false
    }
  }
</script>

<PageHeader eyebrow="Operations" title="System health" description="Job queue, dead letters, reconciliation and the books' invariants.">
  {#snippet actions()}<Button variant="outline" href={providerConsole} target="_blank" rel="noopener">Open provider console <ExternalLink class="size-3.5" /></Button>{/snippet}
</PageHeader>

{#if stats}
  {#if stats.ok}
    <p class="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800">All clear: queue healthy, no dead letters, broker reachable.</p>
  {:else}
    <div class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800">
      {#each stats.alerts as a}<div><strong>{a.replaceAll('_', ' ')}</strong>: {ALERTS[a] ?? ''}</div>{/each}
    </div>
  {/if}

  <div class="mt-4 grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-4">
    <Stat label="Pending jobs" value={stats.pending} hint="Waiting or retrying" />
    <Stat label="Running" value={stats.running} hint="Leased to a worker" />
    <Stat label="Dead letters" value={stats.dead} valueClass={stats.dead ? 'text-loss' : ''} hint="Need a human" />
    <Stat label="Oldest waiting" value={ago(stats.oldest_waiting_seconds)} hint="Queue lag" />
    <Stat label="Broker circuit" hint="Opens after repeated failures">
      <Badge class="border-transparent {circuitTone(stats.provider_circuit)}">{stats.provider_circuit.replace('_', '-')}</Badge>
    </Stat>
  </div>
{/if}

<section class="mt-8">
  <div class="mb-3 flex items-center justify-between">
    <h2 class="text-xl font-bold">Dead-letter queue</h2>
    <Button size="sm" onclick={retryAll} disabled={dead.length === 0}>Retry all</Button>
  </div>
  <Card.Root class="py-0 shadow-none">
    {#if dead.length === 0}
      <p class="py-8 text-center text-sm text-muted-foreground">Empty. Every job has either finished or is still retrying within its budget.</p>
    {:else}
      <Table.Root>
        <Table.Header><Table.Row><Table.Head class="pl-5">Job</Table.Head><Table.Head>Kind</Table.Head><Table.Head>Order</Table.Head><Table.Head class="text-right">Attempts</Table.Head><Table.Head>Last error</Table.Head><Table.Head>Died</Table.Head><Table.Head></Table.Head></Table.Row></Table.Header>
        <Table.Body>
          {#each dead as job (job.id)}
            <Table.Row>
              <Table.Cell class="num pl-5">#{job.id}</Table.Cell>
              <Table.Cell>{job.kind}</Table.Cell>
              <Table.Cell class="num">{#if job.order_id}<a class="text-primary" href="#/orders/{job.order_id}">{shortId(job.order_id)}</a>{:else}—{/if}</Table.Cell>
              <Table.Cell class="num text-right">{job.attempts}</Table.Cell>
              <Table.Cell class="max-w-72 truncate text-xs" title={job.last_error}>{job.last_error ?? '—'}</Table.Cell>
              <Table.Cell class="text-xs">{when(job.updated_at)}</Table.Cell>
              <Table.Cell class="pr-5 text-right"><Button variant="outline" size="sm" onclick={() => retry(job)}>Retry</Button></Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    {/if}
  </Card.Root>
</section>

<div class="mt-8 grid gap-6 lg:grid-cols-2">
  <section>
    <div class="mb-3 flex items-center justify-between">
      <h2 class="text-xl font-bold">Reconciliation</h2>
      <Button size="sm" onclick={runRecon} disabled={reconRunning}>{reconRunning ? 'Running…' : 'Run now'}</Button>
    </div>
    <Card.Root class="gap-0 py-0 shadow-none">
      {#if !recon}
        <p class="py-8 text-center text-sm text-muted-foreground">No run yet. It runs on a schedule, or press Run now.</p>
      {:else}
        <div class="flex items-center gap-2.5 border-b px-5 py-3.5">
          <Badge class="border-transparent {recon.ok ? 'tone-completed' : 'tone-failed'}">{recon.ok ? 'Books agree with the broker' : 'Differences found'}</Badge>
          <span class="text-xs text-muted-foreground">{when(recon.finished_at)}</span>
        </div>
        {#each [['Orders checked', recon.orders_checked], ['Skipped (still settling)', recon.orders_skipped], ['Mismatches', recon.mismatches.length], ['Invariants', recon.invariants_ok ? 'All hold' : 'Broken']] as [k, v]}
          <div class="flex justify-between border-b px-5 py-3 text-sm last:border-b-0"><span class="text-muted-foreground">{k}</span><span class="num font-semibold">{v}</span></div>
        {/each}
        {#if recon.mismatches.length}
          <div class="m-4 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900">
            {#each recon.mismatches.slice(0, 8) as m}
              <div><strong>{m.kind.replaceAll('_', ' ')}</strong> {#if m.order_id}<a class="text-primary" href="#/orders/{m.order_id}">{shortId(m.order_id)}</a>{/if}: ours {m.local || '—'}, broker {m.provider || '—'}</div>
            {/each}
          </div>
        {/if}
      {/if}
    </Card.Root>
  </section>

  <section>
    <h2 class="mb-3 text-xl font-bold">Books</h2>
    <Card.Root class="gap-0 py-0 shadow-none">
      {#if invariants}
        {#each invariants.invariants as inv}
          <div class="flex items-center justify-between border-b px-5 py-2.5 text-sm last:border-b-0">
            <span class="text-muted-foreground">{inv.name.replaceAll('_', ' ')}</span>
            <Badge class="border-transparent {inv.ok ? 'tone-completed' : 'tone-failed'}">{inv.ok ? 'OK' : 'Broken'}</Badge>
          </div>
        {/each}
      {/if}
    </Card.Root>
    <div class="mt-6 mb-3 flex items-center justify-between"><h2 class="text-xl font-bold">Breaks</h2><span class="text-xs text-muted-foreground">{breaks.length} recorded</span></div>
    <Card.Root class="shadow-none">
      <Card.Content class="text-sm">
        {#if breaks.length === 0}
          <p class="text-center text-muted-foreground">No contradictions recorded.</p>
        {:else}
          {#each breaks.slice(0, 6) as b (b.id)}<div class="mb-2"><strong>{b.kind.replaceAll('_', ' ')}</strong>: {b.detail}</div>{/each}
        {/if}
      </Card.Content>
    </Card.Root>
  </section>
</div>

<a href={providerConsole} target="_blank" rel="noopener" class="mt-8 block hover:no-underline">
  <Card.Root class="shadow-none transition-colors hover:bg-muted">
    <Card.Content class="flex items-center justify-between">
      <div>
        <h3 class="font-semibold">Provider console</h3>
        <p class="text-sm text-muted-foreground">Brokersim's own screen: drive fills, cancels and the market, and see every webhook delivery. Opens in a new tab.</p>
      </div>
      <ExternalLink class="size-5 shrink-0 text-muted-foreground" />
    </Card.Content>
  </Card.Root>
</a>
