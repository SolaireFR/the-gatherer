<script lang="ts">
	import { onMount } from 'svelte';
	import { Api, type News } from '$generated/api';
	import Alert from '@solairefr/gosvelte-front/ui/alert.svelte';
	import DayPicker from '@solairefr/gosvelte-front/ui/day-picker.svelte';
	import Text from '@solairefr/gosvelte-front/ui/text.svelte';
	import RequireAuth from '@solairefr/gosvelte-front/auth/require-auth.svelte';
	import { t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';
	import NewsCard from '$lib/news-card.svelte';

	const SECTIONS = ['generales', 'tech', 'alternatives'] as const;

	let days = $state<string[]>([]);
	let day = $state('');
	let items = $state<News[]>([]);
	let section = $state('');
	let category = $state('');
	let loading = $state(true);
	let error = $state('');

	const categories = $derived([...new Set(items.flatMap((n) => n.categories.split(',').filter(Boolean)))].sort());
	const shown = $derived(
		items.filter((n) => (!section || n.section === section) && (!category || n.categories.split(',').includes(category)))
	);

	function errorText(err: unknown): string {
		return err instanceof Error ? err.message : String(err);
	}

	async function loadDay(selected: string) {
		loading = true;
		error = '';
		try {
			items = (await Api.ListNews({ day: selected })) ?? [];
			category = '';
		} catch (err) {
			error = t('news.loadFailed', { error: errorText(err) });
			console.error('Error loading news:', err);
		} finally {
			loading = false;
		}
	}

	async function toggle(news: News) {
		error = '';
		try {
			const updated = await Api.ToggleFavorite(news.id);
			items = items.map((n) => (n.id === updated.id ? updated : n));
		} catch (err) {
			error = t('favorites.toggleFailed', { error: errorText(err) });
			console.error('Error toggling favorite:', err);
		}
	}

	onMount(async () => {
		try {
			days = (await Api.ListNewsDays()) ?? [];
		} catch (err) {
			error = t('news.loadFailed', { error: errorText(err) });
			console.error('Error loading news days:', err);
		}
		if (days.length === 0) {
			loading = false;
			return;
		}
		day = days[0];
		await loadDay(day);
	});
</script>

<!-- Titre et description de la page : +page.ts (SEO, cf. App.PageData) -->

<RequireAuth role="admin">
	<div class="container">
		<header>
			<h1>{t('news.title')}</h1>
			<Text variant="muted">{t('news.count', { count: shown.length })}</Text>
		</header>

		{#if error}<Alert>{error}</Alert>{/if}

		{#if days.length}
			<div class="filters">
				<DayPicker {days} bind:value={day} onchange={loadDay} {loading} />
				<div class="field">
					<label for="section">{t('news.section')}</label>
					<select id="section" bind:value={section}>
						<option value="">{t('news.allSections')}</option>
						{#each SECTIONS as s (s)}<option value={s}>{t(`news.sections.${s}`)}</option>{/each}
					</select>
				</div>
				<div class="field">
					<label for="category">{t('news.category')}</label>
					<select id="category" bind:value={category}>
						<option value="">{t('news.allCategories')}</option>
						{#each categories as c (c)}<option value={c}>{c}</option>{/each}
					</select>
				</div>
			</div>

			<div class="list">
				{#each shown as news (news.id)}
					<NewsCard {news} ontoggle={toggle} />
				{:else}
					{#if !loading}<Text variant="muted">{t('news.empty')}</Text>{/if}
				{/each}
			</div>
		{:else if !loading}
			<p>{t('news.noDays')} <a href="/journal">{t('journal.title')}</a>.</p>
		{/if}
	</div>
</RequireAuth>

<style>
	header {
		margin-bottom: var(--space-4);
	}

	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		gap: var(--space-3);
		margin-bottom: var(--space-4);
	}

	.filters .field {
		margin: 0;
	}

	.list {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
</style>
