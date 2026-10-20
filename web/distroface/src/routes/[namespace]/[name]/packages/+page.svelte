<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { Package, Archive, Link2, Unlink, Download, Lock } from '@lucide/svelte';
	import { LinkedRepositoryKind, type LinkedRepository } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let linked = $state<LinkedRepository[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	let unlinkOpen = $state(false);
	let unlinkTarget = $state<LinkedRepository | null>(null);
	let unlinking = $state(false);

	const images = $derived(linked.filter((l) => l.kind === LinkedRepositoryKind.IMAGE));
	const artifacts = $derived(linked.filter((l) => l.kind === LinkedRepositoryKind.ARTIFACT));

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	async function load() {
		try {
			const resp = await rpcClient.git.listLinkedRepositories({ namespace, name }, silentCallOptions);
			linked = resp.repositories;
		} catch {
			linked = [];
		} finally {
			loaded = true;
		}
	}

	function linkHref(kind: LinkedRepositoryKind): string {
		const base = resolve('/[namespace]/[name]/packages/link', { namespace, name });
		return `${base}?kind=${kind === LinkedRepositoryKind.IMAGE ? 'image' : 'artifact'}`;
	}

	async function unlink() {
		if (!unlinkTarget) return;
		unlinking = true;
		try {
			await rpcClient.git.unlinkRepository({ namespace, name, kind: unlinkTarget.kind, packageName: unlinkTarget.name });
			toast.success('Package unlinked');
			unlinkOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			unlinking = false;
		}
	}

</script>

<svelte:head>
	<title>Packages - {namespace}/{name}</title>
</svelte:head>

{#snippet rows(list: LinkedRepository[], kind: LinkedRepositoryKind)}
	<div class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden">
		{#each list as l (l.fullName)}
			<div class="flex items-center gap-4 px-5 py-3.5">
				{#if kind === LinkedRepositoryKind.IMAGE}
					<Package class="h-4 w-4 text-primary shrink-0" />
				{:else}
					<Archive class="h-4 w-4 text-primary shrink-0" />
				{/if}
				<div class="flex-1 min-w-0">
					<div class="flex items-center gap-2 min-w-0">
						{#if kind === LinkedRepositoryKind.IMAGE}
							<a href={resolve('/images/[namespace]/[...name]', { namespace: l.namespace, name: l.name })} class="text-sm font-medium truncate hover:text-primary transition-colors">{l.fullName}</a>
						{:else}
							<a href={resolve('/artifacts/[namespace]/[repo]', { namespace: l.namespace, repo: l.name })} class="text-sm font-medium truncate hover:text-primary transition-colors">{l.fullName}</a>
						{/if}
						{#if l.isPrivate}
							<Lock class="h-3 w-3 text-muted-foreground shrink-0" />
						{/if}
						{#if l.linkedByName}
							<Badge variant="outline" class="text-[10px] font-normal shrink-0">By name</Badge>
						{/if}
					</div>
					<div class="flex items-center gap-3 text-xs text-muted-foreground mt-0.5 flex-wrap">
						{#if l.latest}
							<span class="font-mono">{kind === LinkedRepositoryKind.IMAGE ? 'latest tag' : 'latest'} {l.latest}</span>
						{:else}
							<span>Nothing published yet</span>
						{/if}
						{#if kind === LinkedRepositoryKind.IMAGE}
							<span class="flex items-center gap-1 tabular-nums"><Download class="h-3 w-3" />{Number(l.pullCount).toLocaleString()}</span>
						{/if}
						{#if l.updatedAt}
							<span>updated {relativeTime(timestampDate(l.updatedAt))}</span>
						{/if}
					</div>
				</div>
				<PermissionGate allowed={gitRepoStore.canAdmin}>
					<Button variant="ghost" size="icon" class="h-7 w-7 text-muted-foreground hover:text-destructive" aria-label="Unlink {l.fullName}" onclick={() => { unlinkTarget = l; unlinkOpen = true; }}>
						<Unlink class="h-3.5 w-3.5" />
					</Button>
				</PermissionGate>
			</div>
		{/each}
	</div>
{/snippet}

<div class="space-y-6">
	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if linked.length === 0}
		<EmptyState
			icon={Package}
			message="No linked packages"
			description="Images and artifacts named {namespace}/{name} link automatically. Admins can link other packages in this namespace."
		>
			{#snippet actions()}
				<PermissionGate allowed={gitRepoStore.canAdmin}>
					<div class="flex gap-2">
						<Button variant="outline" size="sm" href={linkHref(LinkedRepositoryKind.IMAGE)}><Link2 class="h-4 w-4 mr-1.5" />Link Image</Button>
						<Button variant="outline" size="sm" href={linkHref(LinkedRepositoryKind.ARTIFACT)}><Link2 class="h-4 w-4 mr-1.5" />Link Artifact</Button>
					</div>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<section class="space-y-3">
			<div class="section-header">
				<div>
					<h2 class="section-title">Images</h2>
					<p class="section-subtitle">{images.length} container image repositor{images.length === 1 ? 'y' : 'ies'}</p>
				</div>
				<PermissionGate allowed={gitRepoStore.canAdmin}>
					<Button variant="outline" size="sm" href={linkHref(LinkedRepositoryKind.IMAGE)}><Link2 class="h-4 w-4 mr-1.5" />Link Image</Button>
				</PermissionGate>
			</div>
			{#if images.length > 0}
				{@render rows(images, LinkedRepositoryKind.IMAGE)}
			{:else}
				<p class="text-sm text-muted-foreground">No linked images.</p>
			{/if}
		</section>

		<section class="space-y-3">
			<div class="section-header">
				<div>
					<h2 class="section-title">Artifacts</h2>
					<p class="section-subtitle">{artifacts.length} artifact repositor{artifacts.length === 1 ? 'y' : 'ies'}</p>
				</div>
				<PermissionGate allowed={gitRepoStore.canAdmin}>
					<Button variant="outline" size="sm" href={linkHref(LinkedRepositoryKind.ARTIFACT)}><Link2 class="h-4 w-4 mr-1.5" />Link Artifact</Button>
				</PermissionGate>
			</div>
			{#if artifacts.length > 0}
				{@render rows(artifacts, LinkedRepositoryKind.ARTIFACT)}
			{:else}
				<p class="text-sm text-muted-foreground">No linked artifacts.</p>
			{/if}
		</section>
	{/if}
</div>

<ConfirmDialog bind:open={unlinkOpen} title="Unlink package" confirmLabel="Unlink" onConfirm={unlink} loading={unlinking} icon={Unlink}>
	{#snippet description()}
		<strong>{unlinkTarget?.fullName}</strong> keeps its contents but stops sharing access and releases with this repository.
	{/snippet}
</ConfirmDialog>
