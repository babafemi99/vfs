<script>
  import { tick } from 'svelte'
  import * as Card from '$lib/components/ui/card/index.js'
  import * as Sheet from '$lib/components/ui/sheet/index.js'
  import * as Tabs from '$lib/components/ui/tabs/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import { Separator } from '$lib/components/ui/separator/index.js'
  import PageHeader from '../components/PageHeader.svelte'
  import Stat from '../components/Stat.svelte'
  import { api } from '../lib/api.js'
  import { toast } from '../lib/toast.js'

  const MAX_LINES = 1000

  // How each outcome reads in the terminal, and its colour.
  const OUTCOME = {
    accepted: ['✓ ACCEPTED', 't-accepted', 'tone-completed'],
    duplicate: ['= DUPLICATE', 't-duplicate', 'tone-processing'],
    rejected_signature: ['✗ BAD SIGNATURE', 't-refused', 'tone-failed'],
    rejected_timestamp: ['✗ STALE', 't-refused', 'tone-failed'],
    rejected_nonce: ['✗ NO NONCE', 't-refused', 'tone-failed'],
    rejected_replay: ['✗ REPLAY', 't-refused', 'tone-failed'],
    invalid_body: ['✗ BAD BODY', 't-refused', 'tone-failed'],
    error: ['! ERROR', 't-error', 'tone-pending'],
  }
  const label = (o) => OUTCOME[o]?.[0] ?? o
  const termClass = (o) => OUTCOME[o]?.[1] ?? ''
  const tone = (o) => OUTCOME[o]?.[2] ?? 'tone-cancelled'
  const refused = (o) => o.startsWith('rejected_') || o === 'invalid_body'

  let lines = $state([])
  let filter = $state('all')
  let paused = $state(false)
  let connected = $state(false)
  let selected = $state(null) // full detail of the inspected request
  let inspecting = $state(false)
  let scroller

  const shown = $derived(
    lines.filter((l) => filter === 'all' || (filter === 'accepted' ? !refused(l.outcome) : refused(l.outcome))),
  )
  const counts = $derived({
    total: lines.length,
    accepted: lines.filter((l) => l.outcome === 'accepted').length,
    duplicate: lines.filter((l) => l.outcome === 'duplicate').length,
    refused: lines.filter((l) => refused(l.outcome)).length,
  })

  async function scrollToEnd() {
    await tick()
    if (scroller && !paused) scroller.scrollTop = scroller.scrollHeight
  }

  // Load the recent log, then follow the live stream from the last id.
  $effect(() => {
    let source
    let cancelled = false
    api.webhooks(300).then((r) => {
      if (cancelled) return
      lines = r.webhooks
      scrollToEnd()
      const last = lines.length ? lines[lines.length - 1].id : 0
      source = new EventSource(`/api/admin/webhooks/stream?after=${last}`)
      source.onopen = () => (connected = true)
      source.onerror = () => (connected = false)
      source.addEventListener('webhook', (msg) => {
        lines.push(JSON.parse(msg.data))
        if (lines.length > MAX_LINES) lines.splice(0, lines.length - MAX_LINES)
        scrollToEnd()
      })
    })
    return () => {
      cancelled = true
      source?.close()
    }
  })

  async function inspect(line) {
    inspecting = true
    try {
      selected = await api.webhook(line.id)
    } catch (err) {
      toast('Could not load the webhook', err.message, 'error')
      inspecting = false
    }
  }

  const time = (iso) => {
    const d = new Date(iso)
    return d.toLocaleTimeString('en-GB', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
  }

  function describe(l) {
    const parts = [l.event_type ?? '(unknown type)']
    if (l.event_id) parts.push(l.event_id)
    if (l.provider_order_id) parts.push('→ ' + l.provider_order_id)
    if (l.detail && l.outcome !== 'accepted') parts.push('· ' + l.detail)
    return parts.join('  ')
  }

  // The three checks every webhook must pass, as shown in the inspector.
  function checks(w) {
    const o = w.outcome
    const sigOK = !['rejected_signature', 'rejected_timestamp', 'rejected_nonce'].includes(o)
    const tsOK = o !== 'rejected_timestamp'
    const nonceOK = !['rejected_nonce', 'rejected_replay'].includes(o)
    const ts = Number(w.timestamp)
    const skew = ts ? Math.round(new Date(w.received_at).getTime() / 1000 - ts) : null
    return [
      ['Timestamp', tsOK, ts ? `signed ${new Date(ts * 1000).toLocaleTimeString('en-GB')} · ${skew}s before receipt (tolerance ±300s)` : 'missing'],
      ['Nonce', nonceOK, o === 'rejected_replay' ? 'already used: this exact request was received before' : w.nonce ? 'present and never seen before' : 'missing'],
      ['Signature', sigOK && tsOK && o !== 'rejected_nonce', o === 'rejected_signature' ? 'HMAC-SHA512 mismatch' : sigOK ? 'HMAC-SHA512 over timestamp.nonce.body matches' : 'not checked'],
    ]
  }

  function pretty(body) {
    try {
      return JSON.stringify(JSON.parse(body), null, 2)
    } catch {
      return body
    }
  }

  async function copyPayload() {
    await navigator.clipboard?.writeText(selected.body ?? '')
    toast('Payload copied')
  }
</script>

<PageHeader eyebrow="Webhooks" title="Webhook inspector" description="Every request the broker sends: verified, checked for replays, logged. Click a line to inspect it.">
  {#snippet actions()}
    <Button variant="outline" onclick={() => { paused = !paused; if (!paused) scrollToEnd() }}>{paused ? '▶ Resume' : '❚❚ Pause'}</Button>
    <Button variant="outline" onclick={() => (lines = [])}>Clear view</Button>
  {/snippet}
</PageHeader>

<div class="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-4">
  <Stat label="Received" value={counts.total} hint="In this view" />
  <Stat label="Accepted" value={counts.accepted} valueClass="text-gain" hint="New events, queued" />
  <Stat label="Duplicates" value={counts.duplicate} hint="Retries of known events" />
  <Stat label="Refused" value={counts.refused} valueClass={counts.refused ? 'text-loss' : ''} hint="Signature, timestamp, nonce, replay" />
</div>

<div class="mt-6 mb-3 flex items-center justify-between">
  <Tabs.Root bind:value={filter}>
    <Tabs.List>
      <Tabs.Trigger value="all">All</Tabs.Trigger>
      <Tabs.Trigger value="accepted">Accepted</Tabs.Trigger>
      <Tabs.Trigger value="refused">Refused</Tabs.Trigger>
    </Tabs.List>
  </Tabs.Root>
  <span class="flex items-center gap-1.5 text-xs text-muted-foreground"><span class="live-dot" class:on={connected && !paused}></span>{paused ? 'Paused' : connected ? 'Streaming' : 'Connecting…'}</span>
</div>

<div class="terminal">
  <div class="terminal-bar">
    <span class="dots"><span style="background:#f87171"></span><span style="background:#fbbf24"></span><span style="background:#34d399"></span></span>
    brokersim → POST /webhooks/provider
  </div>
  <div class="h-[460px] overflow-y-auto py-2" bind:this={scroller}>
    {#if shown.length === 0}
      <div class="px-4 py-2 text-slate-500">$ waiting for webhooks… place an order, then fill it from the provider console.</div>
    {/if}
    {#each shown as l (l.id)}
      <div class="term-line" class:selected={selected?.id === l.id} role="button" tabindex="0"
        onclick={() => inspect(l)} onkeydown={(e) => e.key === 'Enter' && inspect(l)}>
        <span class="t">{time(l.received_at)}</span>
        <span class={termClass(l.outcome)}>{label(l.outcome)}</span>
        <span class="what">{describe(l)}{#if !l.verified}<span class="dim"> · unverified</span>{/if}</span>
      </div>
    {/each}
  </div>
</div>

<Sheet.Root bind:open={inspecting} onOpenChange={(open) => !open && (selected = null)}>
  <Sheet.Content side="right" class="overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
    {#if selected}
      <Sheet.Header>
        <div class="flex items-center gap-2">
          <Badge class="border-transparent {tone(selected.outcome)}">{selected.outcome.replaceAll('_', ' ')}</Badge>
          {#if !selected.verified}<Badge variant="outline" class="text-loss">unverified</Badge>{/if}
        </div>
        <Sheet.Title class="text-xl">{selected.event_type ?? 'Unknown event'}</Sheet.Title>
        <Sheet.Description class="num">{selected.event_id ?? 'no event id'} · received {new Date(selected.received_at).toLocaleString('en-GB')}</Sheet.Description>
      </Sheet.Header>

      <div class="grid min-w-0 gap-5 px-4 pb-6">
        <Card.Root class="gap-0 py-0 shadow-none">
          <div class="border-b px-4 py-2.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase">Security checks</div>
          {#each checks(selected) as [name, ok, note]}
            <div class="flex items-start gap-3 border-b px-4 py-3 text-sm last:border-b-0">
              <span class="mt-0.5 font-bold {ok ? 'text-gain' : 'text-loss'}">{ok ? '✓' : '✗'}</span>
              <div><div class="font-semibold">{name}</div><div class="text-muted-foreground">{note}</div></div>
            </div>
          {/each}
        </Card.Root>

        <Card.Root class="gap-0 py-0 shadow-none">
          <div class="border-b px-4 py-2.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase">Processing</div>
          <div class="px-4 py-3 text-sm">
            {#if selected.outcome === 'accepted'}
              {#if selected.process_error}
                Processed with a note: <strong>{selected.process_error.replaceAll('_', ' ')}</strong>
              {:else if selected.processed_at}
                Applied to the order at {new Date(selected.processed_at).toLocaleTimeString('en-GB')}.
              {:else}
                Stored in the inbox, waiting for the worker.
              {/if}
              {#if selected.order_id}<div class="mt-2"><Button variant="link" class="h-auto p-0" href="#/orders/{selected.order_id}" onclick={() => (inspecting = false)}>Open the order →</Button></div>{/if}
            {:else if selected.outcome === 'duplicate'}
              Already received under this event id: acknowledged with 200 and ignored. Nothing applied twice.
            {:else}
              Refused before anything was stored. {selected.detail ?? ''}
            {/if}
          </div>
        </Card.Root>

        <Card.Root class="gap-0 py-0 shadow-none">
          <div class="border-b px-4 py-2.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase">Request</div>
          {#each [
            ['X-Sim-Timestamp', selected.timestamp ?? '—'],
            ['X-Sim-Nonce', selected.nonce ?? '—'],
            ['X-Sim-Signature', selected.signature_prefix ? selected.signature_prefix + '…' : '—'],
            ['Provider order', selected.provider_order_id ?? '—'],
            ['From', selected.remote_addr ?? '—'],
            ['Body size', `${selected.body_size} bytes`],
          ] as [k, v]}
            <div class="flex justify-between gap-4 border-b px-4 py-2.5 text-sm last:border-b-0"><span class="text-muted-foreground">{k}</span><span class="num text-right font-medium break-all">{v}</span></div>
          {/each}
        </Card.Root>

        <div>
          <div class="mb-2 flex items-center justify-between">
            <span class="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Payload {selected.verified ? '' : '(unverified: do not trust)'}</span>
            <Button variant="outline" size="sm" onclick={copyPayload}>Copy</Button>
          </div>
          <pre class="json max-h-[420px] max-w-full">{pretty(selected.body ?? '')}</pre>
        </div>
        <Separator />
        <p class="text-xs text-muted-foreground">Only the first characters of the signature are kept. Bodies of refused requests are stored for debugging but never processed.</p>
      </div>
    {/if}
  </Sheet.Content>
</Sheet.Root>
