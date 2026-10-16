<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import { Pencil, Save, Eye, Blocks, GitMerge, HardDrive, X, Plus, Wrench } from '@lucide/svelte';
	import { formatBytes } from '$lib/utils';
	import { Visibility, GitRepoType } from '$lib/proto/distroface/v1/types_pb';
	import { GitMergeMethod, type GitRepository } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let description = $state('');
	let website = $state('');
	let topics = $state<string[]>([]);
	let topicDraft = $state('');
	let visibility = $state<Visibility>(Visibility.PUBLIC);
	let defaultBranch = $state('');
	let branches = $state<string[]>([]);
	let isTemplate = $state(false);

	let units = $state({
		issues: true, pulls: true, releases: true, wiki: true, projects: true, packages: true, actions: true,
		externalIssuesUrl: '', externalWikiUrl: '', blockOnDependencies: false, timeTracking: false
	});
	let merge = $state({
		allowMerge: true, allowSquash: true, allowRebase: true, allowRebaseMerge: true, allowFastForward: true,
		defaultMethod: GitMergeMethod.MERGE, deleteBranchOnMerge: false, allowForks: true
	});

	let savingDetails = $state(false);
	let savingAccess = $state(false);
	let savingUnits = $state(false);
	let savingMerge = $state(false);
	let maintaining = $state(false);
	let seededFor = $state('');

	type MergeFlag = 'allowMerge' | 'allowSquash' | 'allowRebase' | 'allowRebaseMerge' | 'allowFastForward';
	const mergeMethods: { value: GitMergeMethod; label: string; flag: MergeFlag }[] = [
		{ value: GitMergeMethod.MERGE, label: 'Merge commit', flag: 'allowMerge' },
		{ value: GitMergeMethod.SQUASH, label: 'Squash', flag: 'allowSquash' },
		{ value: GitMergeMethod.REBASE, label: 'Rebase', flag: 'allowRebase' },
		{ value: GitMergeMethod.REBASE_MERGE, label: 'Rebase then merge', flag: 'allowRebaseMerge' },
		{ value: GitMergeMethod.FAST_FORWARD, label: 'Fast forward only', flag: 'allowFastForward' }
	];
	const allowedMethods = $derived(mergeMethods.filter((m) => merge[m.flag]));
	const defaultMethodLabel = $derived(
		mergeMethods.find((m) => m.value === merge.defaultMethod)?.label ?? 'Select a method'
	);

	function seed(r: GitRepository) {
		description = r.description;
		website = r.website;
		topics = [...r.topics];
		visibility = r.visibility === Visibility.PRIVATE ? Visibility.PRIVATE : Visibility.PUBLIC;
		defaultBranch = r.defaultBranch;
		isTemplate = r.type === GitRepoType.TEMPLATE;
		if (r.units) {
			units = {
				issues: r.units.issues, pulls: r.units.pulls, releases: r.units.releases, wiki: r.units.wiki,
				projects: r.units.projects, packages: r.units.packages, actions: r.units.actions,
				externalIssuesUrl: r.units.externalIssuesUrl, externalWikiUrl: r.units.externalWikiUrl,
				blockOnDependencies: r.units.blockOnDependencies, timeTracking: r.units.timeTracking
			};
		}
		if (r.merge) {
			merge = {
				allowMerge: r.merge.allowMerge, allowSquash: r.merge.allowSquash, allowRebase: r.merge.allowRebase,
				allowRebaseMerge: r.merge.allowRebaseMerge, allowFastForward: r.merge.allowFastForward,
				defaultMethod: r.merge.defaultMethod || GitMergeMethod.MERGE,
				deleteBranchOnMerge: r.merge.deleteBranchOnMerge, allowForks: r.merge.allowForks
			};
		}
	}

	$effect(() => {
		if (repo && repo.fullName !== seededFor) {
			seededFor = repo.fullName;
			seed(repo);
			void loadBranches();
		}
	});

	async function loadBranches() {
		try {
			const resp = await rpcClient.gitRefs.listBranches(
				{ namespace, name, page: { pageSize: 200, orderBy: 'name' } },
				silentCallOptions
			);
			branches = resp.branches.map((b) => b.name);
		} catch {
			branches = [];
		}
	}

	function addTopic() {
		const t = topicDraft.trim().toLowerCase().replace(/\s+/g, '-');
		topicDraft = '';
		if (!t || topics.includes(t)) return;
		topics = [...topics, t];
	}

	async function saveDetails() {
		savingDetails = true;
		try {
			await rpcClient.git.updateGitRepository({
				namespace, name,
				description: description.trim(),
				website: website.trim(),
				isTemplate
			});
			await rpcClient.git.setTopics({ namespace, name, topics });
			toast.success('Details saved');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingDetails = false;
		}
	}

	async function saveAccess() {
		savingAccess = true;
		try {
			await rpcClient.git.updateGitRepository({ namespace, name, visibility, defaultBranch });
			toast.success('Saved');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingAccess = false;
		}
	}

	async function saveUnits() {
		savingUnits = true;
		try {
			await rpcClient.git.updateGitRepository({
				namespace, name,
				units: { ...units, externalIssuesUrl: units.externalIssuesUrl.trim(), externalWikiUrl: units.externalWikiUrl.trim() }
			});
			toast.success('Features saved');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingUnits = false;
		}
	}

	async function saveMerge() {
		if (allowedMethods.length === 0) {
			toast.error('Allow at least one merge method');
			return;
		}
		if (!allowedMethods.some((m) => m.value === merge.defaultMethod)) {
			merge.defaultMethod = allowedMethods[0].value;
		}
		savingMerge = true;
		try {
			await rpcClient.git.updateGitRepository({ namespace, name, merge: { ...merge } });
			toast.success('Merge settings saved');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingMerge = false;
		}
	}

	async function runMaintenance() {
		maintaining = true;
		try {
			const resp = await rpcClient.git.runMaintenance({ namespace, name });
			toast.success(`Maintenance done, ${formatBytes(Number(resp.sizeBytes))} on disk`);
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			maintaining = false;
		}
	}
</script>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-xl" />
{:else}
	<div class="space-y-4">
		<h2 class="section-title">General</h2>

		<FormCard title="Details" description="How the repository presents itself." icon={Pencil}>
			<div class="space-y-3">
				<FormField label="Description" id="repo-description">
					<Textarea id="repo-description" bind:value={description} rows={3} placeholder="What is this repository for?" />
				</FormField>
				<FormField label="Website" id="repo-website">
					<Input id="repo-website" bind:value={website} placeholder="https://example.com" />
				</FormField>
				<FormField label="Topics" id="repo-topic" help="Press enter to add, topics help people find the repository">
					<div class="space-y-2">
						{#if topics.length > 0}
							<div class="flex flex-wrap gap-1.5">
								{#each topics as topic (topic)}
									<Badge variant="secondary" class="gap-1 pr-1">
										{topic}
										<button
											type="button"
											class="rounded-full hover:bg-foreground/10 p-0.5"
											aria-label="Remove {topic}"
											onclick={() => (topics = topics.filter((t) => t !== topic))}
										>
											<X class="h-3 w-3" />
										</button>
									</Badge>
								{/each}
							</div>
						{/if}
						<div class="flex gap-2">
							<Input
								id="repo-topic"
								bind:value={topicDraft}
								placeholder="topic"
								class="max-w-xs"
								onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTopic(); } }}
							/>
							<Button variant="outline" size="sm" class="h-9" onclick={addTopic} disabled={!topicDraft.trim()}>
								<Plus class="h-3.5 w-3.5 mr-1" />Add
							</Button>
						</div>
					</div>
				</FormField>
				<FormField label="Template repository" horizontal help="Offer this repository as a starting point for new ones">
					<Switch bind:checked={isTemplate} />
				</FormField>
			</div>
			{#snippet footer()}
				<Button onclick={saveDetails} disabled={savingDetails} class="gap-2">
					<Save class="h-4 w-4" />
					{savingDetails ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>

		<FormCard title="Visibility and branches" description="Who can see the repository and where new clones land." icon={Eye}>
			<div class="space-y-3">
				<FormField label="Visibility" id="repo-visibility" help="Private repositories are visible to collaborators and teams only">
					<Select type="single" value={String(visibility)} onValueChange={(v) => { if (v) visibility = Number(v) as Visibility; }}>
						<SelectTrigger id="repo-visibility" class="w-48">
							{visibility === Visibility.PRIVATE ? 'Private' : 'Public'}
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={String(Visibility.PUBLIC)}>Public</SelectItem>
							<SelectItem value={String(Visibility.PRIVATE)}>Private</SelectItem>
						</SelectContent>
					</Select>
				</FormField>
				<FormField label="Default branch" id="repo-default-branch" help="Pull requests and clones target this branch">
					{#if branches.length > 0}
						<Select type="single" value={defaultBranch} onValueChange={(v) => { if (v) defaultBranch = v; }}>
							<SelectTrigger id="repo-default-branch" class="w-64 font-mono text-sm">{defaultBranch || 'Select a branch'}</SelectTrigger>
							<SelectContent>
								{#each branches as b (b)}
									<SelectItem value={b} class="font-mono text-sm">{b}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					{:else}
						<Input id="repo-default-branch" bind:value={defaultBranch} class="w-64 font-mono text-sm" placeholder="main" />
					{/if}
				</FormField>
			</div>
			{#snippet footer()}
				<Button onclick={saveAccess} disabled={savingAccess} class="gap-2">
					<Save class="h-4 w-4" />
					{savingAccess ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>

		<FormCard title="Features" description="Units shown in the tab bar, external urls replace the built in tracker or wiki." icon={Blocks}>
			<div class="space-y-3">
				<FormField label="Issues" horizontal>
					<Switch bind:checked={units.issues} />
				</FormField>
				{#if units.issues}
					<div class="pl-3 border-l-2 border-border/40 space-y-3">
						<FormField label="External issue tracker" id="repo-ext-issues" help="Leave empty to use the built in tracker">
							<Input id="repo-ext-issues" bind:value={units.externalIssuesUrl} placeholder="https://tracker.example.com/project" />
						</FormField>
						<FormField label="Block on dependencies" horizontal help="Blocked issues cannot close until their blockers close">
							<Switch bind:checked={units.blockOnDependencies} />
						</FormField>
						<FormField label="Time tracking" horizontal help="Shows time open and accepts manual time entries on issues">
							<Switch bind:checked={units.timeTracking} />
						</FormField>
					</div>
				{/if}
				<FormField label="Pull requests" horizontal>
					<Switch bind:checked={units.pulls} />
				</FormField>
				<FormField label="Releases" horizontal>
					<Switch bind:checked={units.releases} />
				</FormField>
				<FormField label="Wiki" horizontal>
					<Switch bind:checked={units.wiki} />
				</FormField>
				{#if units.wiki}
					<div class="pl-3 border-l-2 border-border/40">
						<FormField label="External wiki" id="repo-ext-wiki" help="Leave empty to use the built in wiki">
							<Input id="repo-ext-wiki" bind:value={units.externalWikiUrl} placeholder="https://wiki.example.com" />
						</FormField>
					</div>
				{/if}
				<FormField label="Projects" horizontal>
					<Switch bind:checked={units.projects} />
				</FormField>
				<FormField label="Packages" horizontal help="Linked images and artifacts">
					<Switch bind:checked={units.packages} />
				</FormField>
				<FormField label="Actions" horizontal help="Workflows under .distroface/workflows">
					<Switch bind:checked={units.actions} />
				</FormField>
			</div>
			{#snippet footer()}
				<Button onclick={saveUnits} disabled={savingUnits} class="gap-2">
					<Save class="h-4 w-4" />
					{savingUnits ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>

		<FormCard title="Merge settings" description="How pull requests land on their base branch." icon={GitMerge}>
			<div class="space-y-3">
				{#each mergeMethods as method (method.value)}
					<FormField label={method.label} horizontal>
						<Switch checked={merge[method.flag]} onCheckedChange={(v) => (merge[method.flag] = v)} />
					</FormField>
				{/each}
				<FormField label="Default method" id="repo-merge-default">
					<Select
						type="single"
						value={String(merge.defaultMethod)}
						onValueChange={(v) => { if (v) merge.defaultMethod = Number(v) as GitMergeMethod; }}
					>
						<SelectTrigger id="repo-merge-default" class="w-56">{defaultMethodLabel}</SelectTrigger>
						<SelectContent>
							{#each allowedMethods as method (method.value)}
								<SelectItem value={String(method.value)}>{method.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				<FormField label="Delete branch after merge" horizontal help="Head branches of merged pull requests are removed">
					<Switch bind:checked={merge.deleteBranchOnMerge} />
				</FormField>
				<FormField label="Allow forks" horizontal help="Anyone who can read may copy the repository into their namespace">
					<Switch bind:checked={merge.allowForks} />
				</FormField>
			</div>
			{#snippet footer()}
				<Button onclick={saveMerge} disabled={savingMerge} class="gap-2">
					<Save class="h-4 w-4" />
					{savingMerge ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>

		<FormCard title="Storage" description="Size on disk and housekeeping." icon={HardDrive}>
			<div class="grid grid-cols-2 gap-4 text-sm">
				<div>
					<p class="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">Objects</p>
					<p class="text-lg font-semibold tabular-nums mt-0.5">{formatBytes(Number(repo.sizeBytes))}</p>
				</div>
				<div>
					<p class="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">LFS</p>
					<p class="text-lg font-semibold tabular-nums mt-0.5">{formatBytes(Number(repo.lfsSizeBytes))}</p>
				</div>
			</div>
			{#snippet footer()}
				<Button variant="outline" onclick={runMaintenance} disabled={maintaining} class="gap-2">
					<Wrench class="h-4 w-4" />
					{maintaining ? 'Running...' : 'Run maintenance'}
				</Button>
			{/snippet}
		</FormCard>
	</div>
{/if}
