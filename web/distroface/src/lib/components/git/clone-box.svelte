<script lang="ts">
	import { onMount } from 'svelte';
	import { Terminal, Download, FileArchive } from '$lib/icons';
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

	const httpUrl = $derived(
		repo.httpCloneUrl || portalStore.httpCloneURL(repo.namespace, repo.name)
	);
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
				rpcClient.gitFiles.getArchiveURL(
					{ namespace: repo.namespace, name: repo.name, ref: target, format: 'zip' },
					silentCallOptions
				),
				rpcClient.gitFiles.getArchiveURL(
					{ namespace: repo.namespace, name: repo.name, ref: target, format: 'tar.gz' },
					silentCallOptions
				)
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
	<div class="flex items-center border-b">
		<button
			type="button"
			class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors {tab === 'http'
				? 'border-primary text-foreground'
				: 'border-transparent text-muted-foreground hover:text-foreground'}"
			onclick={() => (tab = 'http')}
		>
			HTTPS
		</button>
		{#if sshUrl}
			<button
				type="button"
				class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors {tab === 'ssh'
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'ssh')}
			>
				SSH
			</button>
		{/if}
	</div>

	<div class="space-y-2">
		<div class="flex items-center gap-2 rounded-md border bg-muted py-0.5 pr-0.5 pl-3">
			<code class="min-w-0 flex-1 truncate font-mono text-xs select-all">{url}</code>
			<CopyButton text={url} label="Clone URL copied" />
		</div>
		<p class="text-xs text-muted-foreground">
			{tab === 'ssh'
				? 'Authenticates with an SSH key from your settings'
				: 'Authenticates with your username and a token or password'}
		</p>
	</div>

	<div class="flex items-center gap-2 rounded-md border bg-muted py-0.5 pr-0.5 pl-3">
		<Terminal class="size-3.5 shrink-0 text-muted-foreground" />
		<code class="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground select-all"
			>dfcli git clone {cliRef}</code
		>
		<CopyButton text="dfcli git clone {cliRef}" label="Command copied" />
	</div>

	{#if zipUrl || tarUrl}
		<div class="flex items-center gap-2 border-t pt-2 text-xs text-muted-foreground">
			<Download class="size-3.5" />
			<span>Download</span>
			{#if zipUrl}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a href={zipUrl} class="link inline-flex items-center gap-1 font-medium">
					<FileArchive class="size-3" />zip
				</a>
			{/if}
			{#if tarUrl}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a href={tarUrl} class="link inline-flex items-center gap-1 font-medium">
					<FileArchive class="size-3" />tar.gz
				</a>
			{/if}
		</div>
	{/if}
</div>
