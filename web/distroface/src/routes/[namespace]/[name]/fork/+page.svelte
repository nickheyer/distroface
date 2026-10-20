<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { GitFork, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoHref = $derived(resolve('/[namespace]/[name]', { namespace, name }));
	const canFork = $derived(
		authStore.isAuthenticated && repo.merge?.allowForks !== false && !repo.isEmpty
	);
	const ownNamespace = $derived(authStore.user?.username ?? '');

	let targetNamespace = $state('');
	let targetName = $state(gitRepoStore.repo?.name ?? '');
	let forking = $state(false);

	const nameValid = $derived(
		/^[a-z0-9][a-z0-9._-]{0,99}$/.test(targetName.trim()) &&
			!/\.(git|wiki)$/.test(targetName.trim())
	);

	async function fork() {
		if (!nameValid || forking) return;
		forking = true;
		const owner = targetNamespace || ownNamespace;
		const resp = await rpcClient.git
			.forkGitRepository({
				namespace,
				name,
				targetNamespace: owner,
				targetName: targetName.trim()
			})
			.catch(() => undefined);
		forking = false;
		if (!resp) return;
		toast.success('Fork created');
		await goto(
			resolve('/[namespace]/[name]', {
				namespace: resp.repository?.namespace ?? owner,
				name: resp.repository?.name ?? targetName.trim()
			})
		);
	}
</script>

<svelte:head>
	<title>Fork - {namespace}/{name}</title>
</svelte:head>

{#if !authStore.isAuthenticated}
	<EmptyState
		icon={GitFork}
		message="Sign in to fork a repository"
		description="A fork lives under your account or an organization you belong to."
	>
		{#snippet actions()}
			<Button href={resolve('/login')}>Sign in</Button>
			<Button variant="outline" href={repoHref}>Back to repository</Button>
		{/snippet}
	</EmptyState>
{:else if !canFork}
	<EmptyState
		icon={GitFork}
		message="This repository cannot be forked"
		description={repo.isEmpty
			? 'There are no commits to copy yet.'
			: 'Forking is turned off in the repository settings.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={repoHref}>Back to repository</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Create a new fork"
		description="A fork copies every branch and tag of {namespace}/{name} into a repository you control, so you can push changes and open pull requests back upstream."
		backHref={repoHref}
		backLabel="{namespace}/{name}"
	>
		<FormSection title="Destination" description="Where the fork is created.">
			<div class="grid items-start gap-3 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
				<FormField
					label="Owner"
					id="fork-owner"
					required
					help="Your account or an organization you can create repositories in"
				>
					<AsyncSelect
						bind:selected={targetNamespace}
						placeholder={ownNamespace}
						searchPlaceholder="Search organizations"
						fetchPage={async (query, pageToken) => {
							const resp = await rpcClient.organization.listOrganizations({
								page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
							});
							const items = resp.organizations.map((o) => ({
								value: o.name,
								label: o.displayName || o.name
							}));
							if (
								!pageToken &&
								ownNamespace &&
								ownNamespace.toLowerCase().includes(query.toLowerCase())
							) {
								items.unshift({ value: ownNamespace, label: `${ownNamespace} (you)` });
							}
							return { items, nextPageToken: resp.page?.nextPageToken ?? '' };
						}}
					/>
				</FormField>
				<span class="hidden pt-7 text-xl text-muted-foreground/50 sm:block" aria-hidden="true"
					>/</span
				>
				<FormField
					label="Repository name"
					id="fork-name"
					required
					help="Defaults to the upstream name"
					error={targetName && !nameValid
						? 'Lowercase letters, digits, dots, dashes, underscores'
						: ''}
				>
					<Input
						id="fork-name"
						bind:value={targetName}
						placeholder={name}
						class="font-mono"
						aria-invalid={!!targetName && !nameValid}
						autocomplete="off"
						spellcheck="false"
					/>
				</FormField>
			</div>
		</FormSection>

		{#snippet footer()}
			<Button onclick={fork} disabled={forking || !nameValid}>
				{#if forking}<Loader2 class="animate-spin" />{/if}
				{forking ? 'Forking' : 'Create fork'}
			</Button>
			<Button variant="ghost-muted" href={repoHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
