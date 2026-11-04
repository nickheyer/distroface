<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader, Shield } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import ProtectionForm, {
		emptyProtection,
		protectionFrom,
		protectionInit,
		type ProtectionForm as ProtectionFormValues
	} from '$lib/components/git/protection-form.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const repo = $derived(gitRepoStore.repo);
	const listHref = $derived(resolve('/[namespace]/[name]/settings/branches', { namespace, name }));

	let form = $state<ProtectionFormValues>(emptyProtection());
	let found = $state(false);
	let loaded = $state(false);
	let orgId = $state('');
	let loadedFor = $state('');
	let saving = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
			if (repo.isOrgNamespace) void loadOrg();
		}
	});

	async function load() {
		const resp = await rpcClient.gitRefs
			.listBranchProtections({ namespace, name }, silentCallOptions)
			.catch(() => null);
		const rule = resp?.protections.find((p) => p.id === id);
		if (rule) form = protectionFrom(rule);
		found = !!rule;
		loaded = true;
	}

	async function loadOrg() {
		const resp = await rpcClient.organization
			.getOrganization({ name: namespace }, silentCallOptions)
			.catch(() => null);
		orgId = resp?.organization?.id ?? '';
	}

	async function save() {
		if (!form.pattern.trim() || saving) return;
		saving = true;
		const ok = await rpcClient.gitRefs
			.setBranchProtection({ namespace, name, protection: protectionInit(form) })
			.then(
				() => true,
				() => false
			);
		saving = false;
		if (!ok) return;
		toast.success('Rule updated');
		await goto(resolve('/[namespace]/[name]/settings/branches', { namespace, name }));
	}
</script>

<svelte:head>
	<title>Edit rule - {namespace}/{name}</title>
</svelte:head>

{#if !loaded}
	<div class="box divide-y">
		{#each { length: 2 }, i (i)}
			<div class="space-y-2 px-4 py-3">
				<Skeleton class="h-4 w-40" />
				<Skeleton class="h-3 w-64" />
			</div>
		{/each}
	</div>
{:else if !found}
	<EmptyState
		icon={Shield}
		message="Rule not found"
		description="It may have been deleted or belong to another repository."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to branch protection</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Edit rule"
		description="Applied on push and on merge."
		backHref={listHref}
		backLabel="Branch protection"
	>
		<ProtectionForm bind:form {orgId} />

		{#snippet footer()}
			<Button onclick={save} disabled={saving || !form.pattern.trim()}>
				{#if saving}<Loader class="animate-spin" />{/if}
				{saving ? 'Saving' : 'Save changes'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
