<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Lock, Check, ArrowRight, Loader2, LogOut } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
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

	const confirmMatch = $derived(confirmPassword.length > 0 && newPassword === confirmPassword);

	// Root layout owns session init, unauthenticated visitors go to login
	$effect(() => {
		if (!authStore.loading && !authStore.user) {
			goto(resolve('/login'));
		}
	});

	function validate(): boolean {
		errors = {};
		if (!currentPassword) errors.current = 'Current password is required.';
		if (!newPassword) {
			errors.new = 'New password is required.';
		} else if (newPassword.length < 8) {
			errors.new = 'Must be at least 8 characters.';
		} else if (newPassword === currentPassword) {
			errors.new = 'New password must be different.';
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
			toast.success('Password updated');
			await authStore.validateSession();
			goto(resolve('/'));
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}

	async function handleLogout() {
		await authStore.logout();
		goto(resolve('/login'));
	}
</script>

<svelte:head>
	<title>Change password - Distroface</title>
</svelte:head>

<div class="min-h-screen flex items-center justify-center px-4 py-12">
	<div class="w-full max-w-md space-y-6">
		<div class="flex flex-col items-center gap-3 text-center">
			<img src="/splash-icon.png" alt="Distroface" class="h-14 w-14 rounded-2xl" />
			<div>
				<h1 class="text-xl font-bold tracking-tight">Update your password</h1>
				<p class="text-sm text-muted-foreground mt-1">
					{#if authStore.user?.mustChangePassword}
						An administrator requires you to set a new password before continuing.
					{:else}
						Choose a new password for your account.
					{/if}
				</p>
			</div>
		</div>

		<Card class="border-border/60">
			<CardContent class="p-6">
				<form onsubmit={changePassword} class="space-y-4" novalidate>
					<div class="space-y-1.5">
						<Label for="current-pw" class="text-sm font-medium">Current password</Label>
						<PasswordInput
							id="current-pw"
							placeholder="Enter your current password"
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

					<Button type="submit" class="w-full" disabled={saving || !currentPassword || !newPassword}>
						{#if saving}
							<Loader2 class="h-4 w-4 mr-2 animate-spin" />
							Updating password...
						{:else}
							<Lock class="h-4 w-4 mr-1.5" />
							Update password
							<ArrowRight class="h-4 w-4 ml-1.5" />
						{/if}
					</Button>
				</form>
			</CardContent>
		</Card>

		<div class="text-center">
			<Button variant="ghost" size="sm" class="text-muted-foreground" onclick={handleLogout}>
				<LogOut class="h-3.5 w-3.5 mr-1.5" />
				Sign out
			</Button>
		</div>
	</div>
</div>
