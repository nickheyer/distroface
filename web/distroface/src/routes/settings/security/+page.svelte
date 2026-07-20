<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { Lock, ShieldCheck, Check, ArrowRight, Loader2, KeyRound, Globe } from '@lucide/svelte';
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
			description: 'Sign in with a username and password stored on this server.',
			icon: KeyRound,
			connected: !isOIDC,
			show: true
		},
		{
			label: 'Single sign-on',
			description: 'Sign in through your organization’s identity provider.',
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
	const confirmMatch = $derived(
		confirmPassword.length > 0 && newPassword === confirmPassword
	);

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

<div class="space-y-6">
	<div>
		<h2 class="section-title">Security</h2>
		<p class="section-subtitle">Manage your password and account security.</p>
	</div>

	<Card class="border-border/60 pt-0">
		<div class="flex items-center gap-3 px-6 py-4 border-b border-border/40 bg-muted/20">
			<div class="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
				<ShieldCheck class="h-4 w-4 text-primary" />
			</div>
			<div>
				<h3 class="text-sm font-semibold">Connected accounts</h3>
				<p class="text-xs text-muted-foreground mt-0.5">How you sign in to this account.</p>
			</div>
		</div>
		<CardContent class="p-0">
			<div class="divide-y divide-border/40">
				{#each connectedAccounts.filter((a) => a.show) as account (account.label)}
					<div class="flex items-center gap-3 px-6 py-4">
						<account.icon class="h-4 w-4 text-muted-foreground shrink-0" />
						<div class="flex-1 min-w-0">
							<p class="text-sm font-medium">{account.label}</p>
							<p class="text-xs text-muted-foreground mt-0.5">{account.description}</p>
						</div>
						{#if account.connected}
							<Badge variant="outline" class="text-success border-success/30 gap-1 shrink-0">
								<Check class="h-3 w-3" />
								Connected
							</Badge>
						{:else}
							<span class="text-xs text-muted-foreground shrink-0">Not linked</span>
						{/if}
					</div>
				{/each}
			</div>
		</CardContent>
	</Card>

	{#if isOIDC}
		<Alert>
			<ShieldCheck class="h-4 w-4" />
			<AlertDescription>
				Your account is managed by <strong>{authStore.user?.authProvider}</strong>.
				Password changes are handled through your identity provider.
			</AlertDescription>
		</Alert>
	{:else}
		<Card class="border-border/60 pt-0">
			<div class="flex items-center gap-3 px-6 py-4 border-b border-border/40 bg-muted/20">
				<div class="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
					<Lock class="h-4 w-4 text-primary" />
				</div>
				<div>
					<h3 class="text-sm font-semibold">Change password</h3>
					<p class="text-xs text-muted-foreground mt-0.5">Update your account password.</p>
				</div>
			</div>
			<CardContent class="p-6">
				<form onsubmit={changePassword} class="space-y-4" novalidate>
					<div class="space-y-1.5">
						<Label for="current-pw" class="text-sm font-medium">Current password</Label>
						<PasswordInput
							id="current-pw"
							placeholder="Current password"
							autocomplete="current-password"
							bind:value={currentPassword}
							error={touched.current && !!errors.current}
							onblur={() => { touched.current = true; validate(); }}
						/>
						{#if touched.current && errors.current}
							<p class="text-[13px] text-destructive">{errors.current}</p>
						{/if}
					</div>

					<div class="space-y-1.5">
						<Label for="new-pw" class="text-sm font-medium">New password</Label>
						<PasswordInput
							id="new-pw"
							placeholder="Create a new password"
							autocomplete="new-password"
							bind:value={newPassword}
							error={touched.new && !!errors.new}
							onblur={() => { touched.new = true; validate(); }}
						/>
						{#if touched.new && errors.new}
							<p class="text-[13px] text-destructive">{errors.new}</p>
						{:else}
							<PasswordStrength password={newPassword} />
						{/if}
					</div>

					<div class="space-y-1.5">
						<Label for="confirm-pw" class="text-sm font-medium">Confirm new password</Label>
						<PasswordInput
							id="confirm-pw"
							placeholder="Confirm your new password"
							autocomplete="new-password"
							bind:value={confirmPassword}
							error={touched.confirm && !!errors.confirm}
							onblur={() => { touched.confirm = true; validate(); }}
						/>
						{#if touched.confirm && errors.confirm}
							<p class="text-[13px] text-destructive">{errors.confirm}</p>
						{:else if confirmMatch}
							<p class="text-[13px] text-success flex items-center gap-1">
								<Check class="h-3 w-3" />
								Passwords match
							</p>
						{/if}
					</div>

					<div class="flex justify-end pt-2">
						<Button
							type="submit"
							disabled={saving || !currentPassword || !newPassword}
						>
							{#if saving}
								<Loader2 class="h-4 w-4 mr-2 animate-spin" />
								Changing password...
							{:else}
								Update password
								<ArrowRight class="h-4 w-4 ml-1.5" />
							{/if}
						</Button>
					</div>
				</form>
			</CardContent>
		</Card>
	{/if}
</div>
