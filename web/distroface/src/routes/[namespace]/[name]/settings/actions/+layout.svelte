<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormPage from '$lib/components/form-page.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Info } from '@lucide/svelte';

	let { children } = $props();

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const params = $derived({ namespace, name });

	const tabs = $derived([
		{ href: resolve('/[namespace]/[name]/settings/actions', params), label: 'Settings' },
		{ href: resolve('/[namespace]/[name]/settings/actions/runners', params), label: 'Runners' }
	]);
</script>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<FormPage
		level="section"
		title="Actions"
		description="Workflow defaults and runners for this repository."
		{tabs}
	>
		{#if !configStore.actionsEnabled}
			<Alert>
				<Info />
				<AlertDescription
					>Workflows are turned off for this instance. Settings saved here apply once an
					administrator enables Actions.</AlertDescription
				>
			</Alert>
		{:else if !repo.units?.actions}
			<Alert>
				<Info />
				<AlertDescription
					>The Actions unit is disabled for this repository. Turn it on under General, Features.</AlertDescription
				>
			</Alert>
		{/if}

		{#key repo.id}
			{@render children?.()}
		{/key}
	</FormPage>
{/if}
