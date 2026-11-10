// Administration : statut lu sur le serveur (hors admin : 404 de SvelteKit, comme une page inexistante), menu de /admin, noindex
import { loadAdmin } from '@solairefr/gosvelte-front/admin/admin.svelte.js';

export const load = loadAdmin;
