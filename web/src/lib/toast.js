// Toasts go through Sonner. tone: 'success' | 'error' | 'warning' | 'info'.
import { toast as sonner } from 'svelte-sonner'

export function toast(title, description = '', tone = 'info') {
  const opts = description ? { description } : {}
  switch (tone) {
    case 'success': return sonner.success(title, opts)
    case 'error': return sonner.error(title, opts)
    case 'warning': return sonner.warning(title, opts)
    default: return sonner.info(title, opts)
  }
}
