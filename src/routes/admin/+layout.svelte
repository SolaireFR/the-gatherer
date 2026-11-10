<script lang="ts">
	import type { Snippet } from 'svelte';
	import AdminLayout from '@solairefr/gosvelte-front/admin/admin-layout.svelte';

	let { children }: { children: Snippet } = $props();
</script>

<!-- Mode administrateur (mot de passe redemandé, cf. ADMIN_SUDO_MINUTES), puis la page -->
<AdminLayout>
	{@render children()}
</AdminLayout>
