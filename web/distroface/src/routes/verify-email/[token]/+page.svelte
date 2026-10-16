<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Card, CardContent } from '$lib/components/ui/card';
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

<div class="flex min-h-screen items-center justify-center p-4 bg-muted/30">
	<div class="w-full max-w-105">
		<div class="text-center mb-8">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto h-12 w-12 rounded-xl mb-5" />
			<h1 class="text-xl font-semibold tracking-tight">
				{phase === 'verifying' ? 'Verifying your email' : phase === 'ok' ? 'Email verified' : 'Verification failed'}
			</h1>
			<p class="text-sm text-muted-foreground mt-1.5">
				{phase === 'verifying'
					? 'One moment...'
					: phase === 'ok'
						? 'Your address is confirmed. You are all set.'
						: 'The link is invalid or has expired.'}
			</p>
		</div>

		<Card class="border-border/60">
			<CardContent class="flex flex-col items-center text-center p-8">
				{#if phase === 'verifying'}
					<Loader2 class="h-6 w-6 text-primary animate-spin mb-3" />
					<p class="text-sm text-muted-foreground">Please wait...</p>
				{:else if phase === 'ok'}
					<div class="h-12 w-12 rounded-full bg-success/10 flex items-center justify-center mb-4">
						<MailCheck class="h-6 w-6 text-success" />
					</div>
					<Button class="w-full h-10" href={authStore.isAuthenticated ? resolve('/') : resolve('/login')}>
						{authStore.isAuthenticated ? 'Continue' : 'Sign in'}
						<ArrowRight class="h-4 w-4 ml-1.5" />
					</Button>
				{:else}
					<div class="h-12 w-12 rounded-full bg-destructive/10 flex items-center justify-center mb-4">
						<MailX class="h-6 w-6 text-destructive" />
					</div>
					{#if error}
						<p class="text-[13px] text-destructive mb-4">{error}</p>
					{/if}
					{#if authStore.isAuthenticated}
						<Button variant="outline" class="w-full h-10" onclick={resend} disabled={resending || resent}>
							{#if resending}
								<Loader2 class="h-4 w-4 mr-2 animate-spin" />
								Sending...
							{:else}
								{resent ? 'Link sent' : 'Send a new link'}
							{/if}
						</Button>
					{:else}
						<p class="text-sm text-muted-foreground">Sign in and request a new verification link from your profile.</p>
						<Button variant="outline" class="w-full h-10 mt-4" href={resolve('/login')}>Sign in</Button>
					{/if}
				{/if}
			</CardContent>
		</Card>
	</div>
</div>
