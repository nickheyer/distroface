<script lang="ts">
	import { goto } from '$app/navigation';
	import { Bot } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import AgentForm from '$lib/components/git/agent-form.svelte';
	import type { Agent, AgentProvider } from '$lib/proto/distroface/v1/agents_pb';

	// Create when id is blank, edit otherwise
	let {
		scope,
		id = '',
		listHref,
		canManage = true,
		orgId = '',
		allowCommands = true
	}: {
		scope: { kind: string; id: string };
		id?: string;
		listHref: string;
		canManage?: boolean;
		orgId?: string;
		allowCommands?: boolean;
	} = $props();

	let agent = $state<Agent | null>(null);
	let providers = $state<AgentProvider[]>([]);
	let loaded = $state(false);
	let loadedKey = $state('');

	$effect(() => {
		const key = `${scope.kind}/${scope.id}/${id}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void load();
		}
	});

	async function load() {
		loaded = false;
		const page = { pageSize: 200, pageToken: '', orderBy: '' };
		const [p, a] = await Promise.all([
			rpcClient.agents
				.listAgentProviders({ scope, includeInherited: true, page }, silentCallOptions)
				.catch(() => null),
			id ? rpcClient.agents.getAgent({ scope, id }, silentCallOptions).catch(() => null) : null
		]);
		providers = p?.providers ?? [];
		agent = a?.agent && !a.agent.inherited ? a.agent : null;
		loaded = true;
	}

	// Callers pass a resolved href
	function back() {
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(listHref);
	}
</script>

{#if !canManage}
	<EmptyState icon={Bot} message="You cannot manage agents here">
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to agents</Button>
		{/snippet}
	</EmptyState>
{:else if !loaded}
	<Skeleton class="h-96 w-full rounded-lg" />
{:else if id && !agent}
	<EmptyState
		icon={Bot}
		message="Agent not found"
		description="It may have been removed or belongs to another scope."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to agents</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title={agent ? 'Edit agent' : 'New agent'}
		description={agent
			? 'Changes apply to the next session.'
			: 'A persona with a connector, tools, and triggers.'}
		backHref={listHref}
		backLabel="Agents"
	>
		{#key agent?.id ?? ''}
			<AgentForm
				{scope}
				agent={agent ?? undefined}
				{providers}
				{orgId}
				{allowCommands}
				cancelHref={listHref}
				onSaved={back}
			/>
		{/key}
	</FormPage>
{/if}
