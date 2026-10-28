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
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import StatCard from '$lib/components/stat-card.svelte';
	import { Save, X, Plus, Wrench, Loader } from '$lib/icons';
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
		issues: true,
		pulls: true,
		releases: true,
		wiki: true,
		projects: true,
		packages: true,
		actions: true,
		agents: false,
		externalIssuesUrl: '',
		externalWikiUrl: '',
		blockOnDependencies: false,
		timeTracking: false
	});
	let merge = $state({
		allowMerge: true,
		allowSquash: true,
		allowRebase: true,
		allowRebaseMerge: true,
		allowFastForward: true,
		defaultMethod: GitMergeMethod.MERGE,
		deleteBranchOnMerge: false,
		allowForks: true
	});

	let savingDetails = $state(false);
	let savingAccess = $state(false);
	let savingUnits = $state(false);
	let savingMerge = $state(false);
	let maintaining = $state(false);
	let seededFor = $state('');

	type MergeFlag =
		| 'allowMerge'
		| 'allowSquash'
		| 'allowRebase'
		| 'allowRebaseMerge'
		| 'allowFastForward';
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
				issues: r.units.issues,
				pulls: r.units.pulls,
				releases: r.units.releases,
				wiki: r.units.wiki,
				projects: r.units.projects,
				packages: r.units.packages,
				actions: r.units.actions,
				agents: r.units.agents,
				externalIssuesUrl: r.units.externalIssuesUrl,
				externalWikiUrl: r.units.externalWikiUrl,
				blockOnDependencies: r.units.blockOnDependencies,
				timeTracking: r.units.timeTracking
			};
		}
		if (r.merge) {
			merge = {
				allowMerge: r.merge.allowMerge,
				allowSquash: r.merge.allowSquash,
				allowRebase: r.merge.allowRebase,
				allowRebaseMerge: r.merge.allowRebaseMerge,
				allowFastForward: r.merge.allowFastForward,
				defaultMethod: r.merge.defaultMethod || GitMergeMethod.MERGE,
				deleteBranchOnMerge: r.merge.deleteBranchOnMerge,
				allowForks: r.merge.allowForks
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
				namespace,
				name,
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
				namespace,
				name,
				units: {
					...units,
					externalIssuesUrl: units.externalIssuesUrl.trim(),
					externalWikiUrl: units.externalWikiUrl.trim()
				}
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

{#snippet saveButton(saving: boolean, onclick: () => void)}
	<Button {onclick} disabled={saving}>
		{#if saving}
			<Loader class="animate-spin" />
		{:else}
			<Save />
		{/if}
		Save changes
	</Button>
{/snippet}

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<FormPage
		level="section"
		title="General"
		description="Details, visibility, features, and merge rules for this repository."
	>
		<FormSection title="Details" description="How the repository presents itself.">
			<FormField label="Description" id="repo-description">
				<Textarea
					id="repo-description"
					bind:value={description}
					rows={3}
					placeholder="What is this repository for?"
				/>
			</FormField>
			<FormField label="Website" id="repo-website" span="half">
				<Input id="repo-website" bind:value={website} placeholder="https://example.com" />
			</FormField>
			<FormField label="Topics" id="repo-topic" help="Press enter to add" span="half">
				<div class="space-y-2">
					<div class="flex gap-2">
						<Input
							id="repo-topic"
							bind:value={topicDraft}
							placeholder="topic"
							onkeydown={(e) => {
								if (e.key === 'Enter') {
									e.preventDefault();
									addTopic();
								}
							}}
						/>
						<Button variant="outline" onclick={addTopic} disabled={!topicDraft.trim()}>
							<Plus />Add
						</Button>
					</div>
					{#if topics.length > 0}
						<div class="flex flex-wrap gap-1.5">
							{#each topics as topic (topic)}
								<Badge variant="secondary" class="gap-1 pr-1">
									{topic}
									<button
										type="button"
										class="rounded-full p-0.5 hover:bg-foreground/10"
										aria-label="Remove {topic}"
										onclick={() => (topics = topics.filter((t) => t !== topic))}
									>
										<X class="size-3" />
									</button>
								</Badge>
							{/each}
						</div>
					{/if}
				</div>
			</FormField>
			<FormField
				label="Template repository"
				layout="toggle"
				help="Offer this repository as a starting point for new ones"
			>
				<Switch bind:checked={isTemplate} />
			</FormField>
			{#snippet footer()}
				{@render saveButton(savingDetails, saveDetails)}
			{/snippet}
		</FormSection>

		<FormSection
			title="Visibility and branches"
			description="Who can see the repository and where new clones land."
		>
			<FormField
				label="Visibility"
				id="repo-visibility"
				help="Private repositories are visible to collaborators and teams only"
				span="half"
			>
				<Select
					type="single"
					value={String(visibility)}
					onValueChange={(v) => {
						if (v) visibility = Number(v) as Visibility;
					}}
				>
					<SelectTrigger id="repo-visibility" class="w-full">
						{visibility === Visibility.PRIVATE ? 'Private' : 'Public'}
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={String(Visibility.PUBLIC)}>Public</SelectItem>
						<SelectItem value={String(Visibility.PRIVATE)}>Private</SelectItem>
					</SelectContent>
				</Select>
			</FormField>
			<FormField
				label="Default branch"
				id="repo-default-branch"
				help="Pull requests and clones target this branch"
				span="half"
			>
				{#if branches.length > 0}
					<Select
						type="single"
						value={defaultBranch}
						onValueChange={(v) => {
							if (v) defaultBranch = v;
						}}
					>
						<SelectTrigger id="repo-default-branch" class="w-full font-mono"
							>{defaultBranch || 'Select a branch'}</SelectTrigger
						>
						<SelectContent>
							{#each branches as b (b)}
								<SelectItem value={b} class="font-mono">{b}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				{:else}
					<Input
						id="repo-default-branch"
						bind:value={defaultBranch}
						class="font-mono"
						placeholder="main"
					/>
				{/if}
			</FormField>
			{#snippet footer()}
				{@render saveButton(savingAccess, saveAccess)}
			{/snippet}
		</FormSection>

		<FormSection
			title="Features"
			description="Units shown in the tab bar, external urls replace the built in tracker or wiki."
		>
			<FormField label="Issues" layout="toggle">
				<Switch bind:checked={units.issues} />
			</FormField>
			{#if units.issues}
				<div class="form-subfields">
					<FormField
						label="External issue tracker"
						id="repo-ext-issues"
						help="Leave empty to use the built in tracker"
					>
						<Input
							id="repo-ext-issues"
							bind:value={units.externalIssuesUrl}
							placeholder="https://tracker.example.com/project"
						/>
					</FormField>
					<FormField
						label="Block on dependencies"
						layout="toggle"
						help="Blocked issues cannot close until their blockers close"
					>
						<Switch bind:checked={units.blockOnDependencies} />
					</FormField>
					<FormField
						label="Time tracking"
						layout="toggle"
						help="Shows time open and accepts manual time entries on issues"
					>
						<Switch bind:checked={units.timeTracking} />
					</FormField>
				</div>
			{/if}
			<FormField label="Pull requests" layout="toggle">
				<Switch bind:checked={units.pulls} />
			</FormField>
			<FormField label="Releases" layout="toggle">
				<Switch bind:checked={units.releases} />
			</FormField>
			<FormField label="Wiki" layout="toggle">
				<Switch bind:checked={units.wiki} />
			</FormField>
			{#if units.wiki}
				<div class="form-subfields">
					<FormField
						label="External wiki"
						id="repo-ext-wiki"
						help="Leave empty to use the built in wiki"
					>
						<Input
							id="repo-ext-wiki"
							bind:value={units.externalWikiUrl}
							placeholder="https://wiki.example.com"
						/>
					</FormField>
				</div>
			{/if}
			<FormField label="Projects" layout="toggle">
				<Switch bind:checked={units.projects} />
			</FormField>
			<FormField label="Packages" layout="toggle" help="Linked images and artifacts">
				<Switch bind:checked={units.packages} />
			</FormField>
			<FormField label="Actions" layout="toggle" help="Workflows under .distroface/workflows">
				<Switch bind:checked={units.actions} />
			</FormField>
			<FormField
				label="Agents"
				layout="toggle"
				help="Model driven reviews, tasks, and answers through configured connectors"
			>
				<Switch bind:checked={units.agents} />
			</FormField>
			{#snippet footer()}
				{@render saveButton(savingUnits, saveUnits)}
			{/snippet}
		</FormSection>

		<FormSection title="Merge settings" description="How pull requests land on their base branch.">
			{#each mergeMethods as method (method.value)}
				<FormField label={method.label} layout="toggle">
					<Switch checked={merge[method.flag]} onCheckedChange={(v) => (merge[method.flag] = v)} />
				</FormField>
			{/each}
			<FormField label="Default method" id="repo-merge-default" span="half">
				<Select
					type="single"
					value={String(merge.defaultMethod)}
					onValueChange={(v) => {
						if (v) merge.defaultMethod = Number(v) as GitMergeMethod;
					}}
				>
					<SelectTrigger id="repo-merge-default" class="w-full">{defaultMethodLabel}</SelectTrigger>
					<SelectContent>
						{#each allowedMethods as method (method.value)}
							<SelectItem value={String(method.value)}>{method.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
			<FormField
				label="Delete branch after merge"
				layout="toggle"
				help="Head branches of merged pull requests are removed"
			>
				<Switch bind:checked={merge.deleteBranchOnMerge} />
			</FormField>
			<FormField
				label="Allow forks"
				layout="toggle"
				help="Anyone who can read may copy the repository into their namespace"
			>
				<Switch bind:checked={merge.allowForks} />
			</FormField>
			{#snippet footer()}
				{@render saveButton(savingMerge, saveMerge)}
			{/snippet}
		</FormSection>

		<FormSection title="Storage" description="Size on disk and housekeeping.">
			<div class="grid gap-4 sm:grid-cols-2">
				<StatCard label="Objects" value={formatBytes(Number(repo.sizeBytes))} />
				<StatCard label="LFS" value={formatBytes(Number(repo.lfsSizeBytes))} />
			</div>
			{#snippet footer()}
				<Button variant="outline" onclick={runMaintenance} disabled={maintaining}>
					{#if maintaining}
						<Loader class="animate-spin" />
					{:else}
						<Wrench />
					{/if}
					{maintaining ? 'Running' : 'Run maintenance'}
				</Button>
			{/snippet}
		</FormSection>
	</FormPage>
{/if}
