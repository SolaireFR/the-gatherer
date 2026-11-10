// Configuration du layout (@solairefr/gosvelte-front/layout/app-layout.svelte) : tout se règle ici, sans toucher à gosvelte-front.
// Retirer une clé = retirer l'élément ; menu: false / footer: false = brique désactivée (ex : site d'une page).
// Éléments libres (start, end, bottom, footer.items) : { type: 'button' | 'link' | 'text' | 'icon' } ou custom(Composant, props).
// Textes : t('clé') (src/lib/i18n/<langue>.ts) ; fonction relue à chaque changement de langue.
import type { LayoutConfig } from '@solairefr/gosvelte-front/layout/layout.js';
import { t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';
import { PUBLIC_PROJECT_NAME } from '$generated/env';

export function layout(): LayoutConfig {
	return {
		topbar: {
			title: PUBLIC_PROJECT_NAME,
			logo: '/logo.svg',
			auth: { mobileIconOnly: true },
			// Choix de la langue (masqué s'il n'y a qu'une langue)
			locale: true
		},
		menu: {
			changelog: true,
			items: [
				{ label: t('news.title'), href: '/', icon: 'list' },
				{ label: t('favorites.title'), href: '/favoris', icon: 'star' },
				{ label: t('topics.title'), href: '/config', icon: 'settings' },
				{ label: t('journal.title'), href: '/journal', icon: 'clock' },
				{
					label: t('nav.admin'),
					icon: 'shield',
					// auth : mode 'login' = visible, connexion puis retour ; { role: 'admin' } = réservé
					items: [
						{ label: t('auth.account'), href: '/profile', icon: 'user', auth: { mode: 'login' } },
						// Lien masqué hors rôle admin ; la protection est côté serveur (/admin = 404 pour les autres)
						{ label: t('admin.title'), href: '/admin', icon: 'shield', auth: { role: 'admin' } }
					]
				}
			]
		},
		footer: {
			text: `© ${new Date().getFullYear()} ${PUBLIC_PROJECT_NAME}`,
			// Pages légales (RGPD, mentions légales) : à garder accessibles depuis toutes les pages
			items: [
				{ type: 'link', label: t('privacy.title'), href: '/privacy' },
				{ type: 'link', label: t('legal.title'), href: '/legal' }
			]
		}
	};
}
