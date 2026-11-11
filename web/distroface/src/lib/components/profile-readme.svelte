<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { BookOpen, Loader, Pencil, Plus } from '$lib/icons';
	import type { GitBlob, GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import { Visibility } from '$lib/proto/distroface/v1/types_pb';

	// A repository named after the namespace lends its readme to the profile
	let {
		namespace,
		isOwn = false,
		ownerLabel = 'profile'
	}: { namespace: string; isOwn?: boolean; ownerLabel?: string } = $props();

	let readme = $state<GitBlob | undefined>();
	let loaded = $state(false);
	let loadedFor = '';
	// Failed lookup means the repository itself is absent
	let missing = $state(false);

	// Owners only, carries the branch the editor commits to
	let repo = $state<GitRepository | undefined>();
	let repoFor = '';
	let creating = $state(false);

	$effect(() => {
		if (!namespace || namespace === loadedFor) return;
		loadedFor = namespace;
		loaded = false;
		readme = undefined;
		repo = undefined;
		repoFor = '';
		rpcClient.gitFiles
			.getReadme({ namespace, name: namespace, ref: '', path: '' }, backgroundCallOptions)
			.then((r) => {
				readme = r.blob;
				missing = false;
			})
			.catch(() => {
				readme = undefined;
				missing = true;
			})
			.finally(() => (loaded = true));
	});

	$effect(() => {
		if (!isOwn || !loaded || missing || namespace === repoFor) return;
		repoFor = namespace;
		void findRepo(namespace);
	});

	async function findRepo(ns: string): Promise<GitRepository | undefined> {
		const found = await rpcClient.git
			.getGitRepository({ namespace: ns, name: ns }, backgroundCallOptions)
			.then((r) => r.repository)
			.catch(() => undefined);
		if (ns === namespace) repo = found;
		return found;
	}

	function editHref(target: GitRepository, path: string): string {
		return resolve('/[namespace]/[name]/edit/[ref]/[...path]', {
			namespace: target.namespace,
			name: target.name,
			ref: encodeURIComponent(target.defaultBranch),
			path
		});
	}

	// Creates the repository if missing, then opens the editor
	async function start() {
		if (creating) return;
		creating = true;
		try {
			let target = missing ? undefined : (repo ?? (await findRepo(namespace)));
			let fresh = false;
			if (!target) {
				const resp = await rpcClient.git.createGitRepository({
					namespace,
					name: namespace,
					visibility: Visibility.PUBLIC,
					autoInit: true
				});
				target = resp.repository;
				fresh = true;
			}
			if (!target) return;
			repo = target;
			missing = false;
			if (fresh && !target.isEmpty) {
				await goto(resolve(editHref(target, 'README.md') as `/${string}`));
				return;
			}
			// Existing repository without a readme starts from a blank file
			const blank = resolve('/[namespace]/[name]/new/[ref]/[...path]', {
				namespace: target.namespace,
				name: target.name,
				ref: encodeURIComponent(target.defaultBranch),
				path: ''
			});
			await goto(resolve(`${blank}?filename=README.md` as `/${string}`));
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	const text = $derived(readme ? new TextDecoder().decode(readme.content) : '');
	const dir = $derived(
		readme?.path.includes('/') ? readme.path.slice(0, readme.path.lastIndexOf('/')) : ''
	);
	const baseHref = $derived(`/${namespace}/${namespace}/blob/HEAD/${dir ? `${dir}/` : ''}`);
	const rawBase = $derived(`/${namespace}/${namespace}/-/raw/HEAD/${dir ? `${dir}/` : ''}`);
	const repoHref = $derived(resolve('/[namespace]/[name]', { namespace, name: namespace }));
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
{#if !loaded}
	<Skeleton class="h-40 w-full rounded-lg" />
{:else if readme}
	<div class="box">
		<div class="box-header">
			<a href={repoHref} class="flex min-w-0 items-center gap-2 hover:text-primary">
				<BookOpen class="size-4 text-muted-foreground" />
				<span class="truncate">{namespace}/{namespace}</span>
				<span class="text-xs font-normal text-muted-foreground">/ {readme.name}</span>
			</a>
			{#if isOwn && repo}
				<Button
					variant="ghost-muted"
					size="icon-sm"
					class="-my-1 shrink-0"
					href={editHref(repo, readme.path)}
					aria-label="Edit README"
					title="Edit README"
				>
					<Pencil />
				</Button>
			{/if}
		</div>
		<div class="overflow-x-auto px-4 py-4 sm:px-6 sm:py-5">
			<Markdown
				source={text}
				{baseHref}
				{rawBase}
				repo={`${namespace}/${namespace}`}
				class="markdown-readme"
			/>
		</div>
	</div>
{:else if isOwn}
	<div
		class="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-lg border border-dashed bg-card px-5 py-4"
	>
		<BookOpen class="size-5 shrink-0 text-muted-foreground" />
		<div class="min-w-0 flex-1 basis-64 text-sm">
			<p class="font-medium">Add a README to this {ownerLabel}</p>
			<p class="mt-0.5 text-muted-foreground">
				It shows here for every visitor and lives in the public repository
				<span class="font-mono text-xs text-foreground">{namespace}/{namespace}</span>
			</p>
		</div>
		<Button variant="outline" class="shrink-0" onclick={start} disabled={creating}>
			{#if creating}
				<Loader class="animate-spin" />
			{:else}
				<Plus />
			{/if}
			{creating ? 'Creating' : 'Create README'}
		</Button>
	</div>
{/if}
<!-- eslint-enable svelte/no-navigation-without-resolve -->
