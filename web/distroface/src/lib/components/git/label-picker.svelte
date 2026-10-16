<script lang="ts">
	import { Settings2, Check, Search, Loader2 } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import LabelChip from '$lib/components/git/label-chip.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { Label } from '$lib/proto/distroface/v1/issue_pb';
	import { cn } from '$lib/utils';

	let {
		namespace,
		name,
		selected = $bindable([]),
		canEdit,
		onChange
	}: {
		namespace: string;
		name: string;
		selected: string[];
		canEdit: boolean;
		onChange?: (ids: string[]) => void;
	} = $props();

	let open = $state(false);
	let query = $state('');
	let labels = $state<Label[]>([]);
	let loading = $state(false);
	let loaded = $state(false);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.issue.listLabels(
				{ namespace, name, page: { pageSize: 500, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
			labels = resp.labels;
			loaded = true;
		} catch {
			labels = [];
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		if (open && !loaded) load();
	});

	// Selected chips need names before the popover ever opens
	$effect(() => {
		if (selected.length && !loaded && !loading) load();
	});

	const filtered = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return labels.filter((l) => !q || l.name.toLowerCase().includes(q) || l.description.toLowerCase().includes(q));
	});

	const chosen = $derived(selected.map((id) => labels.find((l) => l.id === id)).filter((l): l is Label => !!l));

	function toggle(label: Label) {
		if (!canEdit) return;
		let next: string[];
		if (selected.includes(label.id)) {
			next = selected.filter((id) => id !== label.id);
		} else {
			next = selected.filter((id) => {
				const other = labels.find((l) => l.id === id);
				return !(label.exclusiveScope && other?.exclusiveScope === label.exclusiveScope);
			});
			next.push(label.id);
		}
		selected = next;
		onChange?.(next);
	}

	function clear() {
		selected = [];
		onChange?.([]);
	}
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Labels</span>
		{#if canEdit}
			<Popover bind:open>
				<PopoverTrigger>
					{#snippet child({ props })}
						<button {...props} type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" title="Edit labels" aria-label="Edit labels">
							<Settings2 class="h-3.5 w-3.5" />
						</button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-72 p-0" align="end">
					<div class="border-b border-border/50 px-2 py-2 flex items-center gap-2">
						<Search class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
						<input class="flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground" placeholder="Filter labels" bind:value={query} />
						{#if loading}
							<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />
						{/if}
					</div>
					<div class="max-h-72 overflow-y-auto py-1">
						{#if !loading && filtered.length === 0}
							<p class="px-3 py-4 text-[12.5px] text-muted-foreground text-center">No labels</p>
						{/if}
						{#each filtered as l (l.id)}
							{@const active = selected.includes(l.id)}
							<button
								type="button"
								class={cn('w-full flex items-start gap-2 px-2.5 py-1.5 text-left hover:bg-accent transition-colors', active && 'bg-accent/50')}
								onclick={() => toggle(l)}
							>
								<span class="h-4 w-4 shrink-0 inline-flex items-center justify-center mt-0.5">
									{#if active}<Check class="h-3.5 w-3.5 text-primary" />{/if}
								</span>
								<span class="min-w-0 flex-1">
									<span class="flex items-center gap-1.5">
										<LabelChip name={l.name} color={l.color} size="sm" />
										{#if l.isOrgLabel}<span class="text-[10px] text-muted-foreground">org</span>{/if}
									</span>
									{#if l.description}
										<span class="block text-[11.5px] text-muted-foreground truncate mt-0.5">{l.description}</span>
									{/if}
								</span>
							</button>
						{/each}
					</div>
					{#if selected.length}
						<div class="border-t border-border/50 px-2 py-1.5">
							<button type="button" class="text-[12px] text-muted-foreground hover:text-foreground" onclick={clear}>Clear labels</button>
						</div>
					{/if}
				</PopoverContent>
			</Popover>
		{/if}
	</div>
	<div class="flex flex-wrap gap-1.5">
		{#if chosen.length === 0}
			<span class="text-[12.5px] text-muted-foreground">None yet</span>
		{:else}
			{#each chosen as l (l.id)}
				<LabelChip name={l.name} color={l.color} description={l.description} size="sm" />
			{/each}
		{/if}
	</div>
</div>
