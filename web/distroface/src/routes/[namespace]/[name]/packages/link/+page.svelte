<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
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
	import { LinkedRepositoryKind } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const listHref = $derived(resolve('/[namespace]/[name]/packages', { namespace, name }));

	const kinds: { value: LinkedRepositoryKind; label: string }[] = [
		{ value: LinkedRepositoryKind.IMAGE, label: 'Image' },
		{ value: LinkedRepositoryKind.ARTIFACT, label: 'Artifact' }
	];
	const kindLabel = (v: LinkedRepositoryKind) => kinds.find((k) => k.value === v)?.label ?? 'Image';

	let kind = $state<LinkedRepositoryKind>(
		page.url.searchParams.get('kind') === 'artifact'
			? LinkedRepositoryKind.ARTIFACT
			: LinkedRepositoryKind.IMAGE
	);
	let packageName = $state('');
	let linking = $state(false);
	let linkedKeys = $state<string[]>([]);

	// Non admins bounce back to the package list
	$effect(() => {
		if (!gitRepoStore.loading && gitRepoStore.repo && !gitRepoStore.canAdmin)
			void goto(resolve('/[namespace]/[name]/packages', { namespace, name }));
	});

	onMount(async () => {
		try {
			const resp = await rpcClient.git.listLinkedRepositories(
				{ namespace, name },
				silentCallOptions
			);
			linkedKeys = resp.repositories.map((l) => `${l.kind}:${l.fullName}`);
		} catch {
			linkedKeys = [];
		}
	});

	// Candidates are same namespace packages not linked here yet
	async function fetchCandidates(query: string, pageToken: string) {
		const pageReq = { pageSize: 20, pageToken, query: { text: query.trim(), filters: [] } };
		if (kind === LinkedRepositoryKind.IMAGE) {
			const resp = await rpcClient.repository.listRepositories(
				{ namespace, page: pageReq },
				silentCallOptions
			);
			return {
				items: resp.repositories
					.filter((r) => !linkedKeys.includes(`${LinkedRepositoryKind.IMAGE}:${r.fullName}`))
					.map((r) => ({
						value: r.name,
						label: r.fullName,
						description: r.sourceRepository ? `Linked to ${r.sourceRepository}` : undefined
					})),
				nextPageToken: resp.page?.nextPageToken ?? ''
			};
		}
		const resp = await rpcClient.artifact.listArtifactRepositories(
			{ namespace, page: pageReq },
			silentCallOptions
		);
		return {
			items: resp.repositories
				.filter(
					(r) => !linkedKeys.includes(`${LinkedRepositoryKind.ARTIFACT}:${namespace}/${r.name}`)
				)
				.map((r) => ({
					value: r.name,
					label: `${namespace}/${r.name}`,
					description: r.sourceRepository ? `Linked to ${r.sourceRepository}` : undefined
				})),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	async function link() {
		if (!packageName || linking) return;
		linking = true;
		try {
			await rpcClient.git.linkRepository({ namespace, name, kind, packageName });
			toast.success('Package linked');
			await goto(resolve('/[namespace]/[name]/packages', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			linking = false;
		}
	}
</script>

<svelte:head>
	<title>Link package - {namespace}/{name}</title>
</svelte:head>

{#if !gitRepoStore.repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<FormPage
		level="section"
		title="Link package"
		description="Access to the package follows this repository's packages unit."
		backHref={listHref}
		backLabel="Packages"
	>
		<FormSection title="Package" description="Only packages under {namespace} can be linked.">
			<FormField label="Kind" id="link-kind" span="half">
				<Select
					type="single"
					value={String(kind)}
					onValueChange={(v) => {
						if (v) {
							kind = Number(v) as LinkedRepositoryKind;
							packageName = '';
						}
					}}
				>
					<SelectTrigger id="link-kind" class="w-full">{kindLabel(kind)}</SelectTrigger>
					<SelectContent>
						{#each kinds as k (k.value)}
							<SelectItem value={String(k.value)}>{k.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
			<FormField label="Package" id="link-name" required span="half">
				{#key kind}
					<AsyncSelect
						bind:selected={packageName}
						placeholder="Choose a package"
						searchPlaceholder="Search packages"
						fetchPage={fetchCandidates}
					/>
				{/key}
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={link} disabled={linking || !packageName}>
				{#if linking}<Loader class="animate-spin" />{/if}
				{linking ? 'Linking' : 'Link package'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
