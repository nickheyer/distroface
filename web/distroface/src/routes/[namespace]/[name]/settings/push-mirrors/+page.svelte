<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import UnitInput from '$lib/components/unit-input.svelte';
	import { Upload, Plus, Trash2, RefreshCw, AlertTriangle, CheckCircle, KeyRound } from '@lucide/svelte';
	import type { GitPushMirror } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let mirrors = $state<GitPushMirror[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	let createOpen = $state(false);
	let remoteUrl = $state('');
	let username = $state('');
	let authToken = $state('');
	let useSshKey = $state(false);
	let intervalMinutes = $state(60);
	let syncOnPush = $state(true);
	let creating = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<GitPushMirror | null>(null);
	let deleting = $state(false);
	let syncingId = $state<string | null>(null);

	const isSsh = $derived(/^(ssh:\/\/|git@|[\w.-]+@)/.test(remoteUrl.trim()));
	const busy = (m: GitPushMirror) => m.syncing || mirrorSyncStore.syncing('git_push', m.id);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	// Finished pushes refresh the list
	$effect(() => {
		const ev = mirrorSyncStore.lastFinished;
		void mirrorSyncStore.finishedSeq;
		if (ev && ev.kind === 'git_push' && mirrors.some((m) => m.id === ev.repoId)) void load();
	});

	async function load() {
		try {
			const resp = await rpcClient.git.listPushMirrors({ namespace, name }, silentCallOptions);
			mirrors = resp.mirrors;
		} catch {
			mirrors = [];
		} finally {
			loaded = true;
		}
	}

	function openCreate() {
		remoteUrl = '';
		username = '';
		authToken = '';
		useSshKey = false;
		intervalMinutes = 60;
		syncOnPush = true;
		createOpen = true;
	}

	async function create() {
		if (!remoteUrl.trim()) return;
		creating = true;
		try {
			await rpcClient.git.createPushMirror({
				namespace, name,
				remoteUrl: remoteUrl.trim(),
				username: username.trim(),
				authToken,
				useSshKey,
				intervalMinutes: Math.max(0, Math.trunc(Number(intervalMinutes) || 0)),
				syncOnPush
			});
			toast.success('Push mirror added');
			createOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	async function syncNow(m: GitPushMirror) {
		syncingId = m.id;
		try {
			await rpcClient.git.syncPushMirror({ namespace, name, id: m.id });
			toast.success('Push started');
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			syncingId = null;
		}
	}

	async function remove() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.git.deletePushMirror({ namespace, name, id: deleteTarget.id });
			toast.success('Push mirror removed');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Push mirrors</h2>
			<p class="section-subtitle">Every ref is pushed to these remotes on a schedule or after each push.</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openCreate} disabled={!repo}>
			<Plus class="h-4 w-4 mr-1.5" />Add Mirror
		</Button>
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if mirrors.length === 0}
		<EmptyState icon={Upload} message="No push mirrors" description="Keep a copy of this repository on another host.">
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openCreate}>
					<Plus class="h-4 w-4 mr-1.5" />Add Mirror
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden">
			{#each mirrors as m (m.id)}
				<div class="flex items-start gap-4 px-5 py-4">
					<Upload class="h-4 w-4 text-primary mt-0.5 shrink-0" />
					<div class="flex-1 min-w-0 space-y-1.5">
						<p class="font-mono text-sm truncate">{m.remoteUrl}</p>
						<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
							{#if busy(m)}
								<span class="flex items-center gap-1 text-amber-600 dark:text-amber-400"><RefreshCw class="h-3 w-3 animate-spin" />Pushing</span>
							{:else if m.lastError}
								<span class="flex items-center gap-1 text-destructive" title={m.lastError}><AlertTriangle class="h-3 w-3" />Last push failed</span>
							{:else if m.lastSync}
								<span class="flex items-center gap-1"><CheckCircle class="h-3 w-3 text-emerald-500" />Pushed {relativeTime(timestampDate(m.lastSync))}</span>
							{:else}
								<span>Never pushed</span>
							{/if}
							<span>every {m.intervalMinutes} min</span>
							{#if m.syncOnPush}<Badge variant="secondary" class="text-[10px] font-normal">On push</Badge>{/if}
							{#if m.useSshKey}<Badge variant="secondary" class="text-[10px] font-normal gap-1"><KeyRound class="h-2.5 w-2.5" />SSH key</Badge>{/if}
							{#if m.hasToken}<Badge variant="secondary" class="text-[10px] font-normal">Token</Badge>{/if}
						</div>
						{#if m.lastError && !busy(m)}
							<p class="text-xs text-destructive/80 break-all">{m.lastError}</p>
						{/if}
					</div>
					<div class="flex items-center gap-1 shrink-0">
						<Button variant="ghost" size="icon" class="h-7 w-7" aria-label="Push now" onclick={() => syncNow(m)} disabled={busy(m) || syncingId === m.id}>
							<RefreshCw class="h-3.5 w-3.5 {busy(m) || syncingId === m.id ? 'animate-spin' : ''}" />
						</Button>
						<Button
							variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive"
							aria-label="Remove mirror"
							onclick={() => { deleteTarget = m; deleteOpen = true; }}
						>
							<Trash2 class="h-3.5 w-3.5" />
						</Button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<FormPanel bind:open={createOpen} title="Add Push Mirror" description="Every branch and tag is force pushed and anything only on the remote is deleted. Credentials are stored encrypted and never shown again." icon={Upload}>
	<div class="space-y-3">
		<FormField label="Remote URL" id="pm-url" required help="https or ssh clone URL of the destination">
			<Input id="pm-url" bind:value={remoteUrl} placeholder="https://github.com/owner/repo.git" class="font-mono text-sm" autocomplete="off" />
		</FormField>
		{#if isSsh}
			<FormField label="Use repository SSH key" horizontal help="Generate it on the Mirror page and install it on the remote">
				<Switch bind:checked={useSshKey} />
			</FormField>
		{:else}
			<FormField label="Username" id="pm-user" help="Optional, some hosts accept the token alone">
				<Input id="pm-user" bind:value={username} autocomplete="off" data-1p-ignore />
			</FormField>
			<FormField label="Token or password" id="pm-token">
				<PasswordInput id="pm-token" bind:value={authToken} placeholder="Token" autocomplete="new-password" data-1p-ignore />
			</FormField>
		{/if}
		<FormField label="Interval" id="pm-interval" help="0 disables scheduled pushes">
			<UnitInput id="pm-interval" unit="min" min={0} bind:value={intervalMinutes} class="w-full sm:w-36" />
		</FormField>
		<FormField label="Push after every push" horizontal help="Mirror right after refs change here">
			<Switch bind:checked={syncOnPush} />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={create} disabled={creating || !remoteUrl.trim()}>{creating ? 'Adding...' : 'Add Mirror'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Remove Push Mirror" confirmLabel="Remove" onConfirm={remove} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Pushes to <strong class="font-mono break-all">{deleteTarget?.remoteUrl}</strong> stop. The remote keeps what it has.
	{/snippet}
</ConfirmDialog>
