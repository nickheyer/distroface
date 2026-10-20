<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import FormField from '$lib/components/form-field.svelte';
	import { Lock, ArrowRight, Loader2, LogOut } from '@lucide/svelte';
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

<div class="flex min-h-screen items-center justify-center bg-muted/40 p-4">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto mb-5 size-10 rounded-lg" />
			<h1 class="text-xl font-semibold tracking-tight">Update your password</h1>
			<p class="mt-1 text-sm text-muted-foreground">
				{#if authStore.user?.mustChangePassword}
					An administrator requires you to set a new password before continuing.
				{:else}
					Choose a new password for your account.
				{/if}
			</p>
		</div>

		<div class="rounded-lg border bg-card p-6 shadow-sm">
			<form onsubmit={changePassword} class="space-y-4" novalidate>
				<FormField
					label="Current password"
					id="current-pw"
					error={touched.current ? errors.current : ''}
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

				<FormField label="New password" id="new-pw" error={touched.new ? errors.new : ''}>
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

				<Button
					type="submit"
					size="lg"
					class="mt-2 w-full"
					disabled={saving || !currentPassword || !newPassword}
				>
					{#if saving}
						<Loader2 class="animate-spin" />
						Updating password
					{:else}
						<Lock />
						Update password
						<ArrowRight />
					{/if}
				</Button>
			</form>
		</div>

		<div class="mt-4 text-center">
			<Button variant="ghost-muted" size="sm" onclick={handleLogout}>
				<LogOut />
				Sign out
			</Button>
		</div>
	</div>
</div>
