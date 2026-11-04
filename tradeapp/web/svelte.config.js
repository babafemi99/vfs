import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

// shadcn-svelte components are written in TypeScript; this lets Vite compile them.
export default { preprocess: vitePreprocess() }
