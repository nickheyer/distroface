<script lang="ts">
	import { onMount } from 'svelte';
	import { Terminal, Download, FileArchive } from '@lucide/svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { portalStore } from '$lib/stores/portal.svelte';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	let {
		repo,
		ref = ''
	}: {
		repo: GitRepository;
		ref?: string;
	} = $props();

	const httpUrl = $derived(repo.httpCloneUrl || portalStore.httpCloneURL(repo.namespace, repo.name));
	const sshUrl = $derived(repo.sshCloneUrl);
	const cliRef = $derived(
		portalStore.isPortal && portalStore.mapUnqualified && repo.namespace === portalStore.orgName
			? repo.name
			: `${repo.namespace}/${repo.name}`
	);

	let tab = $state<'http' | 'ssh'>('http');
	const url = $derived(tab === 'ssh' && sshUrl ? sshUrl : httpUrl);

	let zipUrl = $state('');
	let tarUrl = $state('');

	async function loadArchives() {
		if (repo.isEmpty) return;
		const target = ref || repo.defaultBranch;
		try {
			const [zip, tar] = await Promise.all([
				rpcClient.gitFiles.getArchiveURL({ namespace: repo.namespace, name: repo.name, ref: target, format: 'zip' }, silentCallOptions),
				rpcClient.gitFiles.getArchiveURL({ namespace: repo.namespace, name: repo.name, ref: target, format: 'tar.gz' }, silentCallOptions)
			]);
			zipUrl = zip.url;
			tarUrl = tar.url;
		} catch {
			zipUrl = '';
			tarUrl = '';
		}
	}

	onMount(loadArchives);
	$effect(() => {
		void ref;
		loadArchives();
	});
</script>

<div class="w-full space-y-3">
	<div class="flex items-center gap-1 border-b border-border/50">
		<button
			type="button"
			class="px-3 py-1.5 text-[13px] font-medium border-b-2 -mb-px transition-colors {tab === 'http' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
			onclick={() => (tab = 'http')}
		>
			HTTPS
		</button>
		{#if sshUrl}
			<button
				type="button"
				class="px-3 py-1.5 text-[13px] font-medium border-b-2 -mb-px transition-colors {tab === 'ssh' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'ssh')}
			>
				SSH
			</button>
		{/if}
	</div>

	<div class="space-y-2">
		<div class="rounded-lg bg-muted/30 border border-border/40 pl-3 pr-1 py-1.5 flex items-center gap-2">
			<code class="text-[12.5px] font-mono text-foreground/90 flex-1 min-w-0 truncate select-all">{url}</code>
			<CopyButton text={url} label="Clone URL copied!" />
		</div>
		<p class="text-[12px] text-muted-foreground px-0.5">
			{tab === 'ssh' ? 'Authenticates with an SSH key from your settings.' : 'Authenticates with your username and a token or password.'}
		</p>
	</div>

	<div class="rounded-lg bg-muted/30 border border-border/40 pl-3 pr-1 py-1.5 flex items-center gap-2">
		<Terminal class="h-3.5 w-3.5 text-muted-foreground/60 shrink-0" />
		<code class="text-[12.5px] font-mono text-muted-foreground flex-1 min-w-0 truncate select-all">dfcli git clone {cliRef}</code>
		<CopyButton text="dfcli git clone {cliRef}" label="Command copied!" />
	</div>

	{#if zipUrl || tarUrl}
		<div class="flex items-center gap-2 pt-1 border-t border-border/40">
			<Download class="h-3.5 w-3.5 text-muted-foreground/60" />
			<span class="text-[12px] text-muted-foreground">Download</span>
			{#if zipUrl}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a href={zipUrl} class="inline-flex items-center gap-1 text-[12px] font-medium text-primary hover:underline">
					<FileArchive class="h-3 w-3" />zip
				</a>
			{/if}
			{#if tarUrl}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a href={tarUrl} class="inline-flex items-center gap-1 text-[12px] font-medium text-primary hover:underline">
					<FileArchive class="h-3 w-3" />tar.gz
				</a>
			{/if}
		</div>
	{/if}
</div>
