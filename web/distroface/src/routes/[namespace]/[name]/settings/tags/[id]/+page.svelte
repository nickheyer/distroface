<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader2, Tag } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PrincipalPicker from '$lib/components/git/principal-picker.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const repo = $derived(gitRepoStore.repo);
	const listHref = $derived(resolve('/[namespace]/[name]/settings/tags', { namespace, name }));

	let pattern = $state('');
	let blockDeletion = $state(true);
	let allowedPushers = $state<string[]>([]);
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
			.listTagProtections({ namespace, name }, silentCallOptions)
			.catch(() => null);
		const rule = resp?.protections.find((p) => p.id === id);
		if (rule) {
			pattern = rule.pattern;
			blockDeletion = rule.blockDeletion;
			allowedPushers = [...rule.allowedPushers];
		}
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
		if (!pattern.trim() || saving) return;
		saving = true;
		const ok = await rpcClient.gitRefs
			.setTagProtection({
				namespace,
				name,
				protection: { id, pattern: pattern.trim(), blockDeletion, allowedPushers }
			})
			.then(
				() => true,
				() => false
			);
		saving = false;
		if (!ok) return;
		toast.success('Rule updated');
		await goto(resolve('/[namespace]/[name]/settings/tags', { namespace, name }));
	}
</script>

<svelte:head>
	<title>Edit tag rule - {namespace}/{name}</title>
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
		icon={Tag}
		message="Rule not found"
		description="It may have been deleted or belong to another repository."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to tag protection</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Edit tag rule"
		description="Checked on every tag push."
		backHref={listHref}
		backLabel="Tag protection"
	>
		<FormSection title="Rule" description="Which tags it covers and who may push them.">
			<FormField
				label="Tag pattern"
				id="tag-pattern"
				required
				help="Glob over tag names, v* or release-*"
				span="half"
			>
				<Input
					id="tag-pattern"
					bind:value={pattern}
					placeholder="v*"
					class="font-mono"
					autocomplete="off"
				/>
			</FormField>
			<FormField label="Block deletion" layout="toggle">
				<Switch bind:checked={blockDeletion} />
			</FormField>
			<FormField label="Allowed pushers" help="Empty allows every writer">
				<PrincipalPicker bind:selected={allowedPushers} {orgId} />
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={save} disabled={saving || !pattern.trim()}>
				{#if saving}<Loader2 class="animate-spin" />{/if}
				{saving ? 'Saving' : 'Save changes'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
