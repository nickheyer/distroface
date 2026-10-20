<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { ArrowRight, Loader2, MailCheck, MailX } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { errText } from '$lib/act.svelte';
	import { toast } from 'svelte-sonner';

	type Phase = 'verifying' | 'ok' | 'failed';
	let phase = $state<Phase>('verifying');
	let error = $state('');
	let resending = $state(false);
	let resent = $state(false);

	async function resend() {
		resending = true;
		try {
			await rpcClient.auth.requestEmailVerification({});
			resent = true;
			toast.success('Verification email sent');
		} catch {
			// error interceptor
		} finally {
			resending = false;
		}
	}

	onMount(async () => {
		const token = page.params.token ?? '';
		try {
			await rpcClient.auth.verifyEmail({ token }, silentCallOptions);
			phase = 'ok';
			if (authStore.isAuthenticated) await authStore.validateSession();
		} catch (err) {
			error = errText(err);
			phase = 'failed';
		}
	});
</script>

<svelte:head>
	<title>Verify email - Distroface</title>
</svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/40 p-4">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto mb-5 size-10 rounded-lg" />
			<h1 class="text-xl font-semibold tracking-tight">
				{phase === 'verifying'
					? 'Verifying your email'
					: phase === 'ok'
						? 'Email verified'
						: 'Verification failed'}
			</h1>
			<p class="mt-1 text-sm text-muted-foreground">
				{phase === 'verifying'
					? 'One moment'
					: phase === 'ok'
						? 'Your address is confirmed. You are all set.'
						: 'The link is invalid or has expired.'}
			</p>
		</div>

		<div class="rounded-lg border bg-card p-6 shadow-sm">
			<div class="flex flex-col items-center py-2 text-center">
				{#if phase === 'verifying'}
					<Loader2 class="mb-3 size-5 animate-spin text-muted-foreground" />
					<p class="text-sm text-muted-foreground">Please wait</p>
				{:else if phase === 'ok'}
					<MailCheck class="mb-4 size-6 text-success" />
					<Button
						size="lg"
						class="w-full"
						href={authStore.isAuthenticated ? resolve('/') : resolve('/login')}
					>
						{authStore.isAuthenticated ? 'Continue' : 'Sign in'}
						<ArrowRight />
					</Button>
				{:else}
					<MailX class="mb-4 size-6 text-destructive" />
					{#if error}
						<p class="mb-4 text-sm text-destructive">{error}</p>
					{/if}
					{#if authStore.isAuthenticated}
						<Button
							variant="outline"
							size="lg"
							class="w-full"
							onclick={resend}
							disabled={resending || resent}
						>
							{#if resending}
								<Loader2 class="animate-spin" />
								Sending
							{:else}
								{resent ? 'Link sent' : 'Send a new link'}
							{/if}
						</Button>
					{:else}
						<p class="text-sm text-muted-foreground">
							Sign in and request a new verification link from your profile.
						</p>
						<Button variant="outline" size="lg" class="mt-4 w-full" href={resolve('/login')}
							>Sign in</Button
						>
					{/if}
				{/if}
			</div>
		</div>
	</div>
</div>
