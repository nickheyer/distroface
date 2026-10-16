<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { ArrowRight, Check, Loader2, Lock, CircleCheck } from '@lucide/svelte';
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

<div class="flex min-h-screen items-center justify-center p-4 bg-muted/30">
	<div class="w-full max-w-105">
		<div class="text-center mb-8">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto h-12 w-12 rounded-xl mb-5" />
			<h1 class="text-xl font-semibold tracking-tight">{done ? 'Password updated' : 'Choose a new password'}</h1>
			<p class="text-sm text-muted-foreground mt-1.5">
				{done ? 'Sign in with your new password to continue.' : 'The link works once and expires soon.'}
			</p>
		</div>

		<Card class="border-border/60">
			<CardContent class="p-6">
				{#if done}
					<div class="flex flex-col items-center text-center py-2">
						<div class="h-12 w-12 rounded-full bg-success/10 flex items-center justify-center mb-4">
							<CircleCheck class="h-6 w-6 text-success" />
						</div>
						<Button class="mt-3 w-full h-10" href={resolve('/login')}>
							Sign in
							<ArrowRight class="h-4 w-4 ml-1.5" />
						</Button>
					</div>
				{:else}
					<form onsubmit={submit} class="space-y-4" novalidate>
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

						{#if submitError}
							<p class="text-[13px] text-destructive rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2">
								{submitError}
							</p>
						{/if}

						<Button type="submit" class="w-full h-10 mt-1" disabled={saving || !newPassword || !confirmPassword}>
							{#if saving}
								<Loader2 class="h-4 w-4 mr-2 animate-spin" />
								Updating password...
							{:else}
								<Lock class="h-4 w-4 mr-1.5" />
								Update password
							{/if}
						</Button>
					</form>
				{/if}
			</CardContent>
		</Card>

		{#if !done}
			<p class="text-center text-sm mt-4">
				<a href={resolve('/forgot-password')} class="text-muted-foreground hover:text-foreground transition-colors">
					Request a new link
				</a>
			</p>
		{/if}
	</div>
</div>
