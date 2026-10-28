<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import MirrorConfigFields, {
		mirrorFormFrom,
		mirrorInit,
		type MirrorForm
	} from '$lib/components/mirror-config-fields.svelte';
	import { Save, RefreshCw, Square, Key, Info, Loader } from '$lib/icons';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const isMirror = $derived(repo?.type === GitRepoType.MIRROR);
	const syncing = $derived(
		!!repo && (repo.mirrorSyncing || mirrorSyncStore.syncing('git', repo.id))
	);

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

	// Finished syncs refresh the status card once
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		const ev = mirrorSyncStore.lastFinished;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
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
	<Skeleton class="h-60 w-full rounded-lg" />
{:else if !isMirror}
	<FormPage
		level="section"
		title="Mirror"
		description="Upstream sync for repositories created as mirrors."
	>
		<Alert>
			<Info />
			<AlertDescription>
				This is a {repo.type === GitRepoType.FORK ? 'fork' : 'standard repository'} and does not pull
				from an upstream. Mirrors are chosen when a repository is created. To publish this repository
				elsewhere, add a push mirror instead.
			</AlertDescription>
		</Alert>
	</FormPage>
{:else}
	<FormPage
		level="section"
		title="Mirror"
		description="Refs are fetched from the upstream on a schedule."
	>
		{#snippet actions()}
			{#if syncing}
				<Button variant="outline" onclick={stop} disabled={stopping}>
					{#if stopping}<Loader class="animate-spin" />{:else}<Square />{/if}
					{stopping ? 'Stopping' : 'Stop sync'}
				</Button>
			{:else}
				<Button variant="outline" onclick={syncNow} disabled={triggering}>
					<RefreshCw class={triggering ? 'animate-spin' : ''} />Sync now
				</Button>
			{/if}
		{/snippet}

		{#if syncing}
			<Alert>
				<RefreshCw class="animate-spin" />
				<AlertDescription>
					Sync in progress{mirrorSyncStore.message('git', repo.id)
						? `, ${mirrorSyncStore.message('git', repo.id)}`
						: ''}.
				</AlertDescription>
			</Alert>
		{/if}

		<FormSection title="Upstream" description="Where this mirror pulls from.">
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
				<Button onclick={save} disabled={saving || !form.upstream.trim()}>
					{#if saving}<Loader class="animate-spin" />{:else}<Save />{/if}
					Save changes
				</Button>
			{/snippet}
		</FormSection>

		<FormSection
			title="SSH key"
			description="For ssh upstreams, install the public key as a read only deploy key upstream."
		>
			{#if publicKey}
				<FormField
					label="Public key"
					id="mirror-public-key"
					help="Generating a new key replaces this one"
				>
					<div class="flex items-start gap-2">
						<pre
							id="mirror-public-key"
							class="code-inline min-w-0 flex-1 whitespace-pre-wrap">{publicKey}</pre>
						<CopyButton text={publicKey} label="Public key copied" />
					</div>
				</FormField>
			{:else}
				<p class="text-sm text-muted-foreground">
					No key yet. Generate one and add it to the upstream repository.
				</p>
			{/if}
			{#snippet footer()}
				<Button variant="outline" onclick={generateKey} disabled={generating}>
					{#if generating}<Loader class="animate-spin" />{:else}<Key />{/if}
					{generating ? 'Generating' : publicKey ? 'Regenerate key' : 'Generate key'}
				</Button>
			{/snippet}
		</FormSection>
	</FormPage>
{/if}
