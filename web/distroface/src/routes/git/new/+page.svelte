<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Eye, GitBranch, Loader, Lock } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import RepoNameFields from '$lib/components/repo-name-fields.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm,
		mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import { GitRepoType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import { upstreamRepoName } from '$lib/repo-names';

	type CreateKind = 'standard' | 'mirror' | 'import' | 'template';
	const createKinds: { value: CreateKind; label: string; description: string }[] = [
		{
			value: 'standard',
			label: 'Standard repository',
			description: 'An empty git repository'
		},
		{
			value: 'mirror',
			label: 'Mirror',
			description: 'A synced copy of an upstream git repository'
		},
		{
			value: 'import',
			label: 'Import a repository',
			description: 'One time sync of an upstream git repository'
		},
		{
			value: 'template',
			label: 'From a template',
			description: 'Copy of a template repository on this instance'
		}
	];
	const gitignoreTemplates = [
		'Go',
		'Node',
		'Python',
		'Rust',
		'Java',
		'Kotlin',
		'C',
		'C++',
		'CSharp',
		'Ruby',
		'Swift',
		'Dart',
		'Elixir',
		'Haskell',
		'Scala',
		'Terraform',
		'Unity',
		'VisualStudio'
	];
	const licenses = [
		{ id: 'MIT', label: 'MIT License' },
		{ id: 'Apache-2.0', label: 'Apache License 2.0' },
		{ id: 'GPL-3.0', label: 'GNU GPL v3' },
		{ id: 'LGPL-3.0', label: 'GNU LGPL v3' },
		{ id: 'AGPL-3.0', label: 'GNU AGPL v3' },
		{ id: 'BSD-2-Clause', label: 'BSD 2-Clause' },
		{ id: 'BSD-3-Clause', label: 'BSD 3-Clause' },
		{ id: 'MPL-2.0', label: 'Mozilla Public License 2.0' },
		{ id: 'ISC', label: 'ISC License' },
		{ id: 'Unlicense', label: 'The Unlicense' }
	];

	const namespaceParam = page.url.searchParams.get('namespace') ?? '';

	let createKind = $state<CreateKind>('standard');
	let newNamespace = $state(namespaceParam);
	let newName = $state('');
	let nameValid = $state(false);
	let newDescription = $state('');
	let newWebsite = $state('');
	let newTopics = $state('');
	let newPrivate = $state(false);
	let newDefaultBranch = $state('');
	let newAutoInit = $state(false);
	let newGitignore = $state('');
	let newLicense = $state('');
	let newMirror = $state(emptyMirrorForm());
	let newMirrorReleases = $state(false);
	let newMirrorPackages = $state(false);
	let newMirrorImages = $state('');
	let newImport = $state({
		upstream: '',
		username: '',
		authToken: '',
		metadata: true,
		releases: true,
		wiki: true,
		lfs: true
	});
	let newImportPackages = $state(false);
	let newImportImages = $state('');
	let newTemplate = $state('');
	let newIncludeLabels = $state(true);
	let creating = $state(false);

	const ownNamespace = $derived(authStore.user?.username ?? '');
	const usesMirror = $derived(createKind === 'mirror');
	const usesImport = $derived(createKind === 'import');
	const derivedName = $derived(
		usesMirror
			? upstreamRepoName(newMirror.upstream, 'git')
			: usesImport
				? upstreamRepoName(newImport.upstream, 'git')
				: createKind === 'template'
					? upstreamRepoName(newTemplate, 'git')
					: ''
	);
	const canSubmit = $derived(
		nameValid &&
			(!usesMirror || !!newMirror.upstream.trim()) &&
			(!usesImport || !!newImport.upstream.trim()) &&
			(createKind !== 'template' || !!newTemplate)
	);
	// Org pages link here with their namespace, every exit returns there
	const orgNamespace = $derived(!!namespaceParam && namespaceParam !== ownNamespace);
	const exitHref = $derived(
		orgNamespace ? resolve('/orgs/[name]/repos', { name: namespaceParam }) : resolve('/git')
	);
	const exitLabel = $derived(orgNamespace ? `${namespaceParam} repositories` : 'Repositories');

	function parseTopics(raw: string): string[] {
		return [
			...new Set(
				raw
					.split(/[,\s]+/)
					.map((t) => t.trim().toLowerCase())
					.filter(Boolean)
			)
		];
	}

	async function createRepo() {
		if (!canSubmit || creating) return;
		creating = true;
		const namespace = newNamespace || ownNamespace;
		const name = newName.trim();
		try {
			let created: GitRepository | undefined;
			if (createKind === 'template') {
				const [templateNamespace, templateName] = newTemplate.split('/');
				const resp = await rpcClient.git.useTemplate({
					templateNamespace,
					templateName,
					namespace,
					name,
					description: newDescription.trim(),
					visibility: newPrivate ? Visibility.PRIVATE : Visibility.PUBLIC,
					includeLabels: newIncludeLabels
				});
				created = resp.repository;
			} else {
				const resp = await rpcClient.git.createGitRepository({
					namespace,
					name,
					description: newDescription.trim(),
					website: newWebsite.trim(),
					topics: parseTopics(newTopics),
					visibility: newPrivate ? Visibility.PRIVATE : Visibility.PUBLIC,
					defaultBranch: newDefaultBranch.trim(),
					type: usesMirror ? GitRepoType.MIRROR : GitRepoType.STANDARD,
					mirror: usesMirror ? mirrorInit(newMirror) : undefined,
					importSource: usesImport
						? {
								upstream: newImport.upstream.trim(),
								username: newImport.username.trim(),
								...(newImport.authToken ? { authToken: newImport.authToken } : {}),
								metadata: newImport.metadata,
								releases: newImport.releases,
								wiki: newImport.wiki,
								lfs: newImport.lfs,
								images: newImportPackages
									? newImportImages
											.split(/\n+/)
											.map((s) => s.trim())
											.filter(Boolean)
									: []
							}
						: undefined,
					autoInit: createKind === 'standard' && newAutoInit,
					gitignoreTemplate: createKind === 'standard' && newAutoInit ? newGitignore : '',
					license: createKind === 'standard' && newAutoInit ? newLicense : '',
					mirrorReleases: usesMirror && newMirrorReleases,
					mirrorImages:
						usesMirror && newMirrorPackages
							? newMirrorImages
									.split(/\n+/)
									.map((s) => s.trim())
									.filter(Boolean)
							: []
				});
				created = resp.repository;
			}
			toast.success('Repository created');
			if (created)
				await goto(
					resolve('/[namespace]/[name]', { namespace: created.namespace, name: created.name })
				);
			else await goto(resolve(exitHref as `/${string}`));
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head>
	<title>New repository</title>
</svelte:head>

{#if !authStore.isAuthenticated}
	<EmptyState
		icon={GitBranch}
		message="Sign in to create a repository"
		description="Repositories belong to your account or an organization you are a member of."
	>
		{#snippet actions()}
			<Button href={resolve('/login')}>Sign in</Button>
			<Button variant="outline" href={exitHref}>Back to {exitLabel.toLowerCase()}</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Create a new repository"
		description="Create an empty one, import/mirror an upstream, or start from a template."
		backHref={exitHref}
		backLabel={exitLabel}
	>
		<FormSection title="Repository" description="What kind of repository this is and who owns it.">
			<FormField label="Type" id="git-repo-kind" span="half">
				<Select
					type="single"
					value={createKind}
					onValueChange={(v) => {
						if (v) createKind = v as CreateKind;
					}}
				>
					<SelectTrigger class="w-full" id="git-repo-kind">
						{createKinds.find((k) => k.value === createKind)?.label}
					</SelectTrigger>
					<SelectContent>
						{#each createKinds as k (k.value)}
							<SelectItem value={k.value}>
								<div class="py-0.5">
									<div>{k.label}</div>
									<div class="text-xs text-muted-foreground">{k.description}</div>
								</div>
							</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>

			{#if usesMirror}
				<MirrorConfigFields form={newMirror} kind="git" part="source" idPrefix="git-repo-mirror" />
			{:else if usesImport}
				<FormField
					label="Repository to import"
					id="git-repo-import-upstream"
					required
					help="Any https or ssh clone URL, owner/repo works for GitHub, GitLab, and Gitea"
				>
					<Input
						id="git-repo-import-upstream"
						name="git-repo-import-upstream"
						bind:value={newImport.upstream}
						placeholder="https://example.com/owner/project.git"
						autocomplete="off"
						data-1p-ignore
						data-lpignore="true"
						data-bwignore
					/>
				</FormField>
				<FormField
					label="Username"
					id="git-repo-import-username"
					help="Only for private https upstreams"
					span="half"
				>
					<Input
						id="git-repo-import-username"
						name="git-repo-import-username"
						bind:value={newImport.username}
						placeholder="username"
						autocomplete="off"
						data-1p-ignore
						data-lpignore="true"
						data-bwignore
					/>
				</FormField>
				<FormField
					label="Access token"
					id="git-repo-import-token"
					help="Sent as basic auth, forgotten once the import ends"
					span="half"
				>
					<PasswordInput
						id="git-repo-import-token"
						name="git-repo-import-token"
						bind:value={newImport.authToken}
						placeholder="Token"
						autocomplete="new-password"
						data-1p-ignore
						data-lpignore="true"
						data-bwignore
					/>
				</FormField>
			{:else if createKind === 'template'}
				<FormField label="Template repository" id="git-repo-template" required>
					<AsyncSelect
						bind:selected={newTemplate}
						placeholder="Choose a template"
						searchPlaceholder="Search templates"
						fetchPage={async (query, pageToken) => {
							const resp = await rpcClient.git.listGitRepositories({
								page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
								type: GitRepoType.TEMPLATE
							});
							return {
								items: resp.repositories.map((r) => ({
									value: `${r.namespace}/${r.name}`,
									label: `${r.namespace}/${r.name}`,
									description: r.description
								})),
								nextPageToken: resp.page?.nextPageToken ?? ''
							};
						}}
					/>
				</FormField>
			{/if}

			<RepoNameFields
				family="git"
				bind:namespace={newNamespace}
				bind:name={newName}
				bind:valid={nameValid}
				{ownNamespace}
				defaultName={derivedName}
				placeholder="my-project"
				idPrefix="git-repo"
			/>

			<FormField label="Description" id="git-repo-description">
				<Input
					id="git-repo-description"
					bind:value={newDescription}
					placeholder="What lives here?"
				/>
			</FormField>

			{#if createKind !== 'template'}
				<FormField label="Website" id="git-repo-website" span="half">
					<Input id="git-repo-website" bind:value={newWebsite} placeholder="https://example.com" />
				</FormField>
				<FormField label="Topics" id="git-repo-topics" help="Comma separated" span="half">
					<Input id="git-repo-topics" bind:value={newTopics} placeholder="go, cli, tooling" />
				</FormField>
			{/if}
		</FormSection>

		<FormSection title="Visibility">
			<RadioGroup.Root
				value={newPrivate ? 'private' : 'public'}
				onValueChange={(v) => (newPrivate = v === 'private')}
				class="gap-0 divide-y rounded-lg border"
			>
				<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="git-repo-public">
					<RadioGroup.Item value="public" id="git-repo-public" class="mt-0.5" />
					<Eye class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<span class="min-w-0">
						<span class="block text-sm font-medium">Public</span>
						<span class="block text-xs text-muted-foreground"
							>Anyone who can reach this instance can see and clone it</span
						>
					</span>
				</label>
				<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="git-repo-private">
					<RadioGroup.Item value="private" id="git-repo-private" class="mt-0.5" />
					<Lock class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<span class="min-w-0">
						<span class="block text-sm font-medium">Private</span>
						<span class="block text-xs text-muted-foreground"
							>Visible only to collaborators, teams, and admins</span
						>
					</span>
				</label>
			</RadioGroup.Root>
		</FormSection>

		{#if createKind === 'standard'}
			<FormSection
				title="Initial contents"
				description="Skip this to push an existing repository from your machine."
			>
				<FormField
					label="Default branch"
					id="git-repo-branch"
					help="Empty uses the instance default"
					span="half"
				>
					<Input
						id="git-repo-branch"
						bind:value={newDefaultBranch}
						placeholder="main"
						class="font-mono"
					/>
				</FormField>
				<FormField
					label="Initialize with a README"
					layout="toggle"
					help="Creates a first commit so you can clone right away"
				>
					<Switch bind:checked={newAutoInit} />
				</FormField>
				{#if newAutoInit}
					<FormField label="Add .gitignore" id="git-repo-gitignore" span="half">
						<Select
							type="single"
							value={newGitignore}
							onValueChange={(v) => (newGitignore = v ?? '')}
						>
							<SelectTrigger class="w-full" id="git-repo-gitignore"
								>{newGitignore || 'None'}</SelectTrigger
							>
							<SelectContent>
								<SelectItem value="">None</SelectItem>
								{#each gitignoreTemplates as t (t)}
									<SelectItem value={t}>{t}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					</FormField>
					<FormField label="License" id="git-repo-license" span="half">
						<Select type="single" value={newLicense} onValueChange={(v) => (newLicense = v ?? '')}>
							<SelectTrigger class="w-full" id="git-repo-license">
								{licenses.find((l) => l.id === newLicense)?.label ?? 'None'}
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="">None</SelectItem>
								{#each licenses as l (l.id)}
									<SelectItem value={l.id}>{l.label}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					</FormField>
				{/if}
			</FormSection>
		{/if}

		{#if usesMirror}
			<FormSection title="Sync" description="What to pull from the upstream and how often.">
				<MirrorConfigFields form={newMirror} kind="git" part="sync" idPrefix="git-repo-mirror" />
				<FormField
					label="Also mirror releases"
					layout="toggle"
					help="Creates a linked artifact package that mirrors upstream release assets"
				>
					<Switch bind:checked={newMirrorReleases} />
				</FormField>
				<FormField
					label="Also mirror packages"
					layout="toggle"
					help="Creates linked image mirrors for upstream container images"
				>
					<Switch bind:checked={newMirrorPackages} />
				</FormField>
				{#if newMirrorPackages}
					<FormField
						label="Image references"
						id="git-repo-mirror-images"
						help="One upstream image per line, such as ghcr.io/owner/app"
					>
						<Textarea
							id="git-repo-mirror-images"
							bind:value={newMirrorImages}
							rows={3}
							class="font-mono"
							placeholder="ghcr.io/owner/app"
						/>
					</FormField>
				{/if}
			</FormSection>
		{/if}

		{#if usesImport}
			<FormSection
				title="What to import"
				description="Branches and tags always come over. Once the import finishes the repository is yours to push to."
			>
				<FormField
					label="Issues, pull requests, labels, and milestones"
					layout="toggle"
					help="Needs a GitHub, GitLab, Gitea, or Forgejo upstream, skipped anywhere else"
				>
					<Switch bind:checked={newImport.metadata} />
				</FormField>
				<FormField
					label="Releases and their assets"
					layout="toggle"
					help="Release notes land on the releases page, uploaded assets are copied once into a linked artifact package"
				>
					<Switch bind:checked={newImport.releases} />
				</FormField>
				<FormField
					label="Container images"
					layout="toggle"
					help="Copies upstream images once into linked image repositories, they stand alone afterwards"
				>
					<Switch bind:checked={newImportPackages} />
				</FormField>
				{#if newImportPackages}
					<FormField
						label="Image references"
						id="git-repo-import-images"
						help="One upstream image per line, such as ghcr.io/owner/app"
					>
						<Textarea
							id="git-repo-import-images"
							bind:value={newImportImages}
							rows={3}
							class="font-mono"
							placeholder="ghcr.io/owner/app"
						/>
					</FormField>
				{/if}
				<FormField
					label="Wiki"
					layout="toggle"
					help="Fetches the upstream wiki repository when there is one"
				>
					<Switch bind:checked={newImport.wiki} />
				</FormField>
				<FormField
					label="LFS objects"
					layout="toggle"
					help="Downloads the objects behind LFS pointers through the upstream batch api"
				>
					<Switch bind:checked={newImport.lfs} />
				</FormField>
			</FormSection>
		{/if}

		{#if createKind === 'template'}
			<FormSection title="Template">
				<FormField label="Include labels" layout="toggle" help="Copy the template's issue labels">
					<Switch bind:checked={newIncludeLabels} />
				</FormField>
			</FormSection>
		{/if}

		{#snippet footer()}
			<Button onclick={createRepo} disabled={creating || !canSubmit}>
				{#if creating}<Loader class="animate-spin" />{/if}
				{creating ? (usesMirror || usesImport ? 'Validating' : 'Creating') : 'Create repository'}
			</Button>
			<Button variant="ghost-muted" href={exitHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
