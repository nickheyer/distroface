<script lang="ts">
	import { resolve } from '$app/paths';
	import { Settings2, Check, Search, Loader2, Milestone as MilestoneIcon, CalendarDays } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
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
				rpcClient.issue.listMilestones({ namespace, name, state: IssueState.OPEN, page: { pageSize: 100, pageToken: '', orderBy: '' } }, silentCallOptions),
				rpcClient.issue.listMilestones({ namespace, name, state: IssueState.CLOSED, page: { pageSize: 100, pageToken: '', orderBy: '' } }, silentCallOptions)
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
		class={cn('w-full flex items-start gap-2 px-2.5 py-1.5 text-left hover:bg-accent transition-colors', active && 'bg-accent/50')}
		onclick={() => choose(m.id)}
	>
		<span class="h-4 w-4 shrink-0 inline-flex items-center justify-center mt-0.5">
			{#if active}<Check class="h-3.5 w-3.5 text-primary" />{/if}
		</span>
		<span class="min-w-0 flex-1">
			<span class="block text-[13px] font-medium truncate">{m.title}</span>
			<span class="block text-[11.5px] text-muted-foreground">
				{progress(m)}% complete{#if due(m)}, due {due(m)}{/if}
			</span>
		</span>
	</button>
{/snippet}

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Milestone</span>
		{#if canEdit}
			<Popover bind:open>
				<PopoverTrigger>
					{#snippet child({ props })}
						<button {...props} type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" title="Edit milestone" aria-label="Edit milestone">
							<Settings2 class="h-3.5 w-3.5" />
						</button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-72 p-0" align="end">
					<div class="border-b border-border/50 px-2 py-2 flex items-center gap-2">
						<Search class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
						<input class="flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground" placeholder="Filter milestones" bind:value={query} />
						{#if loading}
							<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />
						{/if}
					</div>
					<div class="max-h-72 overflow-y-auto py-1">
						{#if selected}
							<button type="button" class="w-full px-2.5 py-1.5 text-left text-[12.5px] text-muted-foreground hover:bg-accent" onclick={() => choose(selected)}>Clear milestone</button>
						{/if}
						{#if !loading && filtered.length === 0}
							<p class="px-3 py-4 text-[12.5px] text-muted-foreground text-center">No milestones</p>
						{/if}
						{#each openOnes as m (m.id)}
							{@render row(m)}
						{/each}
						{#if closedOnes.length}
							<p class="px-2.5 pt-2 pb-1 text-[10.5px] uppercase tracking-wider text-muted-foreground">Closed</p>
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
		<a href={resolve('/[namespace]/[name]/milestones', { namespace, name })} class="block group">
			<span class="flex items-center gap-1.5 text-[13px] font-medium group-hover:text-primary transition-colors">
				<MilestoneIcon class="h-3.5 w-3.5 text-muted-foreground" />
				<span class="truncate">{current.title}</span>
			</span>
			<span class="mt-1.5 block h-1.5 rounded-full bg-muted overflow-hidden">
				<span class="block h-full bg-success rounded-full" style="width: {progress(current)}%"></span>
			</span>
			{#if due(current)}
				<span class="mt-1 flex items-center gap-1 text-[11.5px] text-muted-foreground"><CalendarDays class="h-3 w-3" />Due {due(current)}</span>
			{/if}
		</a>
	{:else}
		<span class="text-[12.5px] text-muted-foreground">No milestone</span>
	{/if}
</div>
