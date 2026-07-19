<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Search, X, CornerDownLeft } from '@lucide/svelte';
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
	const matchLabel = (m: MatchKind) =>
		matchOptions.find((o) => o.value === m)?.label ?? 'contains';

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
		class="flex min-h-9 w-full cursor-text flex-wrap items-center gap-1.5 rounded-md border border-input bg-transparent px-2.5 py-1 text-sm shadow-xs transition-colors focus-within:ring-2 focus-within:ring-ring"
		onclick={() => input?.focus()}
	>
		<Search class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
		{#each filter.filters as f, i (i)}
			<Badge variant="secondary" class="gap-1 pr-1 font-normal">
				<span class="font-medium">{filter.label(f.field)}</span>
				<span class="text-muted-foreground">{matchLabel(f.match)}</span>
				<span class="font-mono">{f.value}</span>
				<button
					type="button"
					class="ml-0.5 rounded-sm p-0.5 hover:bg-muted"
					onclick={() => removeFilter(i)}
					aria-label="Remove filter"
				>
					<X class="h-3 w-3" />
				</button>
			</Badge>
		{/each}
		<input
			bind:this={input}
			bind:value={draft}
			class="h-6 min-w-24 flex-1 bg-transparent outline-none placeholder:text-muted-foreground"
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
				class="shrink-0 rounded-sm p-0.5 text-muted-foreground hover:text-foreground"
				onclick={clearAll}
				aria-label="Clear search and filters"
			>
				<X class="h-3.5 w-3.5" />
			</button>
		{/if}
	</div>

	{#if open}
		<div
			class="absolute left-0 right-0 top-full z-50 mt-1 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
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
								'flex w-full items-center gap-1.5 rounded-sm px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
								highlighted === i && 'bg-muted'
							)}
							onmousedown={(e) => e.preventDefault()}
							onclick={() => commitFilter(option.value)}
						>
							<span class="font-medium">{qualifier.field.label}</span>
							<span class="text-muted-foreground">{option.label}</span>
							<span class="truncate font-mono">{qualifier.value}</span>
							{#if i === 0}
								<CornerDownLeft class="ml-auto h-3 w-3 shrink-0 text-muted-foreground" />
							{/if}
						</button>
					{/each}
				{/if}
			{:else}
				<p class="px-2 pb-1 pt-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
					Filter by
				</p>
				{#each fieldMatches as field, i (field.key)}
					<button
						type="button"
						class={cn(
							'flex w-full items-center rounded-sm px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
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
