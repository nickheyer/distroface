<script lang="ts">
	import { page } from '$app/state';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import * as Dialog from '$lib/components/ui/dialog';
	import { dialogShellClass } from '$lib/components/ui/dialog/dialog-content.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormField from '$lib/components/form-field.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Terminal, AlertTriangle, Loader, X } from '$lib/icons';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';
	import { cn } from '$lib/utils';

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
	const gitCredentialExample = 'git config --global credential.helper "dfcli git credential"';

	function storageKey(userId: string): string {
		return `df_cli_prompt_${userId}`;
	}

	async function maybePrompt(userId: string) {
		if (localStorage.getItem(storageKey(userId))) return;
		try {
			const resp = await rpcClient.token.listAPITokens(
				{ page: { pageSize: 1 } },
				silentCallOptions
			);
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

{#snippet commandBox(label: string, command: string)}
	<div class="box">
		<div class="box-header">
			<span class="flex items-center gap-2">
				<Terminal class="size-3.5 text-muted-foreground" />
				{label}
			</span>
			<CopyButton text={command} label="Command copied" />
		</div>
		<pre
			class="px-4 py-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap select-all">{command}</pre>
	</div>
{/snippet}

<Dialog.Root
	bind:open
	onOpenChange={(v) => {
		if (!v) dismiss();
	}}
>
	<Dialog.Portal>
		<Dialog.Overlay />
		<DialogPrimitive.Content
			data-slot="dialog-content"
			class={cn(dialogShellClass, 'max-h-[min(88vh,56rem)] sm:max-w-xl')}
		>
			<div class="flex shrink-0 items-start gap-3 border-b px-6 py-4">
				<Terminal class="mt-1 size-4 shrink-0 text-muted-foreground" />
				<div class="min-w-0 flex-1">
					<Dialog.Title>{newPlaintextToken ? 'Token created' : 'Set up CLI access'}</Dialog.Title>
					<Dialog.Description class="mt-0.5">
						{newPlaintextToken
							? 'Save it and use it like a password'
							: 'Generate a personal access token'}
					</Dialog.Description>
				</div>
				<DialogPrimitive.Close
					class="-mt-1 -mr-1.5 inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50"
				>
					<X class="size-4" />
					<span class="sr-only">Close</span>
				</DialogPrimitive.Close>
			</div>

			<div class="min-h-0 flex-1 overflow-y-auto px-6 py-5">
				{#if newPlaintextToken}
					<div class="space-y-4">
						<Alert variant="destructive">
							<AlertTriangle />
							<AlertDescription
								>Copy your token now. You won't be able to see it again.</AlertDescription
							>
						</Alert>

						<div class="flex items-center gap-2">
							<code class="code-inline min-w-0 flex-1">{newPlaintextToken}</code>
							<CopyButton text={newPlaintextToken} label="Token copied" />
						</div>

						{@render commandBox('Log in with Docker', dockerLoginExample)}
						{@render commandBox('Log in with dfcli', dfcliLoginExample)}
						{#if configStore.gitEnabled}
							{@render commandBox('Let git use the dfcli login', gitCredentialExample)}
						{/if}
					</div>
				{:else}
					<div class="space-y-4">
						<p class="text-sm text-muted-foreground">
							Tools like docker can't authenticate in a browser via SSO.
							<br />
							They authenticate with a personal access token (PAT) instead.
						</p>

						<FormField label="Token name" id="cli-token-name">
							<Input id="cli-token-name" bind:value={tokenName} placeholder="CLI access" />
						</FormField>
						<FormField label="Expiration" id="cli-token-expiry">
							<Select
								type="single"
								value={tokenExpiryOption}
								onValueChange={(v) => {
									if (v) tokenExpiryOption = v;
								}}
							>
								<SelectTrigger class="w-full" id="cli-token-expiry">
									{expiryOptions.find((o) => o.value === tokenExpiryOption)?.label ??
										'Select expiry'}
								</SelectTrigger>
								<SelectContent>
									{#each expiryOptions as option (option.label)}
										<SelectItem value={option.value}>{option.label}</SelectItem>
									{/each}
								</SelectContent>
							</Select>
						</FormField>
					</div>
				{/if}
			</div>

			<div class="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-6 py-3.5">
				{#if newPlaintextToken}
					<Button onclick={dismiss}>Done</Button>
				{:else}
					<Button variant="outline" onclick={dismiss}>Maybe later</Button>
					<Button onclick={createToken} disabled={creating || !tokenName.trim()}>
						{#if creating}
							<Loader class="animate-spin" />
						{/if}
						{creating ? 'Generating' : 'Generate token'}
					</Button>
				{/if}
			</div>
		</DialogPrimitive.Content>
	</Dialog.Portal>
</Dialog.Root>
