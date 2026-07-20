<script lang="ts">
	import { page } from '$app/state';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Terminal, AlertTriangle } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';

	let open = $state(false);
	let checked = $state(false);
	let tokenName = $state('CLI access');
	let tokenExpiryOption = $state('never');
	let creating = $state(false);
	let newPlaintextToken = $state<string | null>(null);

	const expiryOptions = [
		{ value: '7', label: '7 days' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	const registryHost = $derived(portalStore.host(configStore.publicHostname));
	const dockerLoginExample = $derived(
		`docker login ${registryHost} \\\n  -u ${authStore.user?.username} \\\n  -p ${newPlaintextToken ?? 'YOUR_TOKEN'}`
	);
	const dfcliLoginExample = $derived(
		`dfcli --server ${portalStore.scheme()}://${registryHost} \\\n  login --token ${newPlaintextToken ?? 'YOUR_TOKEN'}`
	);

	function storageKey(userId: string): string {
		return `df_cli_prompt_${userId}`;
	}

	async function maybePrompt(userId: string) {
		if (localStorage.getItem(storageKey(userId))) return;
		try {
			const resp = await rpcClient.token.listAPITokens({ page: { pageSize: 1 } }, silentCallOptions);
			if (resp.tokens.length === 0) {
				open = true;
			} else {
				localStorage.setItem(storageKey(userId), 'has-tokens');
			}
		} catch {
			// Best effort, never block the app over a nudge
		}
	}

	$effect(() => {
		const user = authStore.user;
		const path = page.url.pathname;
		if (!user || checked || user.mustChangePassword) return;
		if (!authStore.canCreateTokens) {
			checked = true;
			return;
		}
		// Already on the tokens page, check again after they navigate away
		if (path.startsWith('/settings/tokens')) return;
		checked = true;
		void maybePrompt(user.id);
	});

	function dismiss() {
		if (authStore.user) {
			localStorage.setItem(storageKey(authStore.user.id), 'dismissed');
		}
		open = false;
		newPlaintextToken = null;
	}

	async function createToken() {
		if (!tokenName.trim()) return;
		creating = true;
		try {
			const expiryDays = tokenExpiryOption !== 'never' ? Number(tokenExpiryOption) : undefined;
			const resp = await rpcClient.token.createAPIToken({
				name: tokenName.trim(),
				expiresInDays: expiryDays
			});
			newPlaintextToken = resp.plaintextToken;
			if (authStore.user) {
				localStorage.setItem(storageKey(authStore.user.id), 'created');
			}
			toast.success('Token created');
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
	}
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) dismiss(); }}>
	<Dialog.Portal>
		<Dialog.Overlay />
		<DialogPrimitive.Content
			data-slot="dialog-content"
			class="bg-background data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 fixed left-[50%] top-[50%] z-50 flex flex-col w-full max-w-[calc(100%-2rem)] translate-x-[-50%] translate-y-[-50%] overflow-hidden rounded-lg border shadow-lg duration-200 sm:max-w-lg"
		>
			<div class="flex items-center gap-3 px-6 pt-6 pb-4">
				<div class="h-10 w-10 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
					<Terminal class="h-5 w-5 text-primary" />
				</div>
				<div>
					<h2 class="text-lg font-semibold">
						{newPlaintextToken ? 'Token created' : 'Set up CLI access'}
					</h2>
					<p class="text-sm text-muted-foreground">
						{newPlaintextToken
							? 'Save it and use it like a password'
							: 'Generate a personal access token'}
					</p>
				</div>
			</div>

			<div class="px-6 pb-6 space-y-4 overflow-y-auto max-h-[60vh]">
				{#if newPlaintextToken}
					<Alert variant="destructive">
						<AlertTriangle class="h-4 w-4" />
						<AlertDescription>
							Copy your token now. You won't be able to see it again.
						</AlertDescription>
					</Alert>

					<div class="flex items-center gap-2">
						<code class="code-inline flex-1">{newPlaintextToken}</code>
						<CopyButton text={newPlaintextToken} label="Token copied!" />
					</div>

					<div class="rounded-lg border border-border/50 bg-muted/30 overflow-hidden">
						<div class="flex items-center gap-2 px-3 py-1.5 border-b border-border/40">
							<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
							<span class="flex-1 text-xs font-medium text-muted-foreground">Log in with Docker</span>
							<CopyButton text={dockerLoginExample} label="Command copied!" />
						</div>
						<pre class="px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all select-all">{dockerLoginExample}</pre>
					</div>

					<div class="rounded-lg border border-border/50 bg-muted/30 overflow-hidden">
						<div class="flex items-center gap-2 px-3 py-1.5 border-b border-border/40">
							<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
							<span class="flex-1 text-xs font-medium text-muted-foreground">Log in with dfcli</span>
							<CopyButton text={dfcliLoginExample} label="Command copied!" />
						</div>
						<pre class="px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all select-all">{dfcliLoginExample}</pre>
					</div>
				{:else}
					<p class="text-sm text-muted-foreground">
						Tools like docker can't authenticate in a browser via SSO.
            <br>
            They authenticate with a personal access token (PAT) instead.
					</p>

					<div class="space-y-3">
						<div class="space-y-1.5">
							<Label for="cli-token-name" class="text-sm font-medium">Token name</Label>
							<Input id="cli-token-name" bind:value={tokenName} placeholder="CLI access" />
						</div>
						<div class="space-y-1.5">
							<Label class="text-sm font-medium">Expiration</Label>
							<Select
								type="single"
								value={tokenExpiryOption}
								onValueChange={(v) => { if (v) tokenExpiryOption = v; }}
							>
								<SelectTrigger class="w-full">
									{expiryOptions.find((o) => o.value === tokenExpiryOption)?.label ?? 'Select expiry'}
								</SelectTrigger>
								<SelectContent>
									{#each expiryOptions as option (option.label)}
										<SelectItem value={option.value}>{option.label}</SelectItem>
									{/each}
								</SelectContent>
							</Select>
						</div>
					</div>
				{/if}
			</div>

			<div class="flex items-center justify-end gap-3 px-6 py-4 border-t">
				{#if newPlaintextToken}
					<Button onclick={dismiss}>Done</Button>
				{:else}
					<Button variant="outline" onclick={dismiss}>Maybe later</Button>
					<Button onclick={createToken} disabled={creating || !tokenName.trim()}>
						{creating ? 'Generating...' : 'Generate token'}
					</Button>
				{/if}
			</div>
		</DialogPrimitive.Content>
	</Dialog.Portal>
</Dialog.Root>
