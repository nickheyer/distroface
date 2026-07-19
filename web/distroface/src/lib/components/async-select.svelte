<script lang="ts">
	import { SvelteMap } from 'svelte/reactivity';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import { Check, ChevronsUpDown, X, Loader2, Search } from '@lucide/svelte';
	import { cn } from '$lib/utils';

	type Option = { value: string; label: string; description?: string };

	let {
		fetchPage,
		selected = $bindable(),
		initialSelected = [],
		multiple = false,
		placeholder = 'Select...',
		searchPlaceholder = 'Search...',
		disabled = false,
		triggerClass = ''
	}: {
		fetchPage: (query: string, pageToken: string) => Promise<{ items: Option[]; nextPageToken: string }>;
		selected: string[] | string;
		// Seeds labels for values chosen before any page loads (edit forms)
		initialSelected?: Option[];
		multiple?: boolean;
		placeholder?: string;
		searchPlaceholder?: string;
		disabled?: boolean;
		triggerClass?: string;
	} = $props();

	let open = $state(false);
	let query = $state('');
	let options = $state<Option[]>([]);
	let nextToken = $state('');
	let loading = $state(false);
	let loaded = $state(false);
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let sentinel = $state<HTMLDivElement | null>(null);

	const selectedValues = $derived(Array.isArray(selected) ? selected : selected ? [selected] : []);

	// Values are ids, chips render remembered labels
	const labels = new SvelteMap<string, string>();
	$effect(() => {
		for (const o of initialSelected) labels.set(o.value, o.label);
	});
	const labelOf = (v: string) => labels.get(v) ?? v;

	function remember(items: Option[]) {
		for (const o of items) labels.set(o.value, o.label);
	}

	function appendUnique(items: Option[]) {
		const seen = new Set(options.map((o) => o.value));
		options = [...options, ...items.filter((o) => !seen.has(o.value))];
	}

	async function loadFirst() {
		loading = true;
		try {
			const resp = await fetchPage(query, '');
			options = resp.items;
			remember(resp.items);
			nextToken = resp.nextPageToken;
			loaded = true;
		} catch {
			options = [];
			nextToken = '';
		} finally {
			loading = false;
		}
	}

	async function loadMore() {
		if (!nextToken || loading) return;
		loading = true;
		try {
			const resp = await fetchPage(query, nextToken);
			appendUnique(resp.items);
			remember(resp.items);
			nextToken = resp.nextPageToken;
		} catch {
			nextToken = '';
		} finally {
			loading = false;
		}
	}

	function onSearch() {
		clearTimeout(debounce);
		debounce = setTimeout(loadFirst, 250);
	}

	function choose(v: string) {
		if (multiple) {
			selected = selectedValues.includes(v)
				? selectedValues.filter((x) => x !== v)
				: [...selectedValues, v];
		} else {
			selected = v;
			open = false;
		}
	}

	function removeChip(v: string, e: Event) {
		e.stopPropagation();
		if (multiple) selected = selectedValues.filter((x) => x !== v);
		else selected = '';
	}

	function onOpenChange(next: boolean) {
		open = next;
		if (next && !loaded) loadFirst();
	}

	// Fetch the next page when the list bottom scrolls into view
	$effect(() => {
		if (!sentinel) return;
		const io = new IntersectionObserver((entries) => {
			if (entries[0].isIntersecting) loadMore();
		});
		io.observe(sentinel);
		return () => io.disconnect();
	});
</script>

<Popover bind:open {onOpenChange}>
	<PopoverTrigger {disabled}>
		{#snippet child({ props })}
			<button
				{...props}
				type="button"
				{disabled}
				class={cn(
					'flex min-h-9 w-full items-center justify-between gap-2 rounded-md border border-input bg-transparent px-3 py-1.5 text-sm shadow-xs transition-colors hover:bg-muted/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
					triggerClass
				)}
			>
				<span class="flex flex-1 flex-wrap items-center gap-1 text-left">
					{#if selectedValues.length === 0}
						<span class="text-muted-foreground">{placeholder}</span>
					{:else if multiple}
						{#each selectedValues as v (v)}
							<Badge variant="secondary" class="gap-1 pr-1">
								{labelOf(v)}
								<button
									type="button"
									class="rounded-sm hover:bg-muted-foreground/20"
									onclick={(e) => removeChip(v, e)}
									aria-label={`Remove ${labelOf(v)}`}
								>
									<X class="h-3 w-3" />
								</button>
							</Badge>
						{/each}
					{:else}
						<span>{labelOf(selectedValues[0])}</span>
					{/if}
				</span>
				<ChevronsUpDown class="h-4 w-4 shrink-0 text-muted-foreground" />
			</button>
		{/snippet}
	</PopoverTrigger>
	<PopoverContent class="w-(--bits-popover-anchor-width) p-0" align="start">
		<div class="flex items-center border-b px-3">
			<Search class="h-4 w-4 shrink-0 text-muted-foreground" />
			<Input
				bind:value={query}
				oninput={onSearch}
				placeholder={searchPlaceholder}
				class="h-9 border-0 shadow-none focus-visible:ring-0"
				autocomplete="off"
			/>
		</div>
		<div class="max-h-64 overflow-y-auto p-1">
			{#if options.length === 0 && !loading}
				<p class="px-3 py-6 text-center text-sm text-muted-foreground">No results</p>
			{:else}
				{#each options as opt (opt.value)}
					{@const active = selectedValues.includes(opt.value)}
					<button
						type="button"
						class={cn(
							'flex w-full items-start gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
							active && 'bg-primary/5'
						)}
						onclick={() => choose(opt.value)}
					>
						<Check class={cn('mt-0.5 h-4 w-4 shrink-0', active ? 'opacity-100 text-primary' : 'opacity-0')} />
						<span class="min-w-0 space-y-0.5">
							<span class="block truncate font-medium leading-none">{opt.label}</span>
							{#if opt.description}
								<span class="block truncate text-xs text-muted-foreground">{opt.description}</span>
							{/if}
						</span>
					</button>
				{/each}
				<div bind:this={sentinel} class="h-1"></div>
			{/if}
			{#if loading}
				<div class="flex items-center justify-center py-3">
					<Loader2 class="h-4 w-4 animate-spin text-muted-foreground" />
				</div>
			{/if}
		</div>
	</PopoverContent>
</Popover>
