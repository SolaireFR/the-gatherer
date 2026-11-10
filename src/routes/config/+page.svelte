<script lang="ts">
	import { onMount } from 'svelte';
	import { Api, type Topic } from '$generated/api';
	import Alert from '@solairefr/gosvelte-front/ui/alert.svelte';
	import Button from '@solairefr/gosvelte-front/ui/button.svelte';
	import Text from '@solairefr/gosvelte-front/ui/text.svelte';
	import RequireAuth from '@solairefr/gosvelte-front/auth/require-auth.svelte';
	import { t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';

	// Listes de la page (Topic.kind côté Go)
	const KINDS = ['tech', 'exclude'] as const;
	type Kind = (typeof KINDS)[number];

	let topics = $state<Topic[]>([]);
	let labels = $state<Record<Kind, string>>({ tech: '', exclude: '' });
	let saving = $state<Kind | ''>('');
	let error = $state('');
	// Gardé jusqu'à l'action suivante (vidé à son début : même message annoncé de nouveau)
	let success = $state('');

	function errorText(err: unknown): string {
		return err instanceof Error ? err.message : String(err);
	}

	async function load() {
		try {
			topics = (await Api.ListTopics()) ?? [];
		} catch (err) {
			error = t('topics.loadFailed', { error: errorText(err) });
			console.error('Error loading topics:', err);
		}
	}

	async function add(e: SubmitEvent, kind: Kind) {
		e.preventDefault();
		saving = kind;
		error = success = '';
		try {
			const topic = await Api.CreateTopic({ label: labels[kind], kind });
			topics = [...topics, topic];
			labels[kind] = '';
			success = t('topics.added', { label: topic.label });
		} catch (err) {
			error = t('topics.saveFailed', { error: errorText(err) });
			console.error('Error adding topic:', err);
		} finally {
			saving = '';
		}
	}

	async function remove(topic: Topic) {
		error = success = '';
		try {
			await Api.DeleteTopic(topic.id);
			topics = topics.filter((tp) => tp.id !== topic.id);
			success = t('topics.deleted', { label: topic.label });
		} catch (err) {
			error = t('topics.deleteFailed', { error: errorText(err) });
			console.error('Error deleting topic:', err);
		}
	}

	onMount(load);
</script>

<!-- Titre et description de la page : +page.ts (SEO, cf. App.PageData) -->

<RequireAuth role="admin">
	<div class="container">
		<header>
			<h1>{t('topics.title')}</h1>
			<Text variant="muted">{t('topics.intro')}</Text>
		</header>

		<!-- Toujours montée : message annoncé de façon fiable par les lecteurs d'écran -->
		<Alert type="success" message={success} />
		{#if error}<Alert>{error}</Alert>{/if}

		<div class="kinds">
			{#each KINDS as kind (kind)}
				<section>
					<h2>{t(`topics.${kind}.title`)}</h2>
					<Text variant="muted">{t(`topics.${kind}.hint`)}</Text>
					<ul>
						{#each topics.filter((tp) => tp.kind === kind) as topic (topic.id)}
							<li class={kind}>
								<span>{topic.label}</span>
								<Button icon="close" variant="ghost" size="sm" title={t('topics.deleteName', { label: topic.label })} onclick={() => remove(topic)} />
							</li>
						{:else}
							<li class="empty">{t(`topics.${kind}.empty`)}</li>
						{/each}
					</ul>
					<form onsubmit={(e) => add(e, kind)}>
						<label class="sr-only" for="label-{kind}">{t('topics.label')}</label>
						<input id="label-{kind}" bind:value={labels[kind]} placeholder={t(`topics.${kind}.placeholder`)} required maxlength="100" />
						<Button submit icon="plus" label={t('topics.add')} variant="primary" loading={saving === kind} />
					</form>
				</section>
			{/each}
		</div>
	</div>
</RequireAuth>

<style>
	header {
		margin-bottom: var(--space-4);
	}

	.kinds {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
		gap: var(--space-5);
	}

	h2 {
		margin: 0 0 var(--space-1);
		font-size: var(--font-size-lg);
	}

	ul {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: var(--space-3) 0;
		padding: 0;
		list-style: none;
	}

	li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding-left: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--surface);
	}

	li.tech {
		border-color: var(--success);
	}

	li.exclude {
		border-color: var(--danger);
	}

	li.empty {
		padding: 0;
		border: none;
		background: none;
		color: var(--muted);
	}

	form {
		display: flex;
		gap: var(--space-2);
	}

	input {
		flex: 1;
		min-width: 0;
	}
</style>
