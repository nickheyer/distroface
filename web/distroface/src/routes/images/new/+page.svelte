<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Eye, Loader2, Lock, Package } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
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
	import { RepositoryType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import { upstreamRepoName } from '$lib/repo-names';

	const repoTypeOptions = [
		{
			value: RepositoryType.STANDARD,
			label: 'Standard repository',
			description: 'Images pushed by you or CI'
		},
		{
			value: RepositoryType.MIRROR,
			label: 'Pull-through mirror',
			description: 'Watches an upstream OCI repository and mirrors its tags'
		}
	];

	const namespaceParam = page.url.searchParams.get('namespace') ?? '';

	let newType = $state<RepositoryType>(RepositoryType.STANDARD);
	let newNamespace = $state(namespaceParam);
	let newName = $state('');
	let nameValid = $state(false);
	let newDescription = $state('');
	let newPrivate = $state(false);
	let newMirror = $state(emptyMirrorForm());
	let creating = $state(false);

	const ownNamespace = $derived(authStore.user?.username ?? '');
	const isMirror = $derived(newType === RepositoryType.MIRROR);
	const derivedName = $derived(isMirror ? upstreamRepoName(newMirror.upstream, 'image') : '');
	const canSubmit = $derived(nameValid && (!isMirror || !!newMirror.upstream.trim()));
	// Org pages link here with their namespace, every exit returns there
	const orgNamespace = $derived(!!namespaceParam && namespaceParam !== ownNamespace);
	const exitHref = $derived(
		orgNamespace ? resolve('/orgs/[name]', { name: namespaceParam }) : resolve('/images')
	);
	const exitLabel = $derived(orgNamespace ? `${namespaceParam} images` : 'Images');

	async function createRepo() {
		if (!canSubmit || creating) return;
		creating = true;
		const resp = await rpcClient.repository
			.createRepository({
				namespace: newNamespace || ownNamespace,
				name: newName.trim(),
				description: newDescription.trim(),
				visibility: newPrivate ? Visibility.PRIVATE : Visibility.PUBLIC,
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
				resolve('/images/[namespace]/[...name]', {
					namespace: created.namespace,
					name: created.name
				})
			);
		else await goto(resolve(exitHref as `/${string}`));
		creating = false;
	}
</script>

<svelte:head>
	<title>New image repository</title>
</svelte:head>

{#if !authStore.isAuthenticated}
	<EmptyState
		icon={Package}
		message="Sign in to create a repository"
		description="Image repositories belong to your account or an organization you are a member of."
	>
		{#snippet actions()}
			<Button href={resolve('/login')}>Sign in</Button>
			<Button variant="outline" href={exitHref}>Back to {exitLabel.toLowerCase()}</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Create a new image repository"
		description="Create ahead of a push, or mirror an upstream image."
		backHref={exitHref}
		backLabel={exitLabel}
	>
		<FormSection title="Repository" description="What kind of repository this is and who owns it.">
			<FormField
				label="Type"
				id="image-repo-type"
				help="Mirrors pull new upstream tags automatically"
				span="half"
			>
				<Select
					type="single"
					value={String(newType)}
					onValueChange={(v) => {
						if (v) newType = Number(v) as RepositoryType;
					}}
				>
					<SelectTrigger class="w-full" id="image-repo-type">
						{repoTypeOptions.find((o) => o.value === newType)?.label}
					</SelectTrigger>
					<SelectContent>
						{#each repoTypeOptions as o (o.value)}
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
					kind="oci"
					part="source"
					idPrefix="image-repo-mirror"
				/>
			{/if}

			<RepoNameFields
				family="image"
				bind:namespace={newNamespace}
				bind:name={newName}
				bind:valid={nameValid}
				{ownNamespace}
				defaultName={derivedName}
				placeholder="base-images"
				idPrefix="image-repo"
			/>

			<FormField label="Description" id="image-repo-description">
				<Input
					id="image-repo-description"
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
				<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="image-repo-public">
					<RadioGroup.Item value="public" id="image-repo-public" class="mt-0.5" />
					<Eye class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<span class="min-w-0">
						<span class="block text-sm font-medium">Public</span>
						<span class="block text-xs text-muted-foreground"
							>Anyone who can reach this instance can pull it</span
						>
					</span>
				</label>
				<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="image-repo-private">
					<RadioGroup.Item value="private" id="image-repo-private" class="mt-0.5" />
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
				<MirrorConfigFields form={newMirror} kind="oci" part="sync" idPrefix="image-repo-mirror" />
			</FormSection>
		{/if}

		{#snippet footer()}
			<Button onclick={createRepo} disabled={creating || !canSubmit}>
				{#if creating}<Loader2 class="animate-spin" />{/if}
				{creating ? (isMirror ? 'Validating' : 'Creating') : 'Create repository'}
			</Button>
			<Button variant="ghost-muted" href={exitHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
