<script>
  import * as Card from '$lib/components/ui/card/index.js'
  let { label, value, hint = '', valueClass = '', children } = $props()
</script>

<Card.Root class="gap-1 py-5 shadow-none">
  <Card.Content class="px-5">
    <div class="text-[13px] font-semibold text-muted-foreground">{label}</div>
    <div class="num mt-1 text-2xl font-bold {valueClass}">
      {#if children}{@render children()}{:else}{value}{/if}
    </div>
    {#if hint}<div class="mt-0.5 text-xs text-muted-foreground">{hint}</div>{/if}
  </Card.Content>
</Card.Root>
