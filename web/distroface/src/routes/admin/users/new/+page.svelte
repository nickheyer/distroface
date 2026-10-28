<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader, Users } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import PasswordStrength from '$lib/components/password-strength.svelte';

	let username = $state('');
	let displayName = $state('');
	let email = $state('');
	let password = $state('');
	let selectedRoles = $state<string[]>([]);
	let mustChange = $state(true);
	let creating = $state(false);

	const listHref = resolve('/admin/users');
	const canCreate = $derived(authStore.hasPermission('users', 'create'));
	const passwordError = $derived(password && password.length < 8 ? 'At least 8 characters' : '');
	const valid = $derived(!!username && !!password && !passwordError);

	async function fetchRolePage(query: string, pageToken: string) {
		const resp = await rpcClient.role.listRoles({
			page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
		});
		return {
			items: resp.roles.map((r) => ({ value: r.id, label: r.name, description: r.description })),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	function generatePassword() {
		const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*';
		const bytes = crypto.getRandomValues(new Uint8Array(20));
		password = Array.from(bytes, (b) => chars[b % chars.length]).join('');
	}

	async function createUser() {
		if (!valid || creating) return;
		creating = true;
		try {
			await rpcClient.user.adminCreateUser({
				username,
				password,
				email,
				displayName,
				roleIds: selectedRoles,
				mustChangePassword: mustChange
			});
		} catch {
			creating = false;
			return;
		}
		toast.success('User created');
		await goto(resolve('/admin/users'));
	}
</script>

<svelte:head>
	<title>New user</title>
</svelte:head>

{#if !canCreate}
	<EmptyState
		icon={Users}
		message="You cannot create users"
		description="Ask an administrator for the users permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to users</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="New user"
		description="A local account with a password you set."
		backHref={listHref}
		backLabel="Users"
	>
		<FormSection title="Account" description="Sign-in name and contact details.">
			<FormField
				label="Username"
				id="create-username"
				required
				help="Lowercase letters, numbers, dots, dashes, underscores"
				span="half"
			>
				<Input id="create-username" bind:value={username} placeholder="jdoe" autocomplete="off" />
			</FormField>
			<FormField label="Display name" id="create-display-name" span="half">
				<Input
					id="create-display-name"
					bind:value={displayName}
					placeholder="Jane Doe"
					autocomplete="off"
				/>
			</FormField>
			<FormField label="Email" id="create-email" span="half">
				<Input
					id="create-email"
					bind:value={email}
					placeholder="user@example.com"
					autocomplete="off"
				/>
			</FormField>
		</FormSection>

		<FormSection title="Password" description="Initial credentials for the account.">
			<FormField label="Password" id="create-password" required error={passwordError} span="half">
				<div class="flex gap-2">
					<div class="flex-1">
						<PasswordInput
							id="create-password"
							bind:value={password}
							placeholder="Set a password"
							autocomplete="new-password"
							aria-invalid={!!passwordError}
							data-1p-ignore
							data-lpignore="true"
							data-bwignore
						/>
					</div>
					<Button variant="outline" class="shrink-0" onclick={generatePassword}>Generate</Button>
				</div>
				<PasswordStrength {password} />
			</FormField>
			<FormField label="Require password change at first login" layout="toggle">
				<Switch bind:checked={mustChange} />
			</FormField>
		</FormSection>

		<FormSection title="Access" description="Roles granted to the new account.">
			<FormField label="Roles" help="Empty assigns the default roles">
				<AsyncSelect
					multiple
					bind:selected={selectedRoles}
					placeholder="Select roles"
					searchPlaceholder="Search roles"
					fetchPage={fetchRolePage}
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={createUser} disabled={creating || !valid}>
				{#if creating}<Loader class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create user'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
