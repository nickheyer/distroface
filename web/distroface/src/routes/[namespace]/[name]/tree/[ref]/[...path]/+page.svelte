<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { BookOpen, Code, ChevronDown, Plus, FilePlus, Upload } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import {
		DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import CloneBox from '$lib/components/git/clone-box.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import TreeTable from '$lib/components/git/tree-table.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import LatestCommitBar from '$lib/components/git/latest-commit-bar.svelte';
	import PathBreadcrumb from '$lib/components/git/path-breadcrumb.svelte';
	import ForkContributeBar from '$lib/components/git/fork-contribute-bar.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { GitTreeEntry, GitCommit, GitBlob } from '$lib/proto/distroface/v1/git_pb';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const ref = $derived(page.params.ref ?? '');
	const path = $derived((page.params.path ?? '').replace(/\/+$/, ''));
	const encodedRef = $derived(encodeURIComponent(ref));
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);

	let entries = $state<GitTreeEntry[]>([]);
	let lastCommit = $state<GitCommit | undefined>();
	let readme = $state<GitBlob | undefined>();
	let loading = $state(true);
	let missing = $state(false);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		missing = false;
		try {
			const [tree, rd] = await Promise.all([
				rpcClient.gitFiles.getTree({ namespace, name, ref, path, withLastCommit: true }, silentCallOptions),
				rpcClient.gitFiles.getReadme({ namespace, name, ref, path }, silentCallOptions).catch(() => undefined)
			]);
			if (mine !== seq) return;
			entries = tree.entries;
			lastCommit = tree.lastCommit;
			readme = rd?.blob;
		} catch {
			if (mine !== seq) return;
			entries = [];
			lastCommit = undefined;
			readme = undefined;
			missing = true;
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void ref;
		void path;
		void repo.id;
		load();
	});

	function onRef(next: string) {
		goto(resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(next), path }));
	}

	const readmeText = $derived(readme ? new TextDecoder().decode(readme.content) : '');
	const readmeBase = $derived(`/${namespace}/${name}/blob/${encodedRef}/${path ? `${path}/` : ''}`);
	const readmeRaw = $derived(`/${namespace}/${name}/-/raw/${encodedRef}/${path ? `${path}/` : ''}`);
</script>

<div class="space-y-4">
	<div class="flex items-center gap-3 flex-wrap">
		<RefPicker {namespace} {name} current={ref} defaultBranch={repo.defaultBranch} onSelect={onRef} />
		<PathBreadcrumb {namespace} {name} {ref} {path} />
		<div class="ml-auto flex items-center gap-2">
			<PermissionGate allowed={canWrite}>
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="outline" size="sm" class="h-8 gap-1.5"><Plus class="h-3.5 w-3.5" />Add file<ChevronDown class="h-3 w-3 opacity-70" /></Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						<DropdownMenuItem onclick={() => goto(resolve('/[namespace]/[name]/new/[ref]/[...path]', { namespace, name, ref: encodedRef, path }))}>
							<FilePlus class="h-4 w-4 mr-2" />Create new file
						</DropdownMenuItem>
						<DropdownMenuItem onclick={() => goto(resolve('/[namespace]/[name]/upload/[ref]/[...path]', { namespace, name, ref: encodedRef, path }))}>
							<Upload class="h-4 w-4 mr-2" />Upload files
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</PermissionGate>
			<Popover>
				<PopoverTrigger>
					{#snippet child({ props })}
						<Button {...props} size="sm" class="h-8 gap-1.5"><Code class="h-3.5 w-3.5" />Code<ChevronDown class="h-3 w-3 opacity-70" /></Button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-[min(24rem,calc(100vw-2rem))]" align="end">
					<CloneBox {repo} {ref} />
				</PopoverContent>
			</Popover>
		</div>
	</div>

	{#if missing}
		<EmptyState icon={Code} message="Path not found" description="{path || '/'} does not exist at {ref}." />
	{:else}
		{#if repo.forkOf && !path}
			<ForkContributeBar {repo} branch={ref} />
		{/if}
		<LatestCommitBar commit={lastCommit} {namespace} {name} {ref} {path} {loading} />
		<TreeTable {entries} {namespace} {name} {ref} {path} {loading} />

		{#if readme}
			<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
				<div class="flex items-center gap-2 px-4 py-2.5 border-b border-border/50 bg-muted/20">
					<BookOpen class="h-4 w-4 text-muted-foreground/70" />
					<a href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodedRef, path: readme.path })} class="text-[13px] font-medium hover:text-primary">{readme.name}</a>
				</div>
				<div class="px-6 py-5 min-w-0 overflow-x-auto">
					<Markdown source={readmeText} baseHref={readmeBase} rawBase={readmeRaw} />
				</div>
			</div>
		{/if}
	{/if}
</div>
