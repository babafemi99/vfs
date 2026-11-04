<script>
  import { Badge } from '$lib/components/ui/badge/index.js'
  import { statusLabel, statusTone } from '../lib/format.js'
  let { status } = $props()
</script>

<Badge class="tone-{statusTone(status)} border-transparent font-semibold">{statusLabel(status)}</Badge>
