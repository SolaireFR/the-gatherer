<script lang="ts">
	import type { Snippet } from 'svelte';
	import AppLayout from '@solairefr/gosvelte-front/layout/app-layout.svelte';
	import { layout } from '$lib/layout';

	let { children }: { children: Snippet } = $props();
</script>

<!-- Thème, titre, topbar, menu et footer : gérés par AppLayout, configurés dans src/lib/layout.ts (layout() relue au changement de langue) -->
<AppLayout config={layout()}>
	{@render children()}
</AppLayout>
