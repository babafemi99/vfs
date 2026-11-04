<script>
  let { eyebrow, title, description = '', actions } = $props()
</script>

<div class="mb-6 flex flex-wrap items-end justify-between gap-x-4 gap-y-3">
  <div>
    <p class="eyebrow mb-1.5">{eyebrow}</p>
    <h1 class="text-2xl leading-tight font-bold sm:text-3xl">{title}</h1>
    {#if description}<p class="mt-1.5 text-muted-foreground">{description}</p>{/if}
  </div>
  {#if actions}<div class="flex flex-wrap gap-2">{@render actions()}</div>{/if}
</div>
