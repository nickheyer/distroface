<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Search, X, CornerDownLeft } from '$lib/icons';
	import { MatchKind } from '$lib/proto/distroface/v1/pagination_pb';
	import type { QueryField, QueryFilter } from '$lib/query.svelte';
	import { cn } from '$lib/utils';

	let {
		filter,
		placeholder = 'Search...',
		onchange
	}: {
		filter: QueryFilter;
		placeholder?: string;
		onchange: () => void;
	} = $props();

	const matchOptions = [
		{ value: MatchKind.CONTAINS, label: 'contains' },
		{ value: MatchKind.EQUALS, label: 'is' },
		{ value: MatchKind.PREFIX, label: 'starts with' }
	];
	const matchLabel = (m: MatchKind) => matchOptions.find((o) => o.value === m)?.label ?? 'contains';

	let input = $state<HTMLInputElement | null>(null);
	let draft = $state('');
	let focused = $state(false);
	let highlighted = $state(-1);
	let debounce: ReturnType<typeof setTimeout> | undefined;

	// Resolves "Field: value" drafts against the field list
	const qualifier = $derived.by(() => {
		const idx = draft.indexOf(':');
		if (idx < 0) return null;
		const name = draft.slice(0, idx).trim().toLowerCase();
		const field = filter.fields.find(
			(f) => f.key.toLowerCase() === name || f.label.toLowerCase() === name
		);
		return field ? { field, value: draft.slice(idx + 1).trim() } : null;
	});

	const fieldMatches = $derived.by(() => {
		const q = draft.trim().toLowerCase();
		return filter.fields.filter(
			(f) => !q || f.label.toLowerCase().includes(q) || f.key.toLowerCase().includes(q)
		);
	});

	const rowCount = $derived(
		qualifier ? (qualifier.value ? matchOptions.length : 0) : fieldMatches.length
	);
	const open = $derived(focused && (rowCount > 0 || (qualifier !== null && !qualifier.value)));

	function textChanged() {
		highlighted = -1;
		const next = qualifier ? '' : draft.trim();
		if (filter.text === next) return;
		filter.text = next;
		clearTimeout(debounce);
		debounce = setTimeout(onchange, 500);
	}

	function beginQualifier(field: QueryField) {
		draft = `${field.label}: `;
		highlighted = -1;
		clearTimeout(debounce);
		if (filter.text) {
			filter.text = '';
			onchange();
		}
		input?.focus();
	}

	function commitFilter(match: MatchKind) {
		if (!qualifier || !filter.add(qualifier.field.key, match, qualifier.value)) return;
		draft = '';
		highlighted = -1;
		clearTimeout(debounce);
		onchange();
		input?.focus();
	}

	function removeFilter(i: number) {
		filter.remove(i);
		onchange();
	}

	function clearAll() {
		clearTimeout(debounce);
		draft = '';
		filter.reset();
		onchange();
		input?.focus();
	}

	function handleKey(e: KeyboardEvent) {
		if (e.key === 'Backspace' && draft === '' && filter.filters.length > 0) {
			removeFilter(filter.filters.length - 1);
			return;
		}
		if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && rowCount > 0) {
			e.preventDefault();
			highlighted =
				e.key === 'ArrowDown'
					? (highlighted + 1) % rowCount
					: (highlighted - 1 + rowCount) % rowCount;
			return;
		}
		if (e.key === 'Escape') {
			input?.blur();
			return;
		}
		if (e.key !== 'Enter') return;
		e.preventDefault();
		if (highlighted >= 0 && open) {
			if (qualifier) commitFilter(matchOptions[highlighted].value);
			else beginQualifier(fieldMatches[highlighted]);
		} else if (qualifier?.value) {
			commitFilter(MatchKind.CONTAINS);
		} else {
			clearTimeout(debounce);
			onchange();
		}
	}
</script>

<div class="relative">
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div
		class="flex min-h-8 w-full cursor-text flex-wrap items-center gap-1.5 rounded-md border border-input bg-field px-2 py-0.5 text-sm shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30 hover:border-border-strong dark:shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.2)]"
		onclick={() => input?.focus()}
	>
		<Search class="size-3.5 shrink-0 text-muted-foreground" />
		{#each filter.filters as f, i (i)}
			<Badge variant="secondary" class="gap-1 pr-1 font-normal">
				<span class="font-medium text-foreground">{filter.label(f.field)}</span>
				<span>{matchLabel(f.match)}</span>
				<span class="font-mono text-foreground">{f.value}</span>
				<button
					type="button"
					class="ml-0.5 rounded-full p-0.5 transition-colors hover:bg-foreground/10 hover:text-foreground"
					onclick={() => removeFilter(i)}
					aria-label="Remove filter"
				>
					<X class="size-3" />
				</button>
			</Badge>
		{/each}
		<input
			bind:this={input}
			bind:value={draft}
			class="h-6 min-w-24 flex-1 bg-transparent outline-none"
			placeholder={filter.filters.length > 0 ? '' : placeholder}
			autocomplete="off"
			spellcheck="false"
			oninput={textChanged}
			onfocus={() => (focused = true)}
			onblur={() => (focused = false)}
			onkeydown={handleKey}
		/>
		{#if filter.active || draft}
			<button
				type="button"
				class="shrink-0 rounded-sm p-0.5 text-muted-foreground transition-colors hover:text-foreground"
				onclick={clearAll}
				aria-label="Clear search and filters"
			>
				<X class="size-3.5" />
			</button>
		{/if}
	</div>
	{#if open}
		<div
			class="absolute top-full right-0 left-0 z-50 mt-1 rounded-lg border bg-popover p-1 text-popover-foreground shadow-lg dark:ring-1 dark:ring-white/5"
		>
			{#if qualifier}
				{#if !qualifier.value}
					<p class="px-2 py-1.5 text-xs text-muted-foreground">
						Type a value to filter by {qualifier.field.label}
					</p>
				{:else}
					{#each matchOptions as option, i (option.value)}
						<button
							type="button"
							class={cn(
								'flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
								highlighted === i && 'bg-muted'
							)}
							onmousedown={(e) => e.preventDefault()}
							onclick={() => commitFilter(option.value)}
						>
							<span class="font-medium">{qualifier.field.label}</span>
							<span class="text-muted-foreground">{option.label}</span>
							<span class="truncate font-mono">{qualifier.value}</span>
							{#if i === 0}
								<CornerDownLeft class="ml-auto size-3 shrink-0 text-muted-foreground" />
							{/if}
						</button>
					{/each}
				{/if}
			{:else}
				<p class="px-2 pt-1.5 pb-1 text-xs font-medium text-muted-foreground">Filter by</p>
				{#each fieldMatches as field, i (field.key)}
					<button
						type="button"
						class={cn(
							'flex w-full items-center rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
							highlighted === i && 'bg-muted'
						)}
						onmousedown={(e) => e.preventDefault()}
						onclick={() => beginQualifier(field)}
					>
						{field.label}<span class="text-muted-foreground">:</span>
					</button>
				{/each}
			{/if}
		</div>
	{/if}
</div>
