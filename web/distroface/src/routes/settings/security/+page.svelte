<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Badge } from '$lib/components/ui/badge';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Lock, ShieldCheck, Check, Loader2, KeyRound, Globe } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import PasswordInput from '$lib/components/password-input.svelte';
	import PasswordStrength from '$lib/components/password-strength.svelte';

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let touched = $state<Record<string, boolean>>({});
	let oidcEnabled = $state(false);

	const isOIDC = $derived(authStore.user?.authProvider !== 'local');
	const ssoLinked = $derived(isOIDC || !!authStore.user?.oidcLinked);

	const connectedAccounts = $derived([
		{
			label: 'Local password',
			description: 'Sign in with a username and password stored on this server',
			icon: KeyRound,
			connected: !isOIDC,
			show: true
		},
		{
			label: 'Single sign-on',
			description: 'Sign in through your organization’s identity provider',
			icon: Globe,
			connected: ssoLinked,
			show: oidcEnabled || ssoLinked
		}
	]);

	onMount(async () => {
		try {
			const status = await rpcClient.auth.getAuthStatus({}, silentCallOptions);
			oidcEnabled = status.oidcEnabled;
		} catch {
			// non-critical
		}
	});
	const confirmMatch = $derived(confirmPassword.length > 0 && newPassword === confirmPassword);

	function validate(): boolean {
		errors = {};
		if (!currentPassword) errors.current = 'Current password is required.';
		if (!newPassword) {
			errors.new = 'New password is required.';
		} else if (newPassword.length < 8) {
			errors.new = 'Must be at least 8 characters.';
		}
		if (!confirmPassword) {
			errors.confirm = 'Please confirm your new password.';
		} else if (newPassword !== confirmPassword) {
			errors.confirm = 'Passwords do not match.';
		}
		return Object.keys(errors).length === 0;
	}

	async function changePassword(e: Event) {
		e.preventDefault();
		touched = { current: true, new: true, confirm: true };
		if (!validate()) return;

		saving = true;
		try {
			await rpcClient.user.changePassword({ currentPassword, newPassword });
			toast.success('Password changed successfully');
			currentPassword = '';
			newPassword = '';
			confirmPassword = '';
			errors = {};
			touched = {};
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}
</script>

<FormPage level="section" title="Security" description="Manage your password and account security.">
	<FormSection title="Connected accounts" description="How you sign in to this account.">
		<div class="box divide-y">
			{#each connectedAccounts.filter((a) => a.show) as account (account.label)}
				<div class="flex items-center gap-3 px-4 py-3">
					<account.icon class="size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-0 flex-1">
						<p class="text-sm font-medium">{account.label}</p>
						<p class="mt-0.5 text-xs text-muted-foreground">{account.description}</p>
					</div>
					{#if account.connected}
						<Badge variant="success" class="shrink-0">
							<Check />
							Connected
						</Badge>
					{:else}
						<span class="shrink-0 text-xs text-muted-foreground">Not linked</span>
					{/if}
				</div>
			{/each}
		</div>
	</FormSection>

	<FormSection
		title="Change password"
		description={isOIDC
			? 'Your account is managed by your identity provider.'
			: 'Pick a password at least 8 characters long.'}
	>
		{#if isOIDC}
			<Alert variant="info">
				<ShieldCheck />
				<AlertDescription>
					Your account is managed by <strong>{authStore.user?.authProvider}</strong>. Password
					changes are handled through your identity provider.
				</AlertDescription>
			</Alert>
		{:else}
			<form class="form-fields" onsubmit={changePassword} novalidate>
				<FormField
					label="Current password"
					id="current-pw"
					error={touched.current ? errors.current : ''}
					span="half"
				>
					<PasswordInput
						id="current-pw"
						placeholder="Current password"
						autocomplete="current-password"
						bind:value={currentPassword}
						error={touched.current && !!errors.current}
						onblur={() => {
							touched.current = true;
							validate();
						}}
					/>
				</FormField>

				<FormField
					label="New password"
					id="new-pw"
					error={touched.new ? errors.new : ''}
					span="half"
					newRow
				>
					<PasswordInput
						id="new-pw"
						placeholder="Create a new password"
						autocomplete="new-password"
						bind:value={newPassword}
						error={touched.new && !!errors.new}
						onblur={() => {
							touched.new = true;
							validate();
						}}
					/>
					{#if !(touched.new && errors.new)}
						<PasswordStrength password={newPassword} />
					{/if}
				</FormField>

				<FormField
					label="Confirm new password"
					id="confirm-pw"
					error={touched.confirm ? errors.confirm : ''}
					help={!touched.confirm || !errors.confirm ? (confirmMatch ? 'Passwords match' : '') : ''}
					span="half"
				>
					<PasswordInput
						id="confirm-pw"
						placeholder="Confirm your new password"
						autocomplete="new-password"
						bind:value={confirmPassword}
						error={touched.confirm && !!errors.confirm}
						onblur={() => {
							touched.confirm = true;
							validate();
						}}
					/>
				</FormField>
				<div class="flex flex-wrap items-center gap-2 pt-1">
					<Button type="submit" disabled={saving || !currentPassword || !newPassword}>
						{#if saving}
							<Loader2 class="animate-spin" />
							Changing password
						{:else}
							<Lock />
							Update password
						{/if}
					</Button>
				</div>
			</form>
		{/if}
	</FormSection>
</FormPage>
