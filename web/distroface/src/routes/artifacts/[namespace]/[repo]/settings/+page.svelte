<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Archive, Loader, Settings } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm,
		mirrorFormFrom,
		mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import { ArtifactRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import { artifactMirrorKind } from '$lib/mirror';

	const namespace = $derived(page.params.namespace ?? '');
	const repoName = $derived(page.params.repo ?? '');
	const repoHref = $derived(
		resolve('/artifacts/[namespace]/[repo]', { namespace, repo: repoName })
	);

	let repo = $state<ArtifactRepository | null>(null);
	let loading = $state(true);
	let description = $state('');
	let isPrivate = $state(false);
	let mirror = $state(emptyMirrorForm());
	let saving = $state(false);

	const repoIsMirror = $derived(
		!!repo && repo.type !== ArtifactRepoType.FILE && repo.type !== ArtifactRepoType.UNSPECIFIED
	);
	const canMutate = $derived(
		!!repo &&
			(repo.owner === authStore.user?.username ||
				authStore.hasPermission('artifacts', 'manage') ||
				authStore.hasPermission('organizations', 'update', namespace))
	);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.artifact.getArtifactRepository(
				{ name: repoName, namespace },
				silentCallOptions
			);
			repo = resp.repository ?? null;
			if (repo) {
				description = repo.description;
				isPrivate = repo.isPrivate;
				mirror = mirrorFormFrom(repo.mirror);
			}
		} catch {
			repo = null;
		} finally {
			loading = false;
		}
	}

	async function save() {
		if (!repo || saving) return;
		saving = true;
		try {
			await rpcClient.artifact.updateArtifactRepository({
				name: repoName,
				namespace,
				description,
				isPrivate,
				mirror: repoIsMirror ? mirrorInit(mirror) : undefined
			});
			toast.success('Repository updated');
			await goto(resolve('/artifacts/[namespace]/[repo]', { namespace, repo: repoName }));
		} catch {
			return;
		} finally {
			saving = false;
		}
	}

	onMount(load);
</script>

<svelte:head>
	<title>Settings - {namespace}/{repoName} - Artifacts - Distroface</title>
</svelte:head>

{#if loading}
	<div class="mx-auto w-full max-w-5xl space-y-4">
		<Skeleton class="h-4 w-40" />
		<Skeleton class="h-7 w-64" />
		<Skeleton class="h-4 w-96" />
		<Skeleton class="mt-6 h-56 w-full rounded-lg" />
	</div>
{:else if !repo}
	<EmptyState
		icon={Archive}
		message="Repository not found"
		description="It may be private or deleted."
	>
		{#snippet actions()}
			<Button variant="outline" href={resolve('/artifacts')}>Back to artifacts</Button>
		{/snippet}
	</EmptyState>
{:else if !canMutate}
	<EmptyState
		icon={Settings}
		message="You cannot change this repository"
		description="Only its owner or an administrator can edit the settings."
	>
		{#snippet actions()}
			<Button variant="outline" href={repoHref}>Back to {repoName}</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Repository settings"
		description="Update the description{repoIsMirror
			? ', visibility, and mirror source'
			: ' and visibility'} of {namespace}/{repoName}."
		backHref={repoHref}
		backLabel="{namespace}/{repoName}"
	>
		<FormSection title="Repository" description="How it appears and who can see it.">
			<FormField label="Description" id="settings-description">
				<Input
					id="settings-description"
					bind:value={description}
					placeholder="What is stored here?"
				/>
			</FormField>
			<FormField label="Private" layout="toggle" help="Visible only to you and admins">
				<Switch bind:checked={isPrivate} />
			</FormField>
		</FormSection>

		{#if repoIsMirror}
			<FormSection title="Mirror source" description="The upstream the repository syncs from.">
				<MirrorConfigFields
					form={mirror}
					kind={artifactMirrorKind(repo.type)}
					tokenSet={repo.mirror?.authTokenSet ?? false}
					lastSync={repo.mirrorLastSync}
					lastError={repo.mirrorLastError}
					nextAttempt={repo.mirrorNextAttempt}
					idPrefix="settings-mirror"
				/>
			</FormSection>
		{/if}

		{#snippet footer()}
			<Button onclick={save} disabled={saving}>
				{#if saving}<Loader class="animate-spin" />{/if}
				{saving ? 'Saving' : 'Save changes'}
			</Button>
			<Button variant="ghost-muted" href={repoHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
