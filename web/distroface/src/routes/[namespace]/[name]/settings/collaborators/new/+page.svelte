<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { GitAccessLevel } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const listHref = $derived(
		resolve('/[namespace]/[name]/settings/collaborators', { namespace, name })
	);

	const levels: { value: GitAccessLevel; label: string; help: string }[] = [
		{ value: GitAccessLevel.READ, label: 'Read', help: 'Clone and view every unit' },
		{ value: GitAccessLevel.WRITE, label: 'Write', help: 'Push, triage, and publish packages' },
		{ value: GitAccessLevel.ADMIN, label: 'Admin', help: 'Settings, protections, collaborators' }
	];
	const levelLabel = (v: GitAccessLevel) => levels.find((l) => l.value === v)?.label ?? 'Owner';

	let username = $state('');
	let level = $state<GitAccessLevel>(GitAccessLevel.WRITE);
	let adding = $state(false);

	async function searchUsers(query: string, pageToken: string) {
		const resp = await rpcClient.user.searchUsers(
			{ page: { pageSize: 20, pageToken, query: { text: query.trim(), filters: [] } } },
			silentCallOptions
		);
		return {
			items: resp.users.map((u) => ({
				value: u.username,
				label: u.username,
				description: u.displayName !== u.username ? u.displayName : undefined
			})),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	async function add() {
		if (!username || adding) return;
		adding = true;
		try {
			await rpcClient.git.addCollaborator({ namespace, name, username, permission: level });
			toast.success('Invitation sent');
			await goto(resolve('/[namespace]/[name]/settings/collaborators', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			adding = false;
		}
	}
</script>

<svelte:head>
	<title>Add collaborator - {namespace}/{name}</title>
</svelte:head>

{#if !gitRepoStore.repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<FormPage
		level="section"
		title="Add collaborator"
		description="The user receives an invitation to accept."
		backHref={listHref}
		backLabel="Collaborators"
	>
		<FormSection title="Access" description="Who gets direct access and at what level.">
			<FormField label="User" id="collab-user" required span="half">
				<AsyncSelect
					bind:selected={username}
					placeholder="Search users"
					searchPlaceholder="Username or name"
					fetchPage={searchUsers}
				/>
			</FormField>
			<FormField
				label="Access level"
				id="collab-level"
				help={levels.find((l) => l.value === level)?.help}
				span="half"
			>
				<Select
					type="single"
					value={String(level)}
					onValueChange={(v) => {
						if (v) level = Number(v) as GitAccessLevel;
					}}
				>
					<SelectTrigger id="collab-level" class="w-full">{levelLabel(level)}</SelectTrigger>
					<SelectContent>
						{#each levels as l (l.value)}
							<SelectItem value={String(l.value)}>{l.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={add} disabled={adding || !username}>
				{#if adding}<Loader class="animate-spin" />{/if}
				{adding ? 'Adding' : 'Add collaborator'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
