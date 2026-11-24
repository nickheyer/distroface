<script lang="ts">
	import { resolve } from '$app/paths';
	import { Terminal } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { cn, shellQuote as sh } from '$lib/utils';

	let {
		path,
		file,
		dfcli = '',
		auth = false,
		class: className
	}: {
		path: string;
		file: string;
		dfcli?: string;
		auth?: boolean;
		class?: string;
	} = $props();

	type Client = 'url' | 'curl' | 'wget' | 'httpie' | 'powershell' | 'dfcli';

	const labels: Record<Client, string> = {
		url: 'URL',
		curl: 'curl',
		wget: 'wget',
		httpie: 'HTTPie',
		powershell: 'PowerShell',
		dfcli: 'dfcli'
	};

	let tab = $state<Client>('curl');

	// Signed out requests pass only with anonymous access on
	const authOn = $derived(authStore.localAuthEnabled || authStore.oidcEnabled);
	const needsToken = $derived(authOn && (auth || !authStore.anonymousAccessEnabled));

	const url = $derived(window.location.origin + path);
	const clients = $derived(
		(Object.keys(labels) as Client[]).filter((c) => c !== 'dfcli' || dfcli !== '')
	);

	function ps(s: string): string {
		return `'${s.replace(/'/g, "''")}'`;
	}

	const commands = $derived<Record<Client, string>>({
		url,
		curl: `curl -fL${needsToken ? ' -H "Authorization: Bearer $TOKEN"' : ''} -o ${sh(file)} ${sh(url)}`,
		wget: `wget${needsToken ? ' --header="Authorization: Bearer $TOKEN"' : ''} -O ${sh(file)} ${sh(url)}`,
		httpie: `http --download -o ${sh(file)} ${sh(url)}${needsToken ? ' "Authorization:Bearer $TOKEN"' : ''}`,
		powershell: `Invoke-WebRequest -Uri ${ps(url)} -OutFile ${ps(file)}${needsToken ? ' -Headers @{ Authorization = "Bearer $env:TOKEN" }' : ''}`,
		dfcli
	});
	const command = $derived(commands[tab]);
</script>

<Popover>
	<PopoverTrigger>
		{#snippet child({ props })}
			<Button
				{...props}
				variant="ghost-muted"
				size="icon-sm"
				class={cn('shrink-0', className)}
				title="Download from a terminal"
				aria-label="Download commands for {file}"
			>
				<Terminal />
			</Button>
		{/snippet}
	</PopoverTrigger>
	<PopoverContent align="end" class="w-[min(30rem,calc(100vw-2rem))] p-0">
		<div class="flex items-center overflow-x-auto border-b px-1">
			{#each clients as c (c)}
				<button
					type="button"
					class="-mb-px shrink-0 border-b-2 px-2.5 py-2 text-xs font-medium transition-colors {tab ===
					c
						? 'border-primary text-foreground'
						: 'border-transparent text-muted-foreground hover:text-foreground'}"
					onclick={() => (tab = c)}
				>
					{labels[c]}
				</button>
			{/each}
		</div>
		<div class="space-y-2 p-3">
			<div class="flex items-start gap-2 rounded-md border bg-muted py-0.5 pr-0.5 pl-3">
				<code
					class="min-w-0 flex-1 py-1.5 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap select-all"
					>{command}</code
				>
				<CopyButton text={command} label="{labels[tab]} copied" />
			</div>
			<p class="text-xs text-muted-foreground">
				{#if tab === 'dfcli'}
					Uses the session saved by <code class="font-mono">dfcli login</code>
				{:else if needsToken}
					{tab === 'url' ? 'Send' : 'Set'}
					{#if authStore.isAuthenticated}
						a <a href={resolve('/settings/tokens')} class="link">token</a>
					{:else}
						a token
					{/if}
					{#if tab === 'url'}
						as a bearer Authorization header
					{:else if tab === 'powershell'}
						in <code class="font-mono">$env:TOKEN</code> first
					{:else}
						in <code class="font-mono">$TOKEN</code> first
					{/if}
				{:else}
					Public file, no credentials needed
				{/if}
			</p>
		</div>
	</PopoverContent>
</Popover>
