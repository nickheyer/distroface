<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import MirrorConfigFields, { mirrorFormFrom, mirrorInit, type MirrorForm } from '$lib/components/mirror-config-fields.svelte';
	import { HardDriveDownload, Save, RefreshCw, Square, KeyRound, Info } from '@lucide/svelte';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const isMirror = $derived(repo?.type === GitRepoType.MIRROR);
	const syncing = $derived(!!repo && (repo.mirrorSyncing || mirrorSyncStore.syncing('git', repo.id)));

	let form = $state<MirrorForm>(mirrorFormFrom());
	let seededFor = $state('');
	let saving = $state(false);
	let triggering = $state(false);
	let stopping = $state(false);
	let generating = $state(false);
	let publicKey = $state('');

	$effect(() => {
		if (repo && repo.fullName !== seededFor) {
			seededFor = repo.fullName;
			form = mirrorFormFrom(repo.mirror);
			publicKey = repo.mirrorSshPublicKey;
		}
	});

	// Finished syncs refresh the status card
	$effect(() => {
		const ev = mirrorSyncStore.lastFinished;
		void mirrorSyncStore.finishedSeq;
		if (ev && repo && ev.kind === 'git' && ev.repoId === repo.id) void gitRepoStore.reload();
	});

	async function save() {
		saving = true;
		try {
			await rpcClient.git.updateGitRepository({ namespace, name, mirror: mirrorInit(form) });
			toast.success('Mirror settings saved');
			await gitRepoStore.reload();
			if (repo) form = mirrorFormFrom(repo.mirror);
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	async function syncNow() {
		triggering = true;
		try {
			await rpcClient.git.syncGitRepository({ namespace, name });
			toast.success('Sync started');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			triggering = false;
		}
	}

	async function stop() {
		stopping = true;
		try {
			await rpcClient.git.stopGitRepositorySync({ namespace, name });
			toast.success('Sync stop requested');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			stopping = false;
		}
	}

	async function generateKey() {
		generating = true;
		try {
			const resp = await rpcClient.git.generateMirrorKey({ namespace, name });
			publicKey = resp.publicKey;
			toast.success('SSH key generated');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			generating = false;
		}
	}
</script>

{#if !repo}
	<Skeleton class="h-60 w-full rounded-xl" />
{:else if !isMirror}
	<div class="space-y-4">
		<h2 class="section-title">Mirror</h2>
		<Alert>
			<Info class="h-4 w-4" />
			<AlertDescription>
				This is a {repo.type === GitRepoType.FORK ? 'fork' : 'standard repository'} and does not pull from an upstream.
				Mirrors are chosen when a repository is created. To publish this repository elsewhere, add a push mirror instead.
			</AlertDescription>
		</Alert>
	</div>
{:else}
	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0">
				<h2 class="section-title">Mirror</h2>
				<p class="section-subtitle">Refs are fetched from the upstream on a schedule.</p>
			</div>
			<div class="flex items-center gap-2 shrink-0">
				{#if syncing}
					<Button variant="outline" size="sm" class="h-8" onclick={stop} disabled={stopping}>
						<Square class="h-3.5 w-3.5 mr-1.5" />{stopping ? 'Stopping...' : 'Stop Sync'}
					</Button>
				{:else}
					<Button variant="outline" size="sm" class="h-8" onclick={syncNow} disabled={triggering}>
						<RefreshCw class="h-3.5 w-3.5 mr-1.5 {triggering ? 'animate-spin' : ''}" />Sync Now
					</Button>
				{/if}
			</div>
		</div>

		{#if syncing}
			<Alert>
				<RefreshCw class="h-4 w-4 animate-spin" />
				<AlertDescription>
					Sync in progress{mirrorSyncStore.message('git', repo.id) ? `, ${mirrorSyncStore.message('git', repo.id)}` : ''}.
				</AlertDescription>
			</Alert>
		{/if}

		<FormCard title="Upstream" description="Where this mirror pulls from." icon={HardDriveDownload}>
			<MirrorConfigFields
				{form}
				kind="git"
				tokenSet={repo.mirror?.authTokenSet ?? false}
				lastSync={repo.mirrorLastSync}
				lastError={repo.mirrorLastError}
				nextAttempt={repo.mirrorNextAttempt}
				idPrefix="git-mirror"
			/>
			{#snippet footer()}
				<Button onclick={save} disabled={saving || !form.upstream.trim()} class="gap-2">
					<Save class="h-4 w-4" />
					{saving ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>

		<FormCard title="SSH key" description="For ssh upstreams, install the public key as a read only deploy key upstream." icon={KeyRound}>
			<div class="space-y-3">
				{#if publicKey}
					<FormField label="Public key" id="mirror-public-key" help="Generating a new key replaces this one">
						<div class="flex items-start gap-2">
							<pre id="mirror-public-key" class="code-inline flex-1 min-w-0 text-xs whitespace-pre-wrap break-all">{publicKey}</pre>
							<CopyButton text={publicKey} label="Public key copied!" />
						</div>
					</FormField>
				{:else}
					<p class="text-sm text-muted-foreground">No key yet. Generate one and add it to the upstream repository.</p>
				{/if}
			</div>
			{#snippet footer()}
				<Button variant="outline" onclick={generateKey} disabled={generating} class="gap-2">
					<KeyRound class="h-4 w-4" />
					{generating ? 'Generating...' : publicKey ? 'Regenerate Key' : 'Generate Key'}
				</Button>
			{/snippet}
		</FormCard>
	</div>
{/if}
