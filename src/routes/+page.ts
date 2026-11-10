// SEO de la page (cf. App.PageData dans .gosvelte/gosvelte.d.ts), relu au changement de langue
import { t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';

export const load = () => ({ title: t('news.title'), description: t('news.description') });
