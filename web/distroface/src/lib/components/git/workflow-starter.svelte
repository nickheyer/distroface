<script lang="ts">
	import { resolve } from '$app/paths';
	import { Search, ArrowRight } from '@lucide/svelte';
	import { Input } from '$lib/components/ui/input';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import {
		workflowTemplates,
		templateCategories,
		blankTemplate,
		suggestTemplates,
		workflowDir,
		type WorkflowTemplate
	} from '$lib/components/git/workflow-templates';

	let {
		namespace,
		name,
		defaultBranch,
		canWrite = true
	}: {
		namespace: string;
		name: string;
		defaultBranch: string;
		canWrite?: boolean;
	} = $props();

	let query = $state('');
	let languages = $state<string[]>([]);
	let rootFiles = $state<string[]>([]);
	let detecting = $state(true);
	let detectedFor = $state('');

	$effect(() => {
		const key = `${namespace}/${name}`;
		if (key !== detectedFor) {
			detectedFor = key;
			void detect();
		}
	});

	async function detect() {
		detecting = true;
		const repo = gitRepoStore.repo;
		if (repo && repo.namespace === namespace && repo.name === name && repo.isEmpty) {
			languages = [];
			rootFiles = [];
			detecting = false;
			return;
		}
		const [langs, tree] = await Promise.allSettled([
			rpcClient.git.getRepositoryLanguages({ namespace, name }, silentCallOptions),
			rpcClient.gitFiles.getTree({ namespace, name }, silentCallOptions)
		]);
		languages = langs.status === 'fulfilled' ? langs.value.languages.map((l) => l.name) : [];
		rootFiles = tree.status === 'fulfilled' ? tree.value.entries.map((e) => e.name) : [];
		detecting = false;
	}

	const suggested = $derived(suggestTemplates(languages, rootFiles));
	const q = $derived(query.trim().toLowerCase());
	const matches = $derived(
		q
			? workflowTemplates.filter((t) =>
					`${t.name} ${t.description} ${t.category}`.toLowerCase().includes(q)
				)
			: []
	);

	// Query rides along, resolve() is applied where it renders
	function configureHref(t: WorkflowTemplate): `/${string}` {
		const editor = resolve('/[namespace]/[name]/new/[ref]/[...path]', {
			namespace,
			name,
			ref: encodeURIComponent(defaultBranch || 'main'),
			path: workflowDir
		});
		return `${editor}?filename=${encodeURIComponent(t.fileName)}&workflow_template=${encodeURIComponent(t.id)}` as `/${string}`;
	}

	function byCategory(category: string): WorkflowTemplate[] {
		return workflowTemplates.filter((t) => t.category === category);
	}
</script>

{#snippet card(t: WorkflowTemplate)}
	<div class="flex flex-col rounded-lg border bg-card p-4">
		<div class="flex items-start gap-3">
			<span
				class="flex size-9 shrink-0 items-center justify-center rounded-md text-xs font-bold text-white"
				style="background-color: {t.color}">{t.mark}</span
			>
			<div class="min-w-0 flex-1">
				<p class="truncate text-sm font-semibold">{t.name}</p>
				<p class="text-xs text-muted-foreground">By DistroFace</p>
			</div>
			{#if canWrite}
				<Button size="sm" variant="outline" class="shrink-0" href={resolve(configureHref(t))}>
					Configure
				</Button>
			{/if}
		</div>
		<p class="mt-3 line-clamp-3 text-sm text-muted-foreground">{t.description}</p>
	</div>
{/snippet}

{#snippet section(title: string, items: WorkflowTemplate[])}
	<section class="space-y-3">
		<h3 class="text-sm font-semibold">{title}</h3>
		<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
			{#each items as t (t.id)}
				{@render card(t)}
			{/each}
		</div>
	</section>
{/snippet}

<div class="space-y-8">
	<div class="space-y-3">
		<h2 class="text-xl font-semibold tracking-tight">Get started with Actions</h2>
		<p class="max-w-2xl text-sm text-muted-foreground">
			Build, test, and deploy your code. Make code reviews, branch management, and issue triaging
			work the way you want. Select a workflow to get started.
		</p>
		{#if canWrite}
			<a
				href={resolve(configureHref(blankTemplate))}
				class="link inline-flex items-center gap-1 text-sm"
			>
				Skip this and set up a workflow yourself <ArrowRight class="size-3.5" />
			</a>
		{/if}
		<div class="relative max-w-md">
			<Search class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
			<Input
				bind:value={query}
				placeholder="Search workflows"
				class="pl-8"
				aria-label="Search workflows"
			/>
		</div>
	</div>

	{#if q}
		{#if matches.length === 0}
			<p class="text-sm text-muted-foreground">No workflows match "{query}"</p>
		{:else}
			{@render section(`Results for "${query}"`, matches)}
		{/if}
	{:else}
		{#if detecting}
			<div class="space-y-3">
				<Skeleton class="h-5 w-56" />
				<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
					{#each { length: 3 }, i (i)}
						<Skeleton class="h-32 w-full rounded-lg" />
					{/each}
				</div>
			</div>
		{:else if suggested.length > 0}
			{@render section('Suggested for this repository', suggested)}
		{/if}
		{#each templateCategories as category (category)}
			{@render section(category, byCategory(category))}
		{/each}
	{/if}
</div>
