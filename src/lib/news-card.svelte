<script lang="ts">
	import type { News } from '$generated/api';
	import Button from '@solairefr/gosvelte-front/ui/button.svelte';
	import { i18n, t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';

	interface Props {
		news: News;
		/** Clic sur l'étoile : le parent appelle l'API et remplace la news */
		ontoggle: (news: News) => void;
		/** Affiche le jour de la news (liste qui mélange plusieurs jours : favoris) */
		showDay?: boolean;
	}

	let { news, ontoggle, showDay = false }: Props = $props();

	const categories = $derived(news.categories.split(',').filter(Boolean));
	const sectionLabel = $derived(t(`news.sections.${news.section}` as 'news.sections.tech'));
	const day = $derived(new Date(`${news.day}T00:00`).toLocaleDateString(i18n.locale));
</script>

<article class="news">
	<header>
		<h2><a href={news.link} target="_blank" rel="noopener noreferrer">{news.title}</a></h2>
		<span class="fav" class:on={news.favorite}>
			<Button
				icon={news.favorite ? '★' : '☆'}
				variant="ghost"
				title={t(news.favorite ? 'favorites.remove' : 'favorites.add', { title: news.title })}
				onclick={() => ontoggle(news)}
			/>
		</span>
	</header>
	<p class="meta">
		<span class="tag section">{sectionLabel}</span>
		<span>{news.source}</span>
		{#if showDay}<span>{day}</span>{/if}
		{#each categories as category (category)}<span class="tag">{category}</span>{/each}
	</p>
	{#if news.description}<p class="description">{news.description}</p>{/if}
</article>

<style>
	.news {
		background: var(--bg);
		border: 1px solid var(--border);
		border-left: 3px solid var(--info);
		border-radius: var(--radius);
		padding: var(--space-3) var(--space-4);
	}

	header {
		display: flex;
		align-items: flex-start;
		gap: var(--space-2);
	}

	h2 {
		flex: 1;
		min-width: 0;
		margin: 0;
		font-size: var(--font-size-lg);
		line-height: 1.3;
		overflow-wrap: anywhere;
	}

	.fav {
		flex: none;
		font-size: var(--font-size-lg);
	}

	.fav.on :global(.btn) {
		color: var(--warning);
	}

	h2 a {
		color: var(--info);
		text-decoration: none;
	}

	h2 a:hover {
		text-decoration: underline;
	}

	.meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-2);
		margin: var(--space-1) 0 0;
		color: var(--muted);
		font-size: var(--font-size-xs);
	}

	.tag {
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		padding: 0 var(--space-2);
	}

	.section {
		color: var(--text);
		font-weight: 600;
	}

	.description {
		margin: var(--space-2) 0 0;
		line-height: 1.45;
	}
</style>
