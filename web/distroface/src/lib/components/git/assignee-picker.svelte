<script lang="ts">
	import { resolve } from '$app/paths';
	import { SvelteMap } from 'svelte/reactivity';
	import { Settings2, Check, Search, Loader2 } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { cn } from '$lib/utils';

	let {
		namespace,
		name,
		selected = $bindable([]),
		canEdit,
		onChange,
		known = []
	}: {
		namespace: string;
		name: string;
		selected: string[];
		canEdit: boolean;
		onChange?: (ids: string[]) => void;
		// Usernames for ids chosen before any search ran
		known?: { id: string; username: string }[];
	} = $props();

	type Option = { id: string; username: string; displayName: string };

	let open = $state(false);
	let query = $state('');
	let options = $state<Option[]>([]);
	let loading = $state(false);
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const names = new SvelteMap<string, string>();
	$effect(() => {
		for (const k of known) names.set(k.id, k.username);
		if (authStore.user) names.set(authStore.user.id, authStore.user.username);
	});

	async function search() {
		const mine = ++seq;
		loading = true;
		const text = query.trim();
		try {
			const found: Option[] = [];
			// Collaborators lead the list until a query narrows it
			if (!text) {
				try {
					const collab = await rpcClient.git.listCollaborators(
						{ namespace, name, page: { pageSize: 50, pageToken: '', orderBy: '' } },
						silentCallOptions
					);
					for (const c of collab.collaborators) {
						if (c.accepted) found.push({ id: c.userId, username: c.username, displayName: c.displayName });
					}
				} catch {
					// Collaborator list is optional here
				}
			}
			const resp = await rpcClient.user.searchUsers(
				{ page: { pageSize: 20, pageToken: '', orderBy: '', query: { text, filters: [] } } },
				silentCallOptions
			);
			if (mine !== seq) return;
			const seen = new Set(found.map((o) => o.id));
			for (const u of resp.users) {
				if (!seen.has(u.id)) found.push({ id: u.id, username: u.username, displayName: u.displayName });
			}
			options = found;
			for (const o of options) names.set(o.id, o.username);
		} catch {
			if (mine === seq) options = [];
		} finally {
			if (mine === seq) loading = false;
		}
	}

	function onQuery() {
		clearTimeout(debounce);
		debounce = setTimeout(search, 200);
	}

	$effect(() => {
		if (open) search();
	});

	function toggle(id: string) {
		if (!canEdit) return;
		const next = selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id];
		selected = next;
		onChange?.(next);
	}

	function assignSelf() {
		const me = authStore.user?.id;
		if (me && !selected.includes(me)) toggle(me);
	}

	const labelOf = (id: string) => names.get(id) ?? id.slice(0, 8);
	const initial = (id: string) => labelOf(id).charAt(0).toUpperCase();
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Assignees</span>
		{#if canEdit}
			<Popover bind:open>
				<PopoverTrigger>
					{#snippet child({ props })}
						<button {...props} type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" title="Edit assignees" aria-label="Edit assignees">
							<Settings2 class="h-3.5 w-3.5" />
						</button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-72 p-0" align="end">
					<div class="border-b border-border/50 px-2 py-2 flex items-center gap-2">
						<Search class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
						<input class="flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground" placeholder="Search users" bind:value={query} oninput={onQuery} />
						{#if loading}
							<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />
						{/if}
					</div>
					<div class="max-h-72 overflow-y-auto py-1">
						{#if !loading && options.length === 0}
							<p class="px-3 py-4 text-[12.5px] text-muted-foreground text-center">No users match</p>
						{/if}
						{#each options as u (u.id)}
							{@const active = selected.includes(u.id)}
							<button
								type="button"
								class={cn('w-full flex items-center gap-2 px-2.5 py-1.5 text-left hover:bg-accent transition-colors', active && 'bg-accent/50')}
								onclick={() => toggle(u.id)}
							>
								<span class="h-4 w-4 shrink-0 inline-flex items-center justify-center">
									{#if active}<Check class="h-3.5 w-3.5 text-primary" />{/if}
								</span>
								<span class="h-5 w-5 rounded-full bg-primary/10 text-primary text-[10px] font-semibold inline-flex items-center justify-center shrink-0">{u.username.charAt(0).toUpperCase()}</span>
								<span class="min-w-0 flex-1 truncate text-[13px]">
									{u.username}
									{#if u.displayName && u.displayName !== u.username}
										<span class="text-muted-foreground text-[12px]">{u.displayName}</span>
									{/if}
								</span>
							</button>
						{/each}
					</div>
				</PopoverContent>
			</Popover>
		{/if}
	</div>
	<div class="space-y-1">
		{#if selected.length === 0}
			<span class="text-[12.5px] text-muted-foreground">
				No one
				{#if canEdit && authStore.user}
					<span class="mx-1">/</span>
					<button type="button" class="hover:text-foreground underline-offset-2 hover:underline" onclick={assignSelf}>assign yourself</button>
				{/if}
			</span>
		{:else}
			{#each selected as id (id)}
				<a href={resolve('/[namespace]', { namespace: labelOf(id) })} class="flex items-center gap-2 text-[13px] hover:text-primary transition-colors">
					<span class="h-5 w-5 rounded-full bg-primary/10 text-primary text-[10px] font-semibold inline-flex items-center justify-center shrink-0">{initial(id)}</span>
					<span class="truncate font-medium">{labelOf(id)}</span>
				</a>
			{/each}
		{/if}
	</div>
</div>
