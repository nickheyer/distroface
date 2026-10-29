<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		Tag,
		Rocket,
		Pencil,
		Trash,
		Package,
		Container,
		GitCommit,
		FileArchive,
		Download,
		ChevronLeft,
		ExternalLink,
		Pin
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import FormField from '$lib/components/form-field.svelte';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Release, ReleaseImage } from '$lib/proto/distroface/v1/release_pb';
	import type { Platform } from '$lib/proto/distroface/v1/types_pb';
	import { ConnectError } from '@connectrpc/connect';
	import { formatBytes, relativeTime, truncateDigest } from '$lib/utils';
	import { primeDownload } from '$lib/releases';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const tag = $derived(decodeURIComponent(page.params.tag ?? ''));
	const repo = $derived(gitRepoStore.repo);
	const releasesHref = $derived(resolve('/[namespace]/[name]/releases', { namespace, name }));
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.RELEASES) && !repo?.isArchived);

	let release = $state<Release | undefined>();
	let latestTag = $state('');
	let zipUrl = $state('');
	let tarUrl = $state('');
	let loading = $state(true);
	let error = $state('');
	let loadedKey = $state('');
	let publishing = $state(false);
	let deleteOpen = $state(false);
	let deleteTag = $state(false);
	let deleting = $state(false);

	const isLatest = $derived(!!release && !!latestTag && release.tag === latestTag);
	const date = $derived.by(() => {
		const ts = release?.publishedAt ?? release?.createdAt;
		return ts ? timestampDate(ts) : null;
	});

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const key = `${namespace}/${name}@${tag}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void load();
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const [rel, latest] = await Promise.all([
				rpcClient.release.getRelease({ namespace, name, tag }, silentCallOptions),
				rpcClient.release
					.getLatestRelease({ namespace, name }, silentCallOptions)
					.catch(() => undefined)
			]);
			release = rel.release;
			latestTag = latest?.release?.tag ?? '';
			await loadArchives();
		} catch (err) {
			release = undefined;
			error =
				err instanceof ConnectError ? err.rawMessage || err.message : 'Failed to load release';
		} finally {
			loading = false;
		}
	}

	// Archive urls ship with the release, GetArchiveURL fills gaps
	async function loadArchives() {
		zipUrl = release?.zipUrl ?? '';
		tarUrl = release?.tarUrl ?? '';
		if (!release || (zipUrl && tarUrl)) return;
		try {
			const [zip, tar] = await Promise.all([
				rpcClient.gitFiles.getArchiveURL(
					{ namespace, name, ref: release.tag, format: 'zip' },
					silentCallOptions
				),
				rpcClient.gitFiles.getArchiveURL(
					{ namespace, name, ref: release.tag, format: 'tar.gz' },
					silentCallOptions
				)
			]);
			zipUrl ||= zip.url;
			tarUrl ||= tar.url;
		} catch {
			// Tag may not exist yet for drafts
		}
	}

	async function publish() {
		if (!release || publishing) return;
		publishing = true;
		try {
			const resp = await rpcClient.release.publishRelease({ namespace, name, tag: release.tag });
			if (resp.release) release = resp.release;
			toast.success('Release published');
			void gitRepoStore.reload();
			void loadArchives();
		} catch {
			// Error interceptor already toasted
		} finally {
			publishing = false;
		}
	}

	async function remove() {
		if (!release) return;
		deleting = true;
		try {
			await rpcClient.release.deleteRelease({ namespace, name, tag: release.tag, deleteTag });
			toast.success('Release deleted');
			deleteOpen = false;
			void gitRepoStore.reload();
			await goto(resolve('/[namespace]/[name]/releases', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function pullLine(img: ReleaseImage): string {
		return (
			img.pullCommand ||
			`docker pull ${configStore.publicHostname}/${portalStore.imageRef(img.namespace, img.name)}:${img.tag}`
		);
	}

	function platformLabel(platforms: Platform[]): string {
		return platforms
			.map((p) => [p.os, p.architecture, p.variant].filter(Boolean).join('/'))
			.join(', ');
	}

	function commitTitle(): string {
		return release?.commit?.message.split('\n')[0] ?? '';
	}
</script>

<svelte:head>
	<title>{release?.name || tag} - {namespace}/{name}</title>
</svelte:head>

{#if loading && !release}
	<div class="space-y-4">
		<Skeleton class="h-16 w-full rounded-xl" />
		<Skeleton class="h-48 w-full rounded-xl" />
		<Skeleton class="h-32 w-full rounded-xl" />
	</div>
{:else if !release}
	<EmptyState
		icon={Rocket}
		message="Release not found"
		description={error || `No release is tagged ${tag}.`}
	>
		{#snippet actions()}
			<Button variant="outline" href={releasesHref}>Back to releases</Button>
		{/snippet}
	</EmptyState>
{:else}
	<div class="space-y-5">
		<div class="flex min-w-0 flex-wrap items-start gap-3">
			<div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
				<Tag class="h-5 w-5 text-primary" />
			</div>
			<div class="min-w-0 flex-1">
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="truncate text-lg font-semibold tracking-tight">
						{release.name || release.tag}
					</h1>
					{#if isLatest}
						<Badge
							variant="outline"
							class="h-4.5 border-emerald-500/30 py-0 text-[10px] text-emerald-600 dark:text-emerald-400"
							>Latest</Badge
						>
					{/if}
					{#if release.draft}
						<Badge variant="outline" class="h-4.5 py-0 text-[10px] text-muted-foreground"
							>Draft</Badge
						>
					{/if}
					{#if release.prerelease}
						<Badge
							variant="outline"
							class="h-4.5 border-amber-500/30 py-0 text-[10px] text-amber-600 dark:text-amber-400"
							>Pre-release</Badge
						>
					{/if}
				</div>
				<p class="mt-1 flex flex-wrap items-center gap-2 text-[12.5px] text-muted-foreground">
					<span class="font-mono">{release.tag}</span>
					{#if release.author}
						<span
							>by <a
								href={resolve('/[namespace]', { namespace: release.author })}
								class="font-medium text-foreground/80 hover:text-primary">{release.author}</a
							></span
						>
					{/if}
					{#if date}
						<span title={date.toLocaleString()}
							>{release.draft ? 'saved' : 'published'} {relativeTime(date)}</span
						>
					{/if}
				</p>
			</div>
			<div class="flex flex-wrap items-center gap-2">
				<Button variant="outline" size="sm" class="h-8" href={releasesHref}>
					<ChevronLeft class="mr-1 h-3.5 w-3.5" />Releases
				</Button>
				<PermissionGate allowed={canWrite}>
					<Button
						variant="outline"
						size="sm"
						class="h-8"
						href={resolve('/[namespace]/[name]/releases/[tag]/edit', {
							namespace,
							name,
							tag: encodeURIComponent(release.tag)
						})}
					>
						<Pencil class="mr-1.5 h-3.5 w-3.5" />Edit
					</Button>
					{#if release.draft}
						<Button size="sm" class="h-8" onclick={publish} disabled={publishing}>
							<Rocket class="mr-1.5 h-3.5 w-3.5" />{publishing ? 'Publishing...' : 'Publish'}
						</Button>
					{/if}
					<Button
						variant="ghost"
						size="sm"
						class="h-8 px-2 text-destructive hover:text-destructive"
						onclick={() => (deleteOpen = true)}
						title="Delete release"
					>
						<Trash class="h-3.5 w-3.5" />
					</Button>
				</PermissionGate>
			</div>
		</div>

		{#if release.draft}
			<Alert>
				<Rocket class="h-4 w-4" />
				<AlertDescription
					>This draft is only visible to people who can write releases. Publish it to make it
					public.</AlertDescription
				>
			</Alert>
		{/if}

		{#if release.body.trim()}
			<div class="min-w-0 overflow-x-auto rounded-xl border border-border/60 bg-card px-5 py-4">
				<Markdown source={release.body} />
			</div>
		{/if}

		<div class="grid gap-4 lg:grid-cols-3">
			<section class="overflow-hidden rounded-xl border border-border/60 bg-card">
				<header class="flex items-center gap-2 border-b border-border/40 bg-muted/20 px-4 py-2.5">
					<GitCommit class="h-3.5 w-3.5 text-muted-foreground" />
					<h2 class="flex-1 text-[13px] font-semibold">Source</h2>
					<a
						href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
							namespace,
							name,
							ref: encodeURIComponent(release.tag),
							path: ''
						})}
						class="inline-flex items-center gap-1 text-[12px] text-muted-foreground hover:text-primary"
					>
						Browse<ExternalLink class="h-3 w-3" />
					</a>
				</header>
				<div class="space-y-3 px-4 py-3 text-[13px]">
					<div class="flex flex-wrap items-center gap-2">
						<Tag class="h-3.5 w-3.5 text-muted-foreground" />
						<a
							href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
								namespace,
								name,
								ref: encodeURIComponent(release.tag),
								path: ''
							})}
							class="font-mono font-medium hover:text-primary">{release.tag}</a
						>
					</div>
					{#if release.targetSha}
						<div class="flex min-w-0 items-center gap-2">
							<CommitBadge sha={release.targetSha} {namespace} {name} />
							{#if commitTitle()}
								<a
									href={resolve('/[namespace]/[name]/commit/[sha]', {
										namespace,
										name,
										sha: release.targetSha
									})}
									class="truncate text-[12.5px] text-muted-foreground hover:text-primary"
									title={commitTitle()}>{commitTitle()}</a
								>
							{/if}
						</div>
					{:else}
						<p class="text-[12.5px] text-muted-foreground">
							The tag is created when the release is published.
						</p>
					{/if}
					{#if zipUrl || tarUrl}
						<div class="flex flex-wrap items-center gap-3 border-t border-border/40 pt-2">
							{#if zipUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a
									href={zipUrl}
									class="inline-flex items-center gap-1 text-[12.5px] font-medium text-primary hover:underline"
								>
									<FileArchive class="h-3.5 w-3.5" />Source code (zip)
								</a>
							{/if}
							{#if tarUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a
									href={tarUrl}
									class="inline-flex items-center gap-1 text-[12.5px] font-medium text-primary hover:underline"
								>
									<FileArchive class="h-3.5 w-3.5" />Source code (tar.gz)
								</a>
							{/if}
						</div>
					{/if}
				</div>
			</section>

			<section class="overflow-hidden rounded-xl border border-border/60 bg-card">
				<header class="flex items-center gap-2 border-b border-border/40 bg-muted/20 px-4 py-2.5">
					<Package class="h-3.5 w-3.5 text-muted-foreground" />
					<h2 class="flex-1 text-[13px] font-semibold">
						Assets <span class="font-normal text-muted-foreground tabular-nums"
							>({release.assets.length})</span
						>
					</h2>
				</header>
				{#if release.assets.length === 0}
					<p class="px-4 py-3 text-[12.5px] text-muted-foreground">No files attached.</p>
				{:else}
					<ul class="divide-y divide-border/40">
						{#each release.assets as a (a.id)}
							<li class="min-w-0 px-4 py-2.5 text-[13px]">
								<div class="flex min-w-0 items-center gap-2">
									<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
									<a
										href={a.downloadUrl}
										download={a.name}
										onclick={() => primeDownload(namespace, name)}
										class="inline-flex min-w-0 flex-1 items-center gap-1.5 truncate font-medium text-primary hover:underline"
									>
										<Download class="h-3.5 w-3.5 shrink-0" />{a.label || a.name}
									</a>
									<span class="shrink-0 text-[12px] text-muted-foreground tabular-nums"
										>{formatBytes(Number(a.size))}</span
									>
								</div>
								<p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">
									{#if a.label}<span class="mr-1 font-mono">{a.name}</span>{/if}
									{#if a.digest}<span class="font-mono" title={a.digest}
											>{truncateDigest(a.digest, 20)}</span
										>{/if}
									{#if a.contentType}<span class="mx-1">{a.contentType}</span>{/if}
									<span class="tabular-nums"
										>{Number(a.downloadCount)} download{Number(a.downloadCount) === 1
											? ''
											: 's'}</span
									>
								</p>
							</li>
						{/each}
					</ul>
				{/if}
			</section>

			<section class="overflow-hidden rounded-xl border border-border/60 bg-card">
				<header class="flex items-center gap-2 border-b border-border/40 bg-muted/20 px-4 py-2.5">
					<Container class="h-3.5 w-3.5 text-muted-foreground" />
					<h2 class="flex-1 text-[13px] font-semibold">
						Images <span class="font-normal text-muted-foreground tabular-nums"
							>({release.images.length})</span
						>
					</h2>
					<a
						href={resolve('/images/[namespace]/[...name]', { namespace, name })}
						class="inline-flex items-center gap-1 text-[12px] text-muted-foreground hover:text-primary"
					>
						Images<ExternalLink class="h-3 w-3" />
					</a>
				</header>
				{#if release.images.length === 0}
					<p class="px-4 py-3 text-[12.5px] text-muted-foreground">
						No linked image carries the tag {release.tag}.
					</p>
				{:else}
					<ul class="divide-y divide-border/40">
						{#each release.images as img (`${img.fullName}:${img.tag}`)}
							{@const line = pullLine(img)}
							<li class="min-w-0 space-y-1.5 px-4 py-2.5 text-[13px]">
								<div class="flex min-w-0 flex-wrap items-center gap-2">
									<a
										href={resolve('/images/[namespace]/[...name]', {
											namespace: img.namespace,
											name: img.name
										})}
										class="truncate font-mono font-medium hover:text-primary"
										>{img.fullName}:{img.tag}</a
									>
									{#if img.pinned}
										<Badge variant="outline" class="h-4.5 gap-0.5 py-0 text-[10px]"
											><Pin class="h-2.5 w-2.5" />Pinned</Badge
										>
									{/if}
									{#if img.sizeBytes > 0n}
										<span class="ml-auto text-[12px] text-muted-foreground tabular-nums"
											>{formatBytes(Number(img.sizeBytes))}</span
										>
									{/if}
								</div>
								<p class="truncate text-[11.5px] text-muted-foreground">
									{#if img.digest}<span class="font-mono" title={img.digest}
											>{truncateDigest(img.digest, 20)}</span
										>{/if}
									{#if img.platforms.length > 0}<span class="mx-1">on</span>{platformLabel(
											img.platforms
										)}{/if}
								</p>
								<div
									class="flex min-w-0 items-center gap-2 rounded-md border border-border/40 bg-muted/30 py-1 pr-1 pl-2.5"
								>
									<code
										class="min-w-0 flex-1 truncate font-mono text-[12px] text-muted-foreground select-all"
										>{line}</code
									>
									<CopyButton text={line} label="Pull command copied!" />
								</div>
							</li>
						{/each}
					</ul>
				{/if}
			</section>
		</div>
	</div>
{/if}

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete release"
	confirmLabel="Delete"
	onConfirm={remove}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		<span class="block"
			>This removes the release <strong class="font-mono">{release?.tag}</strong> together with its asset
			files.</span
		>
		<span class="mt-3 block">
			<FormField
				label="Also delete the git tag"
				id="release-delete-tag"
				layout="toggle"
				help="Removes the tag from the repository too"
			>
				<Checkbox id="release-delete-tag" bind:checked={deleteTag} />
			</FormField>
		</span>
	{/snippet}
</ConfirmDialog>
