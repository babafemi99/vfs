<script>
  let { eyebrow, title, description = '', actions } = $props()
</script>

<div class="mb-6 flex flex-wrap items-end justify-between gap-4">
  <div>
    <p class="eyebrow mb-1.5">{eyebrow}</p>
    <h1 class="text-3xl leading-tight font-bold">{title}</h1>
    {#if description}<p class="mt-1.5 text-muted-foreground">{description}</p>{/if}
  </div>
  {#if actions}<div class="flex flex-wrap gap-2">{@render actions()}</div>{/if}
</div>
