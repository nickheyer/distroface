<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Archive, Eye, Loader, Lock } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { OrgContext } from '$lib/org-context.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import RepoNameFields from '$lib/components/repo-name-fields.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm,
		mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import { ArtifactRepoType } from '$lib/proto/distroface/v1/types_pb';
	import { artifactRepoTypeOptions, artifactMirrorKind } from '$lib/mirror';
	import { upstreamRepoName } from '$lib/repo-names';

	const namespaceParam = page.url.searchParams.get('namespace') ?? '';

	let newType = $state<ArtifactRepoType>(ArtifactRepoType.FILE);
	let newNamespace = $state(namespaceParam);
	let newName = $state('');
	let nameValid = $state(false);
	let newDescription = $state('');
	let newPrivate = $state(false);
	let newMirror = $state(emptyMirrorForm());
	let creating = $state(false);

	const ownNamespace = $derived(authStore.user?.username ?? '');
	const isMirror = $derived(newType !== ArtifactRepoType.FILE);
	const derivedName = $derived(isMirror ? upstreamRepoName(newMirror.upstream, 'artifact') : '');
	const canSubmit = $derived(nameValid && (!isMirror || !!newMirror.upstream.trim()));
	const org = new OrgContext();
	// Org pages link here with their namespace, every exit returns there
	const orgNamespace = $derived(!!namespaceParam && namespaceParam !== ownNamespace);
	const exitHref = $derived(
		orgNamespace
			? resolve('/orgs/[name]/artifacts', { name: namespaceParam })
			: resolve('/artifacts')
	);
	const exitLabel = $derived(orgNamespace ? `${namespaceParam} artifacts` : 'Artifacts');
	const canCreate = $derived(authStore.hasPermission('artifacts', 'create') || org.canAdmin);
	const checking = $derived(!canCreate && orgNamespace && org.loading);

	onMount(() => {
		if (orgNamespace) org.load(namespaceParam);
	});

	async function createRepo() {
		if (!canSubmit || creating) return;
		creating = true;
		const resp = await rpcClient.artifact
			.createArtifactRepository({
				name: newName.trim(),
				namespace: newNamespace || ownNamespace,
				description: newDescription.trim(),
				isPrivate: newPrivate,
				type: newType,
				mirror: isMirror ? mirrorInit(newMirror) : undefined
			})
			.catch(() => null);
		if (!resp) {
			creating = false;
			return;
		}
		toast.success('Repository created');
		const created = resp.repository;
		if (created)
			await goto(
				resolve('/artifacts/[namespace]/[repo]', {
					namespace: created.namespace,
					repo: created.name
				})
			);
		else await goto(resolve(exitHref as `/${string}`));
		creating = false;
	}
</script>

<svelte:head>
	<title>New artifact repository</title>
</svelte:head>

{#if checking}
	<div class="mx-auto w-full max-w-5xl space-y-4">
		<Skeleton class="h-7 w-72" />
		<Skeleton class="h-48 w-full rounded-lg" />
	</div>
{:else if !canCreate}
	<EmptyState
		icon={Archive}
		message="You cannot create artifact repositories"
		description="Ask an administrator for the artifacts permission, or an organization owner to make you an admin."
	>
		{#snippet actions()}
			<Button variant="outline" href={exitHref}>Back to {exitLabel.toLowerCase()}</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Create a new artifact repository"
		description="Stores build artifacts and packages, or mirrors release assets from an upstream forge."
		backHref={exitHref}
		backLabel={exitLabel}
	>
		<FormSection title="Repository" description="What kind of repository this is and who owns it.">
			<FormField
				label="Type"
				id="artifact-repo-type"
				help="Mirrors pull upstream releases automatically"
				span="half"
			>
				<Select
					type="single"
					value={String(newType)}
					onValueChange={(v) => {
						if (v) newType = Number(v) as ArtifactRepoType;
					}}
				>
					<SelectTrigger class="w-full" id="artifact-repo-type">
						{artifactRepoTypeOptions.find((o) => o.value === newType)?.label}
					</SelectTrigger>
					<SelectContent>
						{#each artifactRepoTypeOptions as o (o.value)}
							<SelectItem value={String(o.value)}>
								<div class="py-0.5">
									<div>{o.label}</div>
									<div class="text-xs text-muted-foreground">{o.description}</div>
								</div>
							</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>

			{#if isMirror}
				<MirrorConfigFields
					form={newMirror}
					kind={artifactMirrorKind(newType)}
					part="source"
					idPrefix="artifact-repo-mirror"
				/>
			{/if}

			<RepoNameFields
				family="artifact"
				bind:namespace={newNamespace}
				bind:name={newName}
				bind:valid={nameValid}
				{ownNamespace}
				defaultName={derivedName}
				placeholder="build-artifacts"
				idPrefix="artifact-repo"
			/>

			<FormField label="Description" id="artifact-repo-description">
				<Input
					id="artifact-repo-description"
					bind:value={newDescription}
					placeholder="What is stored here?"
				/>
			</FormField>
		</FormSection>

		<FormSection title="Visibility">
			<RadioGroup.Root
				value={newPrivate ? 'private' : 'public'}
				onValueChange={(v) => (newPrivate = v === 'private')}
				class="gap-0 divide-y rounded-lg border"
			>
				<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="artifact-repo-public">
					<RadioGroup.Item value="public" id="artifact-repo-public" class="mt-0.5" />
					<Eye class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<span class="min-w-0">
						<span class="block text-sm font-medium">Public</span>
						<span class="block text-xs text-muted-foreground"
							>Anyone who can reach this instance can download it</span
						>
					</span>
				</label>
				<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="artifact-repo-private">
					<RadioGroup.Item value="private" id="artifact-repo-private" class="mt-0.5" />
					<Lock class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<span class="min-w-0">
						<span class="block text-sm font-medium">Private</span>
						<span class="block text-xs text-muted-foreground"
							>Visible only to you, organization members, and admins</span
						>
					</span>
				</label>
			</RadioGroup.Root>
		</FormSection>

		{#if isMirror}
			<FormSection title="Sync" description="What to pull from the upstream and how often.">
				<MirrorConfigFields
					form={newMirror}
					kind={artifactMirrorKind(newType)}
					part="sync"
					idPrefix="artifact-repo-mirror"
				/>
			</FormSection>
		{/if}

		{#snippet footer()}
			<Button onclick={createRepo} disabled={creating || !canSubmit}>
				{#if creating}<Loader class="animate-spin" />{/if}
				{creating ? (isMirror ? 'Validating' : 'Creating') : 'Create repository'}
			</Button>
			<Button variant="ghost-muted" href={exitHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
