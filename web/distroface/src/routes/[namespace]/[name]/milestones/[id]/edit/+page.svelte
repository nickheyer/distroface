<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Milestone as MilestoneIcon, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
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
	import { IssueState, type Milestone } from '$lib/proto/distroface/v1/issue_pb';
	import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const id = $derived(page.params.id ?? '');
	const canManage = $derived(gitRepoStore.canWrite(GitUnit.ISSUES) && !repo.isArchived);
	const listHref = $derived(resolve('/[namespace]/[name]/milestones', { namespace, name }));

	let milestone = $state<Milestone | undefined>();
	let loading = $state(true);
	let loadedId = $state('');
	let fTitle = $state('');
	let fDescription = $state('');
	let fDue = $state('');
	let saving = $state(false);

	$effect(() => {
		if (!id || loadedId === id) return;
		loadedId = id;
		void load();
	});

	async function load() {
		loading = true;
		let found: Milestone | undefined;
		let pageToken = '';
		do {
			const resp = await rpcClient.issue
				.listMilestones(
					{
						namespace,
						name,
						state: IssueState.UNSPECIFIED,
						page: { pageSize: 500, pageToken, orderBy: '' }
					},
					silentCallOptions
				)
				.catch(() => undefined);
			found = resp?.milestones.find((m) => m.id === id);
			pageToken = resp?.page?.nextPageToken ?? '';
		} while (!found && pageToken);
		milestone = found;
		if (found) {
			fTitle = found.title;
			fDescription = found.description;
			fDue = found.dueDate ? timestampDate(found.dueDate).toISOString().slice(0, 10) : '';
		}
		loading = false;
	}

	function dueTimestamp() {
		if (!fDue) return { seconds: 0n, nanos: 0 };
		const d = new Date(`${fDue}T12:00:00`);
		return Number.isNaN(d.getTime()) ? undefined : timestampFromDate(d);
	}

	async function save() {
		if (!milestone || !fTitle.trim() || saving) return;
		saving = true;
		const resp = await rpcClient.issue
			.updateMilestone({
				namespace,
				name,
				id: milestone.id,
				title: fTitle.trim(),
				description: fDescription.trim(),
				dueDate: dueTimestamp()
			})
			.catch(() => undefined);
		saving = false;
		if (!resp) return;
		toast.success('Milestone updated');
		await goto(resolve('/[namespace]/[name]/milestones', { namespace, name }));
	}
</script>

<svelte:head>
	<title>Edit milestone - {namespace}/{name}</title>
</svelte:head>

{#if !canManage}
	<EmptyState
		icon={MilestoneIcon}
		message="You cannot edit milestones"
		description={repo.isArchived
			? 'This repository is archived and read only.'
			: 'Write access to the issues unit is required.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to milestones</Button>
		{/snippet}
	</EmptyState>
{:else if loading}
	<Skeleton class="h-48 w-full rounded-lg" />
{:else if !milestone}
	<EmptyState
		icon={MilestoneIcon}
		message="Milestone not found"
		description="This milestone does not exist or was deleted."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to milestones</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Edit milestone"
		description="Milestones group issues toward a date and show progress."
		backHref={listHref}
		backLabel="Milestones"
	>
		<FormSection title="Milestone" description="Title, due date, and what it delivers.">
			<FormField label="Title" id="ms-title" required span="half">
				<Input id="ms-title" bind:value={fTitle} placeholder="v1.0" />
			</FormField>
			<FormField
				label="Due date"
				id="ms-due"
				help="Optional, overdue milestones are flagged in the list"
				span="third"
			>
				<Input id="ms-due" type="date" bind:value={fDue} />
			</FormField>
			<FormField label="Description" id="ms-desc">
				<Textarea
					id="ms-desc"
					bind:value={fDescription}
					rows={4}
					placeholder="What this milestone delivers"
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={save} disabled={saving || !fTitle.trim()}>
				{#if saving}<Loader2 class="animate-spin" />{/if}
				{saving ? 'Saving' : 'Save changes'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
