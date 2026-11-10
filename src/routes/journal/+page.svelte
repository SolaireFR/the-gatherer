<script lang="ts">
	import { onMount } from 'svelte';
	import { Api, type FetchRun } from '$generated/api';
	import Alert from '@solairefr/gosvelte-front/ui/alert.svelte';
	import Button from '@solairefr/gosvelte-front/ui/button.svelte';
	import Modal from '@solairefr/gosvelte-front/ui/modal.svelte';
	import Text from '@solairefr/gosvelte-front/ui/text.svelte';
	import Table from '@solairefr/gosvelte-front/ui/table.svelte';
	import { ColumnType, type TableConfig } from '@solairefr/gosvelte-front/ui/table.js';
	import RequireAuth from '@solairefr/gosvelte-front/auth/require-auth.svelte';
	import { i18n, t } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';

	let runs = $state<FetchRun[]>([]);
	let loading = $state(false);
	let starting = $state(false);
	let error = $state('');
	// Gardé jusqu'à l'action suivante (vidé à son début : même message annoncé de nouveau)
	let success = $state('');
	let shownPrompt = $state<FetchRun | null>(null);

	// $derived : libellés retraduits au changement de langue
	const tableConfig = $derived<TableConfig<FetchRun>>({
		columns: [
			{ key: 'created_at', label: t('journal.date'), type: ColumnType.DateTime, mobileTitle: true },
			{ key: 'day', label: t('journal.day'), type: ColumnType.Date },
			{ key: 'model', label: t('journal.model') },
			{ key: 'saved', label: t('journal.saved'), type: ColumnType.Number },
			{ key: 'error', label: t('journal.status'), cell: status }
		],
		actions: [{ label: t('journal.prompt'), icon: 'eye', onclick: (run) => (shownPrompt = run) }],
		empty: t('journal.empty'),
		caption: t('journal.title'),
		pageSize: 20
	});

	function errorText(err: unknown): string {
		return err instanceof Error ? err.message : String(err);
	}

	async function load() {
		loading = true;
		error = '';
		try {
			runs = (await Api.ListFetchRuns()) ?? [];
		} catch (err) {
			error = t('journal.loadFailed', { error: errorText(err) });
			console.error('Error loading fetch runs:', err);
		} finally {
			loading = false;
		}
	}

	async function start() {
		starting = true;
		error = success = '';
		try {
			const started = await Api.StartFetch();
			success = t('journal.started', { day: new Date(`${started.day}T00:00`).toLocaleDateString(i18n.locale) });
		} catch (err) {
			error = t('journal.startFailed', { error: errorText(err) });
			console.error('Error starting fetch:', err);
		} finally {
			starting = false;
		}
	}

	onMount(load);
</script>

{#snippet status(run: FetchRun)}
	{#if run.error}
		<span class="failed">{t('journal.failed')}</span> <span class="error">{run.error}</span>
	{:else}
		<span class="ok">{t('journal.ok')}</span>
	{/if}
{/snippet}

<!-- Titre et description de la page : +page.ts (SEO, cf. App.PageData) -->

<RequireAuth role="admin">
	<div class="container">
		<header>
			<div>
				<h1>{t('journal.title')}</h1>
				<Text variant="muted">{t('journal.intro')}</Text>
			</div>
			<div class="actions">
				<Button icon="refresh" label={t('common.refresh')} variant="ghost" mobileIconOnly {loading} onclick={load} />
				<Button icon="sparkles" label={t('journal.start')} variant="primary" mobileIconOnly loading={starting} onclick={start} />
			</div>
		</header>

		<!-- Toujours montée : message annoncé de façon fiable par les lecteurs d'écran -->
		<Alert type="success" message={success} />
		{#if error}<Alert>{error}</Alert>{/if}

		<Table config={tableConfig} rows={runs} {loading} />
	</div>
</RequireAuth>

{#if shownPrompt}
	<Modal title={t('journal.promptTitle', { date: new Date(shownPrompt.created_at).toLocaleString(i18n.locale) })} wide onclose={() => (shownPrompt = null)}>
		<pre class="prompt">{shownPrompt.prompt}</pre>
	</Modal>
{/if}

<style>
	header {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}

	.actions {
		display: flex;
		gap: var(--space-2);
	}

	.ok {
		color: var(--success);
		font-weight: 600;
	}

	.failed {
		color: var(--danger);
		font-weight: 600;
	}

	.error {
		font-family: monospace;
		font-size: var(--font-size-xs);
		overflow-wrap: anywhere;
	}

	.prompt {
		margin: 0;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		font-size: var(--font-size-sm);
	}
</style>
