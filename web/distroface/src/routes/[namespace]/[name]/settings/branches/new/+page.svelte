<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import FormPage from '$lib/components/form-page.svelte';
	import ProtectionForm, {
		emptyProtection,
		protectionInit,
		type ProtectionForm as ProtectionFormValues
	} from '$lib/components/git/protection-form.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const listHref = $derived(resolve('/[namespace]/[name]/settings/branches', { namespace, name }));

	let form = $state<ProtectionFormValues>(emptyProtection());
	let orgId = $state('');
	let loadedFor = $state('');
	let saving = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void prefillDefaultBranch(repo.defaultBranch);
			if (repo.isOrgNamespace) void loadOrg();
		}
	});

	async function prefillDefaultBranch(defaultBranch: string) {
		const resp = await rpcClient.gitRefs
			.listBranchProtections({ namespace, name }, silentCallOptions)
			.catch(() => null);
		if (resp && resp.protections.length === 0 && !form.pattern) form.pattern = defaultBranch;
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
		toast.success('Rule created');
		await goto(resolve('/[namespace]/[name]/settings/branches', { namespace, name }));
	}
</script>

<svelte:head>
	<title>New protection rule - {namespace}/{name}</title>
</svelte:head>

<FormPage
	level="section"
	title="New protection rule"
	description="Applied on push and on merge."
	backHref={listHref}
	backLabel="Branch protection"
>
	<ProtectionForm bind:form {orgId} />

	{#snippet footer()}
		<Button onclick={save} disabled={saving || !form.pattern.trim()}>
			{#if saving}<Loader2 class="animate-spin" />{/if}
			{saving ? 'Saving' : 'Create rule'}
		</Button>
		<Button variant="ghost-muted" href={listHref}>Cancel</Button>
	{/snippet}
</FormPage>
