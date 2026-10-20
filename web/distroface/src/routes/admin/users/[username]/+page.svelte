<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Loader2, UserPen } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import type { User } from '$lib/proto/distroface/v1/types_pb';

	const username = $derived(page.params.username ?? '');
	const listHref = resolve('/admin/users');
	const canUpdate = $derived(authStore.hasPermission('users', 'update'));

	let user = $state<User | undefined>(undefined);
	let loading = $state(true);
	let email = $state('');
	let active = $state(true);
	let selectedRoles = $state<string[]>([]);
	let saving = $state(false);

	function getInitials(u: User): string {
		const name = u.displayName || u.username;
		return name
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	async function fetchRolePage(query: string, pageToken: string) {
		const resp = await rpcClient.role.listRoles({
			page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
		});
		return {
			items: resp.roles.map((r) => ({ value: r.id, label: r.name, description: r.description })),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.user.getUser({ username }, silentCallOptions);
			user = resp.user;
			if (user) {
				email = user.email;
				active = user.isActive;
				selectedRoles = user.roles.map((r) => r.id);
			}
		} catch {
			user = undefined;
		} finally {
			loading = false;
		}
	}

	async function save() {
		if (!user || saving) return;
		saving = true;
		try {
			await rpcClient.user.adminUpdateUser({
				userId: user.id,
				email: email || undefined,
				isActive: active,
				roleIds: selectedRoles
			});
		} catch {
			saving = false;
			return;
		}
		toast.success('User updated');
		await goto(resolve('/admin/users'));
	}

	onMount(() => {
		if (canUpdate) load();
	});
</script>

<svelte:head>
	<title>Edit user - {username}</title>
</svelte:head>

{#if !canUpdate}
	<EmptyState
		icon={UserPen}
		message="You cannot edit users"
		description="Ask an administrator for the users permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to users</Button>
		{/snippet}
	</EmptyState>
{:else if loading}
	<div class="box divide-y">
		{#each { length: 4 }, i (i)}
			<div class="flex items-center gap-3 px-4 py-3">
				<Skeleton class="size-7 rounded-full" />
				<Skeleton class="h-4 w-32" />
				<Skeleton class="h-4 w-48" />
				<Skeleton class="ml-auto h-4 w-16 rounded-full" />
			</div>
		{/each}
	</div>
{:else if !user}
	<EmptyState
		icon={UserPen}
		message="User not found"
		description="No account named {username} exists on this instance."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to users</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Edit user"
		description="Contact address, sign-in status, and role assignments."
		backHref={listHref}
		backLabel="Users"
	>
		<FormSection title="Account" description="Who this is and how they sign in.">
			<div class="flex items-center gap-3 rounded-lg border bg-card p-4">
				<Avatar class="size-12">
					<AvatarFallback class="text-base">{getInitials(user)}</AvatarFallback>
				</Avatar>
				<div class="min-w-0">
					<p class="text-sm font-semibold">{user.username}</p>
					<p class="mt-0.5 text-xs text-muted-foreground">
						{user.authProvider === 'local' ? 'Local account' : user.authProvider}
						{#if user.createdAt}
							&middot; Joined {relativeTime(timestampDate(user.createdAt))}
						{/if}
					</p>
				</div>
			</div>

			<FormField label="Email" id="edit-email" span="half">
				<Input id="edit-email" bind:value={email} placeholder="user@example.com" />
			</FormField>
		</FormSection>

		<FormSection title="Access" description="Sign-in status and roles.">
			<FormField label="Active" help="Inactive users cannot sign in" layout="toggle">
				<Switch bind:checked={active} />
			</FormField>
			<FormField label="Roles">
				<AsyncSelect
					multiple
					bind:selected={selectedRoles}
					initialSelected={user.roles.map((r) => ({ value: r.id, label: r.name }))}
					placeholder="Select roles"
					searchPlaceholder="Search roles"
					fetchPage={fetchRolePage}
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={save} disabled={saving}>
				{#if saving}<Loader2 class="animate-spin" />{/if}
				{saving ? 'Saving' : 'Save changes'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
