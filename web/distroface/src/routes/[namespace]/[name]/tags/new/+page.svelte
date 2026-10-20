<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader2, Tag } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const canWrite = $derived(!!repo && gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);
	const listHref = $derived(resolve('/[namespace]/[name]/tags', { namespace, name }));

	let tag = $state('');
	let target = $state(page.url.searchParams.get('target') ?? '');
	let message = $state('');
	let seededFor = $state('');
	let creating = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== seededFor) {
			seededFor = repo.fullName;
			if (!target) target = repo.defaultBranch;
		}
	});

	async function createTag() {
		if (!tag.trim() || creating) return;
		creating = true;
		try {
			await rpcClient.gitRefs.createTag({
				namespace,
				name,
				tag: tag.trim(),
				target: target.trim(),
				message: message.trim()
			});
			toast.success('Tag created');
			gitRepoStore.reload();
			await goto(resolve('/[namespace]/[name]/tags', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head>
	<title>New tag - {namespace}/{name}</title>
</svelte:head>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !canWrite}
	<EmptyState
		icon={Tag}
		message="You cannot create tags"
		description={repo.isArchived
			? 'This repository is archived.'
			: 'Write access to code is required.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to tags</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="New tag"
		description="Marks a point in the history of {namespace}/{name}."
		backHref={listHref}
		backLabel="Tags"
	>
		<FormSection title="Tag" description="A message makes it an annotated tag.">
			<FormField label="Tag name" id="new-tag-name" required span="half">
				<Input
					id="new-tag-name"
					bind:value={tag}
					placeholder="v1.2.0"
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
				/>
			</FormField>
			<FormField label="Target" id="new-tag-target" help="Branch, tag, or commit sha" span="half">
				<Input id="new-tag-target" bind:value={target} class="font-mono" spellcheck="false" />
			</FormField>
			<FormField label="Message" id="new-tag-message" help="Creates an annotated tag when set">
				<Textarea
					id="new-tag-message"
					bind:value={message}
					rows={3}
					placeholder="Release notes or a short description"
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={createTag} disabled={creating || !tag.trim()}>
				{#if creating}<Loader2 class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create tag'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
