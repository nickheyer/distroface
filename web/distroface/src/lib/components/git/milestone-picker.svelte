<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		Settings2,
		Check,
		Search,
		Loader2,
		Milestone as MilestoneIcon,
		CalendarDays
	} from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { IssueState, type Milestone } from '$lib/proto/distroface/v1/issue_pb';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { cn } from '$lib/utils';

	let {
		namespace,
		name,
		selected = $bindable(''),
		canEdit,
		onChange
	}: {
		namespace: string;
		name: string;
		selected: string;
		canEdit: boolean;
		onChange?: (id: string) => void;
	} = $props();

	let open = $state(false);
	let query = $state('');
	let milestones = $state<Milestone[]>([]);
	let loading = $state(false);
	let loaded = $state(false);

	async function load() {
		loading = true;
		try {
			const [openResp, closedResp] = await Promise.all([
				rpcClient.issue.listMilestones(
					{
						namespace,
						name,
						state: IssueState.OPEN,
						page: { pageSize: 100, pageToken: '', orderBy: '' }
					},
					silentCallOptions
				),
				rpcClient.issue.listMilestones(
					{
						namespace,
						name,
						state: IssueState.CLOSED,
						page: { pageSize: 100, pageToken: '', orderBy: '' }
					},
					silentCallOptions
				)
			]);
			milestones = [...openResp.milestones, ...closedResp.milestones];
			loaded = true;
		} catch {
			milestones = [];
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		if ((open || selected) && !loaded && !loading) load();
	});

	const filtered = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return milestones.filter((m) => !q || m.title.toLowerCase().includes(q));
	});
	const openOnes = $derived(filtered.filter((m) => m.state !== IssueState.CLOSED));
	const closedOnes = $derived(filtered.filter((m) => m.state === IssueState.CLOSED));
	const current = $derived(milestones.find((m) => m.id === selected));

	function progress(m: Milestone): number {
		const open = Number(m.openIssues);
		const closed = Number(m.closedIssues);
		const total = open + closed;
		return total === 0 ? 0 : Math.round((closed / total) * 100);
	}

	function due(m: Milestone): string {
		return m.dueDate ? timestampDate(m.dueDate).toLocaleDateString() : '';
	}

	function choose(id: string) {
		if (!canEdit) return;
		open = false;
		const next = id === selected ? '' : id;
		selected = next;
		onChange?.(next);
	}
</script>

{#snippet row(m: Milestone)}
	{@const active = m.id === selected}
	<button
		type="button"
		class={cn(
			'flex w-full items-start gap-2 px-2.5 py-1.5 text-left transition-colors hover:bg-muted',
			active && 'bg-muted/60'
		)}
		onclick={() => choose(m.id)}
	>
		<span class="mt-0.5 inline-flex size-4 shrink-0 items-center justify-center">
			{#if active}<Check class="size-3.5 text-primary" />{/if}
		</span>
		<span class="min-w-0 flex-1">
			<span class="block truncate text-sm font-medium">{m.title}</span>
			<span class="block text-xs text-muted-foreground">
				{progress(m)}% complete{#if due(m)}, due {due(m)}{/if}
			</span>
		</span>
	</button>
{/snippet}

<div class="space-y-2">
	<div class="flex min-h-6 items-center justify-between">
		<span class="eyebrow">Milestone</span>
		{#if canEdit}
			<Popover bind:open>
				<PopoverTrigger>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost-muted"
							size="icon-xs"
							title="Edit milestone"
							aria-label="Edit milestone"
						>
							<Settings2 />
						</Button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-72 p-0" align="end">
					<div class="flex items-center gap-2 border-b px-2.5 py-2">
						<Search class="size-3.5 shrink-0 text-muted-foreground" />
						<input
							class="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
							placeholder="Filter milestones"
							bind:value={query}
						/>
						{#if loading}
							<Loader2 class="size-3.5 animate-spin text-muted-foreground" />
						{/if}
					</div>
					<div class="max-h-72 overflow-y-auto py-1">
						{#if selected}
							<button
								type="button"
								class="w-full px-2.5 py-1.5 text-left text-sm text-muted-foreground transition-colors hover:bg-muted"
								onclick={() => choose(selected)}>Clear milestone</button
							>
						{/if}
						{#if !loading && filtered.length === 0}
							<p class="px-3 py-4 text-center text-sm text-muted-foreground">No milestones</p>
						{/if}
						{#each openOnes as m (m.id)}
							{@render row(m)}
						{/each}
						{#if closedOnes.length}
							<p class="eyebrow px-2.5 pt-2 pb-1">Closed</p>
							{#each closedOnes as m (m.id)}
								{@render row(m)}
							{/each}
						{/if}
					</div>
				</PopoverContent>
			</Popover>
		{/if}
	</div>
	{#if current}
		<a href={resolve('/[namespace]/[name]/milestones', { namespace, name })} class="group block">
			<span
				class="flex items-center gap-1.5 text-sm font-medium transition-colors group-hover:text-primary"
			>
				<MilestoneIcon class="size-3.5 text-muted-foreground" />
				<span class="truncate">{current.title}</span>
			</span>
			<span class="mt-1.5 block h-1.5 overflow-hidden rounded-full bg-muted">
				<span class="block h-full rounded-full bg-success" style="width: {progress(current)}%"
				></span>
			</span>
			{#if due(current)}
				<span class="mt-1 flex items-center gap-1 text-xs text-muted-foreground"
					><CalendarDays class="size-3" />Due {due(current)}</span
				>
			{/if}
		</a>
	{:else}
		<span class="text-sm text-muted-foreground">No milestone</span>
	{/if}
</div>
