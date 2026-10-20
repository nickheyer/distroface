<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { SquareKanban, Loader2, Lock, Eye } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const id = $derived(page.params.id ?? '');
	const canEdit = $derived(gitRepoStore.canWrite(GitUnit.PROJECTS) && !repo.isArchived);
	const listHref = $derived(resolve('/[namespace]/[name]/projects', { namespace, name }));
	const detailHref = $derived(
		resolve('/[namespace]/[name]/projects/[id]', { namespace, name, id })
	);

	const visibilities = [
		{
			value: 'private',
			label: 'Private',
			help: 'Only collaborators with write access to projects see this board',
			icon: Lock
		},
		{
			value: 'public',
			label: 'Public',
			help: 'Anyone who can see the repository sees this board',
			icon: Eye
		}
	];

	let project = $state<Project | undefined>();
	let loading = $state(true);
	let loadedId = $state('');
	let fTitle = $state('');
	let fDescription = $state('');
	let fPublic = $state(false);
	let saving = $state(false);

	$effect(() => {
		if (!id || loadedId === id) return;
		loadedId = id;
		void load();
	});

	async function load() {
		loading = true;
		const resp = await rpcClient.project
			.getProject({ id }, silentCallOptions)
			.catch(() => undefined);
		project = resp?.project;
		if (project) {
			fTitle = project.title;
			fDescription = project.description;
			fPublic = project.public;
		}
		loading = false;
	}

	async function save() {
		if (!project || !fTitle.trim() || saving) return;
		saving = true;
		const resp = await rpcClient.project
			.updateProject({
				id: project.id,
				title: fTitle.trim(),
				description: fDescription.trim(),
				public: fPublic
			})
			.catch(() => undefined);
		saving = false;
		if (!resp) return;
		toast.success('Project updated');
		await goto(resolve('/[namespace]/[name]/projects/[id]', { namespace, name, id }));
	}
</script>

<svelte:head>
	<title>Edit {project?.title ?? 'project'} - {namespace}/{name}</title>
</svelte:head>

{#if !canEdit}
	<EmptyState
		icon={SquareKanban}
		message="You cannot edit projects"
		description={repo.isArchived
			? 'This repository is archived and read only.'
			: 'Write access to the projects unit is required.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to projects</Button>
		{/snippet}
	</EmptyState>
{:else if loading}
	<div class="box divide-y">
		{#each { length: 3 }, i (i)}
			<div class="flex items-center gap-3 px-4 py-3">
				<Skeleton class="size-4 rounded-sm" />
				<div class="flex-1 space-y-2">
					<Skeleton class="h-4 w-40" />
					<Skeleton class="h-3 w-64" />
				</div>
			</div>
		{/each}
	</div>
{:else if !project}
	<EmptyState
		icon={SquareKanban}
		message="Project not found"
		description="This board does not exist or you cannot see it."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to projects</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Edit project"
		description="The title and description show on the board and in lists."
		backHref={detailHref}
		backLabel={project.title}
	>
		<FormSection title="Project" description="Name and purpose of the board.">
			<FormField label="Title" id="project-title" required span="half">
				<Input id="project-title" bind:value={fTitle} />
			</FormField>
			<FormField label="Description" id="project-desc">
				<Textarea id="project-desc" bind:value={fDescription} rows={2} />
			</FormField>
		</FormSection>

		<FormSection title="Visibility" description="Who can see the board.">
			<RadioGroup.Root
				value={fPublic ? 'public' : 'private'}
				onValueChange={(v) => (fPublic = v === 'public')}
				class="gap-0 divide-y rounded-lg border"
			>
				{#each visibilities as v (v.value)}
					<label
						class="flex cursor-pointer items-start gap-3 px-4 py-3"
						for="project-visibility-{v.value}"
					>
						<RadioGroup.Item value={v.value} id="project-visibility-{v.value}" class="mt-0.5" />
						<v.icon class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
						<span class="min-w-0">
							<span class="block text-sm font-medium">{v.label}</span>
							<span class="block text-xs text-muted-foreground">{v.help}</span>
						</span>
					</label>
				{/each}
			</RadioGroup.Root>
		</FormSection>

		{#snippet footer()}
			<Button onclick={save} disabled={saving || !fTitle.trim()}>
				{#if saving}<Loader2 class="animate-spin" />{/if}
				{saving ? 'Saving' : 'Save changes'}
			</Button>
			<Button variant="ghost-muted" href={detailHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
