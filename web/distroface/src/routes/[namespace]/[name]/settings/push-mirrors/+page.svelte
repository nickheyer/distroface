<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import {
		Upload,
		Plus,
		Trash2,
		RefreshCw,
		AlertTriangle,
		CheckCircle,
		KeyRound
	} from '@lucide/svelte';
	import type { GitPushMirror } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const newHref = $derived(
		resolve('/[namespace]/[name]/settings/push-mirrors/new', { namespace, name })
	);

	let mirrors = $state<GitPushMirror[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	let deleteOpen = $state(false);
	let deleteTarget = $state<GitPushMirror | null>(null);
	let deleting = $state(false);
	let syncingId = $state<string | null>(null);

	const busy = (m: GitPushMirror) => m.syncing || mirrorSyncStore.syncing('git_push', m.id);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	// Finished pushes refresh the list once
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		const ev = mirrorSyncStore.lastFinished;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
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

<div class="space-y-6">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Push mirrors</h2>
			<p class="section-subtitle">
				Every ref is pushed to these remotes on a schedule or after each push.
			</p>
		</div>
		<Button class="shrink-0" href={newHref} disabled={!repo}>
			<Plus />Add mirror
		</Button>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 2 }, i (i)}
				<div class="space-y-2 px-4 py-3">
					<Skeleton class="h-4 w-64" />
					<Skeleton class="h-3 w-40" />
				</div>
			{/each}
		</div>
	{:else if mirrors.length === 0}
		<EmptyState
			icon={Upload}
			message="No push mirrors"
			description="Keep a copy of this repository on another host."
		>
			{#snippet actions()}
				<Button variant="outline" href={newHref}>
					<Plus />Add mirror
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y">
			{#each mirrors as m (m.id)}
				<div class="flex items-start gap-3 px-4 py-3">
					<Upload class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-0 flex-1 space-y-1.5">
						<p class="truncate font-mono text-sm">{m.remoteUrl}</p>
						<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
							{#if busy(m)}
								<span class="flex items-center gap-1 text-warning"
									><RefreshCw class="size-3 animate-spin" />Pushing</span
								>
							{:else if m.lastError}
								<span class="flex items-center gap-1 text-destructive" title={m.lastError}
									><AlertTriangle class="size-3" />Last push failed</span
								>
							{:else if m.lastSync}
								<span class="flex items-center gap-1"
									><CheckCircle class="size-3 text-success" />Pushed {relativeTime(
										timestampDate(m.lastSync)
									)}</span
								>
							{:else}
								<span>Never pushed</span>
							{/if}
							<span>every {m.intervalMinutes} min</span>
							{#if m.syncOnPush}<Badge variant="secondary">On push</Badge>{/if}
							{#if m.useSshKey}<Badge variant="secondary"><KeyRound />SSH key</Badge>{/if}
							{#if m.hasToken}<Badge variant="secondary">Token</Badge>{/if}
						</div>
						{#if m.lastError && !busy(m)}
							<p class="text-xs break-all text-destructive">{m.lastError}</p>
						{/if}
					</div>
					<div class="flex shrink-0 items-center gap-1">
						<Button
							variant="ghost-muted"
							size="icon-sm"
							aria-label="Push now"
							onclick={() => syncNow(m)}
							disabled={busy(m) || syncingId === m.id}
						>
							<RefreshCw class={busy(m) || syncingId === m.id ? 'animate-spin' : ''} />
						</Button>
						<Button
							variant="ghost-muted"
							size="icon-sm"
							class="text-destructive hover:text-destructive"
							aria-label="Remove mirror"
							onclick={() => {
								deleteTarget = m;
								deleteOpen = true;
							}}
						>
							<Trash2 />
						</Button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Remove push mirror"
	confirmLabel="Remove"
	onConfirm={remove}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Pushes to <strong class="font-mono break-all">{deleteTarget?.remoteUrl}</strong> stop. The remote
		keeps what it has.
	{/snippet}
</ConfirmDialog>
