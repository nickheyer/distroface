<script lang="ts">
	import { goto } from '$app/navigation';
	import { Plug } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import AgentProviderForm from '$lib/components/git/agent-provider-form.svelte';
	import type { AgentProvider } from '$lib/proto/distroface/v1/agents_pb';

	// Create when id is blank, edit otherwise
	let {
		scope,
		id = '',
		listHref,
		canManage = true
	}: {
		scope: { kind: string; id: string };
		id?: string;
		listHref: string;
		canManage?: boolean;
	} = $props();

	let provider = $state<AgentProvider | null>(null);
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
		if (!id) {
			provider = null;
			loaded = true;
			return;
		}
		const resp = await rpcClient.agents
			.getAgentProvider({ scope, id }, silentCallOptions)
			.catch(() => null);
		provider = resp?.provider && !resp.provider.inherited ? resp.provider : null;
		loaded = true;
	}

	// Callers pass a resolved href
	function back() {
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(listHref);
	}
</script>

{#if !canManage}
	<EmptyState icon={Plug} message="You cannot manage connectors here">
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to connectors</Button>
		{/snippet}
	</EmptyState>
{:else if !loaded}
	<Skeleton class="h-96 w-full rounded-lg" />
{:else if id && !provider}
	<EmptyState
		icon={Plug}
		message="Connector not found"
		description="It may have been removed or belongs to another scope."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to connectors</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title={provider ? 'Edit connector' : 'New connector'}
		description={provider
			? 'Changes apply to the next request.'
			: 'An inference endpoint agents speak through.'}
		backHref={listHref}
		backLabel="Connectors"
	>
		{#key provider?.id ?? ''}
			<AgentProviderForm
				{scope}
				provider={provider ?? undefined}
				cancelHref={listHref}
				onSaved={back}
			/>
		{/key}
	</FormPage>
{/if}
