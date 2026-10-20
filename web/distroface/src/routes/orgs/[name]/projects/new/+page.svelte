<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Kanban, Loader, Lock, Eye } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { ProjectTemplate } from '$lib/proto/distroface/v1/project_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const listHref = $derived(resolve('/orgs/[name]/projects', { name: orgName }));

	const templates = [
		{
			value: ProjectTemplate.BASIC_KANBAN,
			label: 'Basic kanban',
			help: 'To do, In progress, Done'
		},
		{
			value: ProjectTemplate.BUG_TRIAGE,
			label: 'Bug triage',
			help: 'Needs triage, High, Low, Closed'
		},
		{ value: ProjectTemplate.NONE, label: 'Empty', help: 'Start with no columns' }
	];

	const visibilities = [
		{
			value: 'private',
			label: 'Private',
			help: 'Only organization members see this board',
			icon: Lock
		},
		{
			value: 'public',
			label: 'Public',
			help: 'Anyone who can see the organization sees this board',
			icon: Eye
		}
	];

	let newTitle = $state('');
	let newDescription = $state('');
	let newTemplate = $state<ProjectTemplate>(ProjectTemplate.BASIC_KANBAN);
	let newPublic = $state(false);
	let creating = $state(false);

	async function createProject() {
		if (!newTitle.trim() || creating) return;
		creating = true;
		const resp = await rpcClient.project
			.createProject({
				orgId,
				title: newTitle.trim(),
				description: newDescription.trim(),
				template: newTemplate,
				public: newPublic
			})
			.catch(() => undefined);
		creating = false;
		if (!resp) return;
		toast.success('Project created');
		if (resp.project) {
			await goto(resolve('/orgs/[name]/projects/[id]', { name: orgName, id: resp.project.id }));
			return;
		}
		await goto(resolve('/orgs/[name]/projects', { name: orgName }));
	}
</script>

<svelte:head>
	<title>New project - {orgName}</title>
</svelte:head>

{#if !ctx.canAdmin}
	<EmptyState
		icon={Kanban}
		message="You cannot create projects"
		description="Only organization owners and admins manage projects."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to projects</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="New project"
		description="A board for issues from any repository in the organization."
		backHref={listHref}
		backLabel="Projects"
	>
		<FormSection title="Project" description="Name and purpose of the board.">
			<FormField label="Title" id="project-title" required span="half">
				<Input id="project-title" bind:value={newTitle} placeholder="Q4 roadmap" />
			</FormField>
			<FormField label="Description" id="project-desc">
				<Textarea
					id="project-desc"
					bind:value={newDescription}
					rows={2}
					placeholder="What this board tracks"
				/>
			</FormField>
		</FormSection>

		<FormSection title="Template" description="Starting columns for the board.">
			<RadioGroup.Root
				value={String(newTemplate)}
				onValueChange={(v) => {
					if (v) newTemplate = Number(v) as ProjectTemplate;
				}}
				class="gap-0 divide-y rounded-lg border"
			>
				{#each templates as t (t.value)}
					<label
						class="flex cursor-pointer items-start gap-3 px-4 py-3"
						for="project-template-{t.value}"
					>
						<RadioGroup.Item
							value={String(t.value)}
							id="project-template-{t.value}"
							class="mt-0.5"
						/>
						<span class="min-w-0">
							<span class="block text-sm font-medium">{t.label}</span>
							<span class="block text-xs text-muted-foreground">{t.help}</span>
						</span>
					</label>
				{/each}
			</RadioGroup.Root>
		</FormSection>

		<FormSection title="Visibility" description="Who can see the board.">
			<RadioGroup.Root
				value={newPublic ? 'public' : 'private'}
				onValueChange={(v) => (newPublic = v === 'public')}
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
			<Button onclick={createProject} disabled={creating || !newTitle.trim()}>
				{#if creating}<Loader class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create project'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
