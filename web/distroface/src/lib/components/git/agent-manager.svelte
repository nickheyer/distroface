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
	import { Bot, Plus, Pencil, Trash2, FileCode2, AlertCircle } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { AgentSource, type Agent } from '$lib/proto/distroface/v1/agents_pb';
	import { kindLabels } from './agents-state';
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
		description = 'Personas with a connector, tools, and triggers. Each gets a bot account.',
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

	let agents = $state<Agent[]>([]);
	let loaded = $state(false);
	let loading = $state(false);
	let deleteOpen = $state(false);
	let deleteTarget = $state<Agent | null>(null);
	let deleting = $state(false);

	function editable(a: Agent): boolean {
		return !a.inherited && a.source !== AgentSource.FILE;
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.agents.listAgents({
				scope,
				includeInherited,
				page: { pageSize: 200, pageToken: '', orderBy: '' }
			});
			agents = resp.agents;
		} catch {
			agents = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.agents.deleteAgent({ scope, id: deleteTarget.id });
			toast.success('Agent removed');
			deleteOpen = false;
			await load();
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function triggerSummary(a: Agent): string[] {
		const t = a.triggers;
		if (!t) return [];
		const out: string[] = [];
		if (t.pullRequestOpened || t.pullRequestSynchronize || t.pullRequestReadyForReview)
			out.push('pull requests');
		if (t.reviewRequested) out.push('review requests');
		if (t.issueAssigned) out.push('assignment');
		if (t.issueLabels.length) out.push(`labels: ${t.issueLabels.join(', ')}`);
		if (t.mention) out.push('mentions');
		if (t.reviewFeedback) out.push('review feedback');
		if (t.workflowFailure) out.push('failed workflows');
		return out;
	}

	onMount(load);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Agents</h2>
			<p class="section-subtitle">{description}</p>
		</div>
		{#if canManage}
			<Button class="shrink-0" href={newHref}><Plus />New agent</Button>
		{/if}
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-lg" />
	{:else if agents.length === 0}
		<EmptyState
			icon={Bot}
			message="No agents"
			description={includeInherited
				? 'Define one here, at a higher scope, or under .distroface/agents in the repository.'
				: 'Define a reviewer, a coder, an assistant, or a triage agent.'}
		>
			{#snippet actions()}
				{#if canManage}
					<Button variant="outline" href={newHref}><Plus />New agent</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else if agents.length > 0}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Agent</TableHead>
						<TableHead class="w-28">Kind</TableHead>
						{#if includeInherited}
							<TableHead class="w-32">Scope</TableHead>
						{/if}
						<TableHead class="hidden md:table-cell">Triggers</TableHead>
						<TableHead class="hidden w-32 lg:table-cell">Sessions</TableHead>
						{#if canManage}
							<TableHead class="w-20"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each agents as a (a.id)}
						<TableRow>
							<TableCell>
								<p class="flex flex-wrap items-center gap-2 font-medium">
									{a.name}
									<span class="font-mono text-xs text-muted-foreground"
										>@{a.botUsername || a.slug}</span
									>
									{#if !a.enabled}<Badge variant="outline">Disabled</Badge>{/if}
									{#if a.source === AgentSource.FILE}
										<Badge variant="secondary" title={a.filePath}><FileCode2 />File</Badge>
									{/if}
									{#if a.error}
										<span title={a.error} class="inline-flex"
											><AlertCircle class="size-3.5 text-warning" /></span
										>
									{/if}
								</p>
								<p class="mt-0.5 truncate text-xs text-muted-foreground">
									{a.description || a.providerName || 'Default connector'}{a.model
										? `, ${a.model}`
										: ''}
								</p>
							</TableCell>
							<TableCell
								><Badge variant="secondary">{kindLabels[a.kind] ?? 'Agent'}</Badge></TableCell
							>
							{#if includeInherited}
								<TableCell>
									<Badge variant={a.inherited ? 'outline' : 'secondary'}
										>{scopeLabel(a.scope?.kind)}</Badge
									>
								</TableCell>
							{/if}
							<TableCell class="hidden text-xs text-muted-foreground md:table-cell">
								{triggerSummary(a).join(', ') || 'Manual only'}
							</TableCell>
							<TableCell class="hidden text-xs text-muted-foreground tabular-nums lg:table-cell">
								{a.sessionCount}{#if a.lastSessionAt}, last {relativeTime(
										timestampDate(a.lastSessionAt)
									)}{/if}
							</TableCell>
							{#if canManage}
								<TableCell class="text-right">
									{#if editable(a)}
										<div class="flex justify-end gap-1">
											<Button
												variant="ghost-muted"
												size="icon-sm"
												href={editHref(a.id)}
												aria-label="Edit {a.name}"
											>
												<Pencil />
											</Button>
											<Button
												variant="ghost-muted"
												size="icon-sm"
												class="hover:text-destructive"
												onclick={() => {
													deleteTarget = a;
													deleteOpen = true;
												}}
												aria-label="Remove {a.name}"
											>
												<Trash2 />
											</Button>
										</div>
									{:else}
										<span class="text-xs whitespace-nowrap text-muted-foreground">
											{a.source === AgentSource.FILE ? 'In repository' : 'Inherited'}
										</span>
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
	title="Remove agent"
	confirmLabel="Remove"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Remove <strong>{deleteTarget?.name}</strong>? Past sessions stay readable. Its bot account is
		deactivated once no other scope defines the handle.
	{/snippet}
</ConfirmDialog>
