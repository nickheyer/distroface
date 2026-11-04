<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Plug, Plus, Pencil, Trash, PlugZap, Loader } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import type { AgentProvider } from '$lib/proto/distroface/v1/agents_pb';
	import { formatTokens, providerKinds } from './agents-state';
	import { scopeLabel } from './actions-state';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		scope,
		newHref,
		editHref,
		canManage = true,
		includeInherited = false,
		description = 'Inference endpoints agents speak through.',
		onChanged
	}: {
		scope: { kind: string; id: string };
		newHref: string;
		editHref: (id: string) => string;
		canManage?: boolean;
		includeInherited?: boolean;
		description?: string;
		onChanged?: () => void;
	} = $props();

	let providers = $state<AgentProvider[]>([]);
	let loaded = $state(false);
	let loading = $state(false);
	let testing = $state('');
	let deleteOpen = $state(false);
	let deleteTarget = $state<AgentProvider | null>(null);
	let deleting = $state(false);

	// Inherited rows are managed where they were registered
	function own(p: AgentProvider): boolean {
		return !p.inherited;
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.agents.listAgentProviders({
				scope,
				includeInherited,
				page: { pageSize: 200, pageToken: '', orderBy: '' }
			});
			providers = resp.providers;
		} catch {
			providers = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function test(p: AgentProvider) {
		testing = p.id;
		try {
			const resp = await rpcClient.agents.testAgentProvider({ scope, id: p.id });
			if (resp.ok) toast.success(resp.message);
			else toast.error(resp.message);
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			testing = '';
		}
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.agents.deleteAgentProvider({ scope, id: deleteTarget.id });
			toast.success('Connector removed');
			deleteOpen = false;
			await load();
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	onMount(load);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Connectors</h2>
			<p class="section-subtitle">{description}</p>
		</div>
		{#if canManage}
			<Button class="shrink-0" href={newHref}><Plus />New connector</Button>
		{/if}
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-lg" />
	{:else if providers.length === 0}
		<EmptyState
			icon={Plug}
			message="No connectors"
			description={includeInherited
				? 'Add one here or at a higher scope.'
				: 'Add an OpenAI compatible, Ollama, Anthropic, Azure, Gemini, or Bedrock endpoint.'}
		>
			{#snippet actions()}
				{#if canManage}
					<Button variant="outline" href={newHref}><Plus />New connector</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else if providers.length > 0}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Connector</TableHead>
						<TableHead class="hidden sm:table-cell">Model</TableHead>
						<TableHead class="w-24">Status</TableHead>
						{#if includeInherited}
							<TableHead class="w-32">Scope</TableHead>
						{/if}
						<TableHead class="hidden w-28 md:table-cell">Today</TableHead>
						<TableHead class="hidden w-36 md:table-cell">Last test</TableHead>
						{#if canManage}
							<TableHead class="w-28"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each providers as p (p.id)}
						<TableRow>
							<TableCell>
								<p class="flex items-center gap-2 font-medium">
									{p.name}
									{#if p.isDefault}<Badge variant="default">Default</Badge>{/if}
								</p>
								<p class="mt-0.5 truncate font-mono text-xs text-muted-foreground">
									{providerKinds[p.kind]?.label ?? 'Connector'}, {p.baseUrl}
								</p>
							</TableCell>
							<TableCell class="hidden font-mono text-xs sm:table-cell">
								{p.defaultModel || '-'}
								{#if p.models.length > 0}
									<span class="text-muted-foreground"> (+{p.models.length} allowed)</span>
								{/if}
							</TableCell>
							<TableCell>
								<Badge variant={p.enabled ? 'success' : 'outline'}
									>{p.enabled ? 'Enabled' : 'Disabled'}</Badge
								>
							</TableCell>
							{#if includeInherited}
								<TableCell>
									<Badge variant={own(p) ? 'secondary' : 'outline'}
										>{scopeLabel(p.scope?.kind)}</Badge
									>
								</TableCell>
							{/if}
							<TableCell class="hidden text-xs text-muted-foreground tabular-nums md:table-cell">
								{formatTokens(p.tokensToday)} tokens {#if p.tokensPerDay > 0n}/ {formatTokens(
										p.tokensPerDay
									)}{/if}
							</TableCell>
							<TableCell class="hidden text-xs text-muted-foreground md:table-cell">
								{#if p.lastTestedAt}
									<span class={p.lastTestError ? 'text-destructive' : ''} title={p.lastTestError}>
										{p.lastTestError ? 'Failed' : 'OK'}
										{relativeTime(timestampDate(p.lastTestedAt))}
									</span>
								{:else}
									Never
								{/if}
							</TableCell>
							{#if canManage}
								<TableCell class="text-right">
									{#if own(p)}
										<div class="flex justify-end gap-1">
											<Button
												variant="ghost-muted"
												size="icon-sm"
												onclick={() => test(p)}
												disabled={testing === p.id}
												aria-label="Test {p.name}"
												title="Test connection"
											>
												{#if testing === p.id}<Loader class="animate-spin" />{:else}<PlugZap />{/if}
											</Button>
											<Button
												variant="ghost-muted"
												size="icon-sm"
												href={editHref(p.id)}
												aria-label="Edit {p.name}"
											>
												<Pencil />
											</Button>
											<Button
												variant="ghost-muted"
												size="icon-sm"
												class="hover:text-destructive"
												onclick={() => {
													deleteTarget = p;
													deleteOpen = true;
												}}
												aria-label="Remove {p.name}"
											>
												<Trash />
											</Button>
										</div>
									{:else}
										<span
											class="text-xs whitespace-nowrap text-muted-foreground"
											title="Managed in the {scopeLabel(p.scope?.kind).toLowerCase()} settings"
											>Inherited</span
										>
									{/if}
								</TableCell>
							{/if}
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Remove connector"
	confirmLabel="Remove"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		Remove <strong>{deleteTarget?.name}</strong>? Agents bound to it fall back to the scope default
		connector.
	{/snippet}
</ConfirmDialog>
