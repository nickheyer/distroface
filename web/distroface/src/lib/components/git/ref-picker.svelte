<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitBranch, Tag, ChevronsUpDown, Check, Search, Loader2 } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { GitBranch as GitBranchMsg, GitTag } from '$lib/proto/distroface/v1/git_pb';
	import { cn } from '$lib/utils';

	type RefKind = 'branch' | 'tag';

	let {
		namespace,
		name,
		current,
		defaultBranch = '',
		onSelect,
		kinds = ['branch', 'tag'],
		label,
		class: className
	}: {
		namespace: string;
		name: string;
		current: string;
		defaultBranch?: string;
		onSelect: (ref: string, kind: RefKind) => void;
		kinds?: RefKind[];
		label?: string;
		class?: string;
	} = $props();

	let open = $state(false);
	let tabChoice = $state<RefKind | null>(null);
	const tab = $derived<RefKind>(tabChoice ?? kinds[0] ?? 'branch');
	let query = $state('');
	let branches = $state<GitBranchMsg[]>([]);
	let tags = $state<GitTag[]>([]);
	let loading = $state(false);
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const isSha = $derived(/^[0-9a-f]{7,64}$/i.test(current) && !branches.some((b) => b.name === current) && !tags.some((t) => t.name === current));
	const currentKind = $derived<RefKind | 'sha'>(
		tags.some((t) => t.name === current) && !branches.some((b) => b.name === current) ? 'tag' : isSha ? 'sha' : 'branch'
	);

	async function load() {
		const mine = ++seq;
		loading = true;
		const page = { pageSize: 50, pageToken: '', query: { text: query.trim(), filters: [] }, orderBy: '' };
		try {
			if (tab === 'branch') {
				const resp = await rpcClient.gitRefs.listBranches({ namespace, name, page }, silentCallOptions);
				if (mine === seq) branches = resp.branches;
			} else {
				const resp = await rpcClient.gitRefs.listGitTags({ namespace, name, page }, silentCallOptions);
				if (mine === seq) tags = resp.tags;
			}
		} catch {
			if (mine === seq) {
				if (tab === 'branch') branches = [];
				else tags = [];
			}
		} finally {
			if (mine === seq) loading = false;
		}
	}

	function onQuery() {
		clearTimeout(debounce);
		debounce = setTimeout(load, 200);
	}

	function switchTab(next: RefKind) {
		if (tab === next) return;
		tabChoice = next;
		load();
	}

	function choose(ref: string, kind: RefKind) {
		open = false;
		if (ref !== current) onSelect(ref, kind);
	}

	$effect(() => {
		if (open) load();
	});

	const items = $derived<{ name: string; kind: RefKind; hint: string }[]>(
		tab === 'branch'
			? branches.map((b) => ({ name: b.name, kind: 'branch', hint: b.isDefault ? 'default' : '' }))
			: tags.map((t) => ({ name: t.name, kind: 'tag', hint: t.hasRelease ? 'release' : '' }))
	);

	const sortedItems = $derived(
		[...items].sort((a, b) => {
			if (a.name === current) return -1;
			if (b.name === current) return 1;
			if (a.name === defaultBranch) return -1;
			if (b.name === defaultBranch) return 1;
			return 0;
		})
	);
</script>

<Popover bind:open>
	<PopoverTrigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" size="sm" class={cn('h-8 gap-1.5 max-w-[16rem] font-normal', className)}>
				{#if currentKind === 'tag'}
					<Tag class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
				{:else}
					<GitBranch class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
				{/if}
				{#if label}
					<span class="text-muted-foreground">{label}:</span>
				{/if}
				<span class="truncate font-medium font-mono text-[12.5px]">{currentKind === 'sha' ? current.slice(0, 10) : current}</span>
				<ChevronsUpDown class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
			</Button>
		{/snippet}
	</PopoverTrigger>
	<PopoverContent class="w-[min(20rem,calc(100vw-2rem))] p-0" align="start">
		<div class="border-b border-border/50 px-2 py-2 flex items-center gap-2">
			<Search class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
			<input
				class="flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground"
				placeholder="Find a {tab}..."
				bind:value={query}
				oninput={onQuery}
			/>
			{#if loading}
				<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />
			{/if}
		</div>
		{#if kinds.length > 1}
			<div class="flex border-b border-border/50">
				{#each kinds as k (k)}
					<button
						type="button"
						class="flex-1 px-3 py-1.5 text-[12.5px] font-medium border-b-2 -mb-px transition-colors {tab === k ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
						onclick={() => switchTab(k)}
					>
						{k === 'branch' ? 'Branches' : 'Tags'}
					</button>
				{/each}
			</div>
		{/if}
		<div class="max-h-72 overflow-y-auto py-1">
			{#if sortedItems.length === 0 && !loading}
				<p class="px-3 py-4 text-center text-[12.5px] text-muted-foreground">No {tab === 'branch' ? 'branches' : 'tags'} found</p>
			{/if}
			{#each sortedItems as item (item.name)}
				<button
					type="button"
					class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-muted/60 transition-colors"
					onclick={() => choose(item.name, item.kind)}
				>
					<Check class="h-3.5 w-3.5 shrink-0 {item.name === current ? 'opacity-100 text-primary' : 'opacity-0'}" />
					<span class="font-mono text-[12.5px] truncate flex-1">{item.name}</span>
					{#if item.hint}
						<span class="text-[10px] uppercase tracking-wide text-muted-foreground border border-border/60 rounded px-1 py-px">{item.hint}</span>
					{/if}
				</button>
			{/each}
		</div>
		{#if tab === 'branch'}
			<a href={resolve('/[namespace]/[name]/branches', { namespace, name })} class="block border-t border-border/50 px-3 py-2 text-[12px] text-primary hover:underline">View all branches</a>
		{:else}
			<a href={resolve('/[namespace]/[name]/tags', { namespace, name })} class="block border-t border-border/50 px-3 py-2 text-[12px] text-primary hover:underline">View all tags</a>
		{/if}
	</PopoverContent>
</Popover>
