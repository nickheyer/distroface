<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { setContext } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormPage from '$lib/components/form-page.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import {
		RepoAgentsContext,
		REPO_AGENTS_CONTEXT_KEY
	} from '$lib/components/git/repo-agents-context.svelte';
	import { Info } from '@lucide/svelte';

	let { children } = $props();

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const params = $derived({ namespace, name });

	const ctx = new RepoAgentsContext();
	setContext(REPO_AGENTS_CONTEXT_KEY, ctx);

	let loadedFor = $state('');
	$effect(() => {
		if (repo && repo.id !== loadedFor) {
			loadedFor = repo.id;
			void ctx.load(repo);
		}
	});

	// Create and edit pages bring their own header
	const nested = $derived(/\/agents\/(definitions|connectors)\/[^/]+\/?$/.test(page.url.pathname));

	const tabs = $derived([
		{ href: resolve('/[namespace]/[name]/settings/agents', params), label: 'Settings' },
		{ href: resolve('/[namespace]/[name]/settings/agents/definitions', params), label: 'Agents' },
		{
			href: resolve('/[namespace]/[name]/settings/agents/connectors', params),
			label: 'Connectors'
		},
		{ href: resolve('/[namespace]/[name]/settings/agents/usage', params), label: 'Usage' }
	]);
</script>

{#if !repo || !ctx.parentScope}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if nested}
	{#key repo.id}
		{@render children?.()}
	{/key}
{:else}
	<FormPage
		level="section"
		title="Agents"
		description="Limits, agents, and connectors for this repository. Organization and instance definitions apply here too."
		{tabs}
	>
		{#if !configStore.agentsEnabled}
			<Alert>
				<Info />
				<AlertDescription
					>Agents are turned off for this instance. Settings saved here apply once an administrator
					enables them.</AlertDescription
				>
			</Alert>
		{:else if ctx.overview && !ctx.overview.enabled}
			<Alert>
				<Info />
				<AlertDescription
					>Agents are turned off for this organization by its owners.</AlertDescription
				>
			</Alert>
		{:else if !repo.units?.agents}
			<Alert>
				<Info />
				<AlertDescription
					>The Agents unit is disabled for this repository. Turn it on under General, Features to
					show the Agents tab.</AlertDescription
				>
			</Alert>
		{/if}

		{#key repo.id}
			{@render children?.()}
		{/key}
	</FormPage>
{/if}
