<script lang="ts">
	import { resolve } from '$app/paths';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Search, ArrowRight, AlertCircle, AlertTriangle, RotateCcw, Loader } from '$lib/icons';
	import { Input } from '$lib/components/ui/input';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { errText } from '$lib/act.svelte';
	import Octicon, { hasOcticon } from '$lib/components/git/octicon.svelte';
	import type { WorkflowTemplate } from '$lib/proto/distroface/v1/actions_pb';

	// Every new workflow lands here, the first directory the server reads
	const workflowDir = '.distroface/workflows';

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
	let templates = $state<WorkflowTemplate[]>([]);
	let source = $state('');
	let fetchedAt = $state<Date | undefined>(undefined);
	let refreshError = $state('');
	let loading = $state(true);
	let error = $state('');
	let languages = $state<string[]>([]);
	let rootFiles = $state<string[]>([]);
	let detecting = $state(true);
	let loadedFor = $state('');

	$effect(() => {
		const key = `${namespace}/${name}`;
		if (key !== loadedFor) {
			loadedFor = key;
			void load();
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const resp = await rpcClient.actions.listWorkflowTemplates(
				{ namespace, name },
				silentCallOptions
			);
			templates = resp.templates;
			source = resp.source;
			fetchedAt = resp.fetchedAt ? timestampDate(resp.fetchedAt) : undefined;
			refreshError = resp.refreshError;
		} catch (err) {
			error = errText(err);
		} finally {
			loading = false;
		}
		await detect();
	}

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

	// Languages rank by bytes, root files confirm a toolchain
	function suggest(max = 4): WorkflowTemplate[] {
		const lower = languages.map((l) => l.toLowerCase());
		const scored: Array<{ t: WorkflowTemplate; score: number }> = [];
		for (const t of templates) {
			let score = 0;
			const rank = Math.min(
				...t.categories.map((c) => {
					const i = lower.indexOf(c.toLowerCase());
					return i < 0 ? Infinity : i;
				})
			);
			if (rank !== Infinity) score += 10 - Math.min(rank, 5);
			if (
				t.filePatterns.some((p) => {
					try {
						const re = new RegExp(p);
						return rootFiles.some((f) => re.test(f));
					} catch {
						return false;
					}
				})
			)
				score += 8;
			if (score > 0) scored.push({ t, score });
		}
		return scored
			.sort((a, b) => b.score - a.score)
			.slice(0, max)
			.map((s) => s.t);
	}

	const suggested = $derived(templates.length ? suggest() : []);
	// Sections arrive in the order the server sorted them
	const categories = $derived.by(() => {
		const out: string[] = [];
		for (const t of templates) if (!out.includes(t.category)) out.push(t.category);
		return out;
	});
	const q = $derived(query.trim().toLowerCase());
	const matches = $derived(
		q
			? templates.filter((t) =>
					`${t.name} ${t.description} ${t.categories.join(' ')}`.toLowerCase().includes(q)
				)
			: []
	);
	const blank = $derived(templates.find((t) => t.id === 'ci/blank' || t.id.endsWith('/blank')));
	const sourceLabel = $derived(source.replace(/^https?:\/\//, ''));
	const fetchedLabel = $derived(fetchedAt ? fetchedAt.toLocaleString() : '');

	// Query rides along, resolve() is applied where it renders
	function configureHref(t?: WorkflowTemplate): `/${string}` {
		const editor = resolve('/[namespace]/[name]/new/[ref]/[...path]', {
			namespace,
			name,
			ref: encodeURIComponent(defaultBranch || 'main'),
			path: workflowDir
		});
		if (!t) return `${editor}?filename=${encodeURIComponent('workflow.yml')}` as `/${string}`;
		return `${editor}?filename=${encodeURIComponent(t.fileName)}&workflow_template=${encodeURIComponent(t.id)}` as `/${string}`;
	}

	function byCategory(category: string): WorkflowTemplate[] {
		return templates.filter((t) => t.category === category);
	}
</script>

{#snippet card(t: WorkflowTemplate)}
	<div class="flex flex-col rounded-lg border bg-card p-4">
		<div class="flex items-start gap-3">
			<span
				class="flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-md border bg-muted/40"
			>
				{#if t.iconName && hasOcticon(t.iconName)}
					<Octicon name={t.iconName} class="size-6" />
				{:else if t.iconDataUrl}
					<img src={t.iconDataUrl} alt="" class="size-6 object-contain" />
				{:else}
					<span class="text-xs font-bold text-muted-foreground">{t.name.slice(0, 2)}</span>
				{/if}
			</span>
			<div class="min-w-0 flex-1">
				<p class="truncate text-sm font-semibold">{t.name}</p>
				<p class="truncate text-xs text-muted-foreground" title={t.creator || source}>
					By {t.creator || sourceLabel}
				</p>
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
			<a href={resolve(configureHref(blank))} class="link inline-flex items-center gap-1 text-sm">
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
		{#if !loading && !error && refreshError}
			<p class="inline-flex items-start gap-2 text-xs text-muted-foreground" title={refreshError}>
				<AlertTriangle class="mt-0.5 size-3.5 shrink-0" />
				<span>Catalog from {fetchedLabel}, refresh failed: {refreshError}</span>
			</p>
		{/if}
	</div>

	{#if loading}
		<div class="space-y-3">
			<Skeleton class="h-5 w-56" />
			<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
				{#each { length: 6 }, i (i)}
					<Skeleton class="h-32 w-full rounded-lg" />
				{/each}
			</div>
		</div>
	{:else if error}
		<Alert variant="destructive">
			<AlertCircle />
			<AlertDescription class="flex flex-wrap items-center gap-3">
				<span class="min-w-0 flex-1">Starter workflows are unavailable: {error}</span>
				<Button size="sm" variant="outline" onclick={load}>
					<RotateCcw />Try again
				</Button>
			</AlertDescription>
		</Alert>
	{:else if q}
		{#if matches.length === 0}
			<p class="text-sm text-muted-foreground">No workflows match "{query}"</p>
		{:else}
			{@render section(`Results for "${query}"`, matches)}
		{/if}
	{:else}
		{#if detecting}
			<div class="inline-flex items-center gap-2 text-sm text-muted-foreground">
				<Loader class="size-3.5 animate-spin" />Looking at the repository
			</div>
		{:else if suggested.length > 0}
			{@render section('Suggested for this repository', suggested)}
		{/if}
		{#each categories as category (category)}
			{@render section(category, byCategory(category))}
		{/each}
	{/if}
</div>
