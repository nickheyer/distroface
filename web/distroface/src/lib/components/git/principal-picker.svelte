<script lang="ts">
	import AsyncSelect from '$lib/components/async-select.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';

	// User and team ids, teams need an org id
	let {
		selected = $bindable([]),
		orgId = '',
		placeholder = 'Everyone with write access',
		disabled = false
	}: {
		selected: string[];
		orgId?: string;
		placeholder?: string;
		disabled?: boolean;
	} = $props();

	type Option = { value: string; label: string; description?: string };

	async function fetchPage(query: string, pageToken: string) {
		const text = query.trim();
		const users = await rpcClient.user.searchUsers(
			{ page: { pageSize: 20, pageToken, query: { text, filters: [] } } },
			silentCallOptions
		);
		const items: Option[] = users.users.map((u) => ({
			value: u.id,
			label: u.username,
			description: u.displayName && u.displayName !== u.username ? u.displayName : 'User'
		}));
		// Teams ride along on the first page only
		if (orgId && !pageToken) {
			try {
				const teams = await rpcClient.organization.listTeams(
					{ orgId, page: { pageSize: 50, query: { text, filters: [] } } },
					silentCallOptions
				);
				items.unshift(
					...teams.teams.map((t) => ({ value: t.id, label: t.name, description: 'Team' }))
				);
			} catch {
				// Teams are optional here
			}
		}
		return { items, nextPageToken: users.page?.nextPageToken ?? '' };
	}
</script>

<AsyncSelect bind:selected multiple {placeholder} searchPlaceholder="Search users and teams..." {fetchPage} {disabled} />
