<script lang="ts">
	import { resolve } from '$app/paths';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { ArrowRight, ArrowLeft, Loader2, MailCheck } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';

	let identifier = $state('');
	let submitting = $state(false);
	let sent = $state(false);
	let touched = $state(false);

	const invalid = $derived(touched && !identifier.trim());

	// Sent state always, accounts cannot be enumerated
	async function submit(e: Event) {
		e.preventDefault();
		touched = true;
		if (!identifier.trim()) return;
		submitting = true;
		try {
			await rpcClient.auth.requestPasswordReset({ identifier: identifier.trim() }, silentCallOptions);
		} catch {
			// Same outcome either way
		} finally {
			submitting = false;
			sent = true;
		}
	}
</script>

<svelte:head>
	<title>Forgot password - Distroface</title>
</svelte:head>

<div class="flex min-h-screen items-center justify-center p-4 bg-muted/30">
	<div class="w-full max-w-105">
		<div class="text-center mb-8">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto h-12 w-12 rounded-xl mb-5" />
			<h1 class="text-xl font-semibold tracking-tight">{sent ? 'Check your email' : 'Forgot your password?'}</h1>
			<p class="text-sm text-muted-foreground mt-1.5">
				{sent
					? 'If an account matches, a reset link is on its way. It expires soon.'
					: 'Enter your username or email and we will send a reset link.'}
			</p>
		</div>

		<Card class="border-border/60">
			<CardContent class="p-6">
				{#if sent}
					<div class="flex flex-col items-center text-center py-2">
						<div class="h-12 w-12 rounded-full bg-success/10 flex items-center justify-center mb-4">
							<MailCheck class="h-6 w-6 text-success" />
						</div>
						<p class="text-sm text-muted-foreground">
							Did not get it? Check your spam folder, or try again in a few minutes.
						</p>
						<Button variant="outline" class="mt-5" onclick={() => { sent = false; identifier = ''; touched = false; }}>
							Send another link
						</Button>
					</div>
				{:else}
					<form onsubmit={submit} class="space-y-4" novalidate>
						<div class="space-y-1.5">
							<Label for="reset-id" class="text-sm font-medium">Username or email</Label>
							<Input
								id="reset-id"
								type="text"
								placeholder="Username or email"
								autocomplete="username"
								bind:value={identifier}
								aria-invalid={invalid}
								onblur={() => (touched = true)}
							/>
							{#if invalid}
								<p class="text-[13px] text-destructive">Username or email is required.</p>
							{/if}
						</div>
						<Button type="submit" class="w-full h-10 mt-1" disabled={submitting}>
							{#if submitting}
								<Loader2 class="h-4 w-4 mr-2 animate-spin" />
								Sending...
							{:else}
								Send reset link
								<ArrowRight class="h-4 w-4 ml-1.5" />
							{/if}
						</Button>
					</form>
				{/if}
			</CardContent>
		</Card>

		<p class="text-center text-sm mt-4">
			<a href={resolve('/login')} class="inline-flex items-center gap-1.5 text-muted-foreground hover:text-foreground transition-colors">
				<ArrowLeft class="h-3.5 w-3.5" />
				Back to sign in
			</a>
		</p>
	</div>
</div>
