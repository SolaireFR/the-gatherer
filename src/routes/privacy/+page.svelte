<script lang="ts">
	import PrivacyPolicy from '@solairefr/gosvelte-front/legal/privacy-policy.svelte';
	import { legal } from '$lib/legal';
</script>

<!-- Sections du moteur et du back (auth.RegisterPersonalData) générées d'après les réglages réels ; traitements du projet
     non rattachés à un compte (formulaire de contact…) : <h3>…</h3><p>données, finalité, base légale, conservation</p> ici -->
<PrivacyPolicy {legal} />
