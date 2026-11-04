<script>
  import * as Dialog from '$lib/components/ui/dialog/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import { Input } from '$lib/components/ui/input/index.js'
  import { Label } from '$lib/components/ui/label/index.js'
  import { api, newKey } from '../lib/api.js'
  import { formatMoney, parseDollars } from '../lib/money.js'
  import { toast } from '../lib/toast.js'

  let { open = $bindable(false), ondone } = $props()

  // One idempotency key each time the dialog opens: a double click or a
  // retried request funds once.
  let key = $state(newKey())
  let text = $state('1,000')
  let busy = $state(false)
  let error = $state('')
  const cents = $derived(parseDollars(text))

  $effect(() => {
    if (open) {
      key = newKey()
      error = ''
    }
  })

  async function submit(e) {
    e.preventDefault()
    if (!cents) { error = 'Enter an amount like 1,000 or 250.50'; return }
    busy = true
    error = ''
    try {
      const res = await api.fund(cents, key)
      toast('Account funded', `${formatMoney(res.amount)} added. Available ${formatMoney(res.cash.available)}.`, 'success')
      ondone?.()
      open = false
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-md">
    <form onsubmit={submit} class="grid gap-5">
      <Dialog.Header>
        <p class="eyebrow">Funding</p>
        <Dialog.Title class="text-xl">Add cash</Dialog.Title>
        <Dialog.Description>Demo funding: moves money from the system funding account into your available cash.</Dialog.Description>
      </Dialog.Header>
      <div class="grid gap-2">
        <Label for="amount">Amount (USD)</Label>
        <Input id="amount" class="num h-11" bind:value={text} inputmode="decimal" autocomplete="off" />
        <div class="flex gap-2">
          {#each ['1,000', '10,000', '50,000'] as pick}
            <Button type="button" variant="outline" size="sm" onclick={() => (text = pick)}>${pick}</Button>
          {/each}
        </div>
      </div>
      {#if error}<p class="text-sm text-destructive">{error}</p>{/if}
      <Dialog.Footer>
        <Button type="button" variant="outline" onclick={() => (open = false)}>Cancel</Button>
        <Button type="submit" disabled={busy}>{busy ? 'Adding…' : `Add ${cents ? formatMoney(cents) : ''}`}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
