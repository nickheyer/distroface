<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormField from '$lib/components/form-field.svelte';
	import { ArrowRight, AlertTriangle, Loader, Lock, CheckCircle } from '$lib/icons';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { errText } from '$lib/act.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import PasswordStrength from '$lib/components/password-strength.svelte';

	const token = $derived(page.params.token ?? '');

	let newPassword = $state('');
	let confirmPassword = $state('');
	let saving = $state(false);
	let done = $state(false);
	let submitError = $state('');
	let errors = $state<Record<string, string>>({});
	let touched = $state<Record<string, boolean>>({});

	const confirmMatch = $derived(confirmPassword.length > 0 && newPassword === confirmPassword);

	function validate(): boolean {
		errors = {};
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

	async function submit(e: Event) {
		e.preventDefault();
		touched = { new: true, confirm: true };
		if (!validate()) return;
		saving = true;
		submitError = '';
		try {
			await rpcClient.auth.resetPassword({ token, newPassword }, silentCallOptions);
			done = true;
		} catch (err) {
			submitError = errText(err);
		} finally {
			saving = false;
		}
	}
</script>

<svelte:head>
	<title>Reset password - Distroface</title>
</svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/40 p-4">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto mb-5 h-10 w-auto" />
			<h1 class="text-xl font-semibold tracking-tight">
				{done ? 'Password updated' : 'Choose a new password'}
			</h1>
			<p class="mt-1 text-sm text-muted-foreground">
				{done
					? 'Sign in with your new password to continue.'
					: 'The link works once and expires soon.'}
			</p>
		</div>

		<div class="rounded-lg border bg-card p-6 shadow-sm">
			{#if done}
				<div class="flex flex-col items-center py-2 text-center">
					<CheckCircle class="mb-4 size-6 text-success" />
					<Button size="lg" class="w-full" href={resolve('/login')}>
						Sign in
						<ArrowRight />
					</Button>
				</div>
			{:else}
				<form onsubmit={submit} class="space-y-4" novalidate>
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
						help={!touched.confirm || !errors.confirm
							? confirmMatch
								? 'Passwords match'
								: ''
							: ''}
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

					{#if submitError}
						<Alert variant="destructive">
							<AlertTriangle />
							<AlertDescription>{submitError}</AlertDescription>
						</Alert>
					{/if}

					<Button
						type="submit"
						size="lg"
						class="mt-2 w-full"
						disabled={saving || !newPassword || !confirmPassword}
					>
						{#if saving}
							<Loader class="animate-spin" />
							Updating password
						{:else}
							<Lock />
							Update password
						{/if}
					</Button>
				</form>
			{/if}
		</div>

		{#if !done}
			<p class="mt-4 text-center text-sm">
				<a
					href={resolve('/forgot-password')}
					class="text-muted-foreground transition-colors hover:text-foreground"
				>
					Request a new link
				</a>
			</p>
		{/if}
	</div>
</div>
