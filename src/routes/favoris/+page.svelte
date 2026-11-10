<script lang="ts">
	import { onMount } from 'svelte';
	import { Api, type News } from '$generated/api';
	import Alert from '@solairefr/gosvelte-front/ui/alert.svelte';
	import Text from '@solairefr/gosvelte-front/ui/text.svelte';
	import RequireAuth from '@solairefr/gosvelte-front/auth/require-auth.svelte';
	import { t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';
	import NewsCard from '$lib/news-card.svelte';

	let items = $state<News[]>([]);
	let loading = $state(true);
	let error = $state('');

	function errorText(err: unknown): string {
		return err instanceof Error ? err.message : String(err);
	}

	// Retirée des favoris : retirée de la liste
	async function toggle(news: News) {
		error = '';
		try {
			const updated = await Api.ToggleFavorite(news.id);
			items = updated.favorite ? items : items.filter((n) => n.id !== updated.id);
		} catch (err) {
			error = t('favorites.toggleFailed', { error: errorText(err) });
			console.error('Error toggling favorite:', err);
		}
	}

	onMount(async () => {
		try {
			items = (await Api.ListFavoriteNews()) ?? [];
		} catch (err) {
			error = t('news.loadFailed', { error: errorText(err) });
			console.error('Error loading favorites:', err);
		} finally {
			loading = false;
		}
	});
</script>

<!-- Titre et description de la page : +page.ts (SEO, cf. App.PageData) -->

<RequireAuth role="admin">
	<div class="container">
		<header>
			<h1>{t('favorites.title')}</h1>
			<Text variant="muted">{t('favorites.count', { count: items.length })}</Text>
		</header>

		{#if error}<Alert>{error}</Alert>{/if}

		<div class="list">
			{#each items as news (news.id)}
				<NewsCard {news} ontoggle={toggle} showDay />
			{:else}
				{#if !loading}<Text variant="muted">{t('favorites.empty')}</Text>{/if}
			{/each}
		</div>
	</div>
</RequireAuth>

<style>
	header {
		margin-bottom: var(--space-4);
	}

	.list {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
</style>
