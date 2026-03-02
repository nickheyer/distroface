<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { rpcClient } from '$lib/api/rpc-client';
	import type { User } from '$lib/proto/distroface/v1/types_pb';

	let {
		value = $bindable(''),
		excludeUsernames = [],
		placeholder = 'Search users...'
	}: {
		value: string;
		excludeUsernames?: string[];
		placeholder?: string;
	} = $props();

	let results = $state<User[]>([]);
	let showDropdown = $state(false);
	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	function getInitials(user: User): string {
		const name = user.displayName || user.username;
		return name.split(/[\s-]+/).map((w) => w[0]).join('').toUpperCase().slice(0, 2);
	}

	function onInput() {
		clearTimeout(searchTimeout);
		if (value.length < 1) {
			results = [];
			showDropdown = false;
			return;
		}
		searchTimeout = setTimeout(async () => {
			try {
				const resp = await rpcClient.user.listUsers({ query: value, pageSize: 10 });
				results = resp.users.filter((u) => !excludeUsernames.includes(u.username));
				showDropdown = results.length > 0;
			} catch {
				results = [];
				showDropdown = false;
			}
		}, 250);
	}

	function selectUser(username: string) {
		value = username;
		showDropdown = false;
		results = [];
	}
</script>

<div class="relative">
	<Input
		bind:value
		{placeholder}
		oninput={onInput}
		onfocus={() => { if (results.length > 0) showDropdown = true; }}
		onblur={() => setTimeout(() => (showDropdown = false), 200)}
		autocomplete="off"
	/>
	{#if showDropdown}
		<div class="absolute z-50 top-full left-0 right-0 mt-1 bg-popover border border-border rounded-lg shadow-md max-h-60 overflow-y-auto">
			{#each results as user}
				<button
					type="button"
					class="w-full flex items-center gap-3 px-3 py-2 hover:bg-accent text-left transition-colors"
					onmousedown={() => selectUser(user.username)}
				>
					<Avatar class="h-7 w-7">
						<AvatarFallback class="text-[10px] bg-primary/10 text-primary font-medium">
							{getInitials(user)}
						</AvatarFallback>
					</Avatar>
					<div class="min-w-0 flex-1">
						<span class="text-sm font-medium block truncate">{user.username}</span>
						{#if user.displayName && user.displayName !== user.username}
							<span class="text-xs text-muted-foreground block truncate">{user.displayName}</span>
						{/if}
					</div>
				</button>
			{/each}
		</div>
	{/if}
</div>
