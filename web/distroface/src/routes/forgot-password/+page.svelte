<script lang="ts">
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import FormField from '$lib/components/form-field.svelte';
	import { ArrowRight, ArrowLeft, Loader, MailCheck } from '$lib/icons';
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
			await rpcClient.auth.requestPasswordReset(
				{ identifier: identifier.trim() },
				silentCallOptions
			);
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

<div class="flex min-h-screen items-center justify-center bg-muted/40 p-4">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center">
			<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto mb-5 h-10 w-auto" />
			<h1 class="text-xl font-semibold tracking-tight">
				{sent ? 'Check your email' : 'Forgot your password?'}
			</h1>
			<p class="mt-1 text-sm text-muted-foreground">
				{sent
					? 'If an account matches, a reset link is on its way. It expires soon.'
					: 'Enter your username or email and we will send a reset link.'}
			</p>
		</div>

		<div class="rounded-lg border bg-card p-6 shadow-sm">
			{#if sent}
				<div class="flex flex-col items-center py-2 text-center">
					<MailCheck class="mb-3 size-6 text-success" />
					<p class="text-sm text-muted-foreground">
						Did not get it? Check your spam folder, or try again in a few minutes.
					</p>
					<Button
						variant="outline"
						class="mt-5"
						onclick={() => {
							sent = false;
							identifier = '';
							touched = false;
						}}
					>
						Send another link
					</Button>
				</div>
			{:else}
				<form onsubmit={submit} class="space-y-4" novalidate>
					<FormField
						label="Username or email"
						id="reset-id"
						error={invalid ? 'Username or email is required.' : ''}
					>
						<Input
							id="reset-id"
							type="text"
							placeholder="Username or email"
							autocomplete="username"
							bind:value={identifier}
							aria-invalid={invalid}
							onblur={() => (touched = true)}
						/>
					</FormField>
					<Button type="submit" size="lg" class="mt-2 w-full" disabled={submitting}>
						{#if submitting}
							<Loader class="animate-spin" />
							Sending
						{:else}
							Send reset link
							<ArrowRight />
						{/if}
					</Button>
				</form>
			{/if}
		</div>

		<p class="mt-4 text-center text-sm">
			<a
				href={resolve('/login')}
				class="inline-flex items-center gap-1.5 text-muted-foreground transition-colors hover:text-foreground"
			>
				<ArrowLeft class="size-3.5" />
				Back to sign in
			</a>
		</p>
	</div>
</div>
