<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import StatCard from '$lib/components/stat-card.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Info } from '$lib/icons';
	import { Act } from '$lib/act.svelte';
	import { formatBytes } from '$lib/utils';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const act = new Act();

	let enabled = $state(false);
	let seededFor = $state('');

	$effect(() => {
		if (repo && repo.fullName !== seededFor) {
			seededFor = repo.fullName;
			enabled = repo.lfsEnabled;
		}
	});

	async function toggle(v: boolean) {
		enabled = v;
		const ok = await act.run(async () => {
			await rpcClient.git.updateGitRepository({ namespace, name, lfsEnabled: v });
			await gitRepoStore.reload();
		});
		if (!ok) enabled = !v;
	}

	const trackExample = $derived(
		`git lfs install\ngit lfs track "*.psd"\ngit add .gitattributes\ngit push`
	);
</script>

{#if !repo}
	<Skeleton class="h-40 w-full rounded-lg" />
{:else}
	<FormPage
		level="section"
		title="Git LFS"
		description="Large files tracked by pointer and stored outside git history."
	>
		{#if !configStore.lfsEnabled}
			<Alert>
				<Info />
				<AlertDescription>
					Large file storage is turned off for this instance. An administrator can enable it under
					Admin, Git.
				</AlertDescription>
			</Alert>
		{/if}

		<FormSection
			title="Large file storage"
			description="Pointer files in git, blobs in the artifact store."
		>
			<FormField
				label="Enable LFS"
				layout="toggle"
				tag={act.tag}
				error={act.error}
				help="Clients with git-lfs upload tracked files through the batch API"
			>
				<Switch
					checked={enabled}
					disabled={act.busy || !configStore.lfsEnabled}
					onCheckedChange={toggle}
				/>
			</FormField>
			<div class="grid gap-4 sm:grid-cols-2">
				<StatCard label="LFS objects" value={formatBytes(Number(repo.lfsSizeBytes))} />
				<StatCard label="Git objects" value={formatBytes(Number(repo.sizeBytes))} />
			</div>
			{#if enabled}
				<div class="space-y-2">
					<p class="eyebrow">Track files locally</p>
					<div class="flex items-start gap-2">
						<pre
							class="code-inline min-w-0 flex-1 leading-relaxed whitespace-pre-wrap">{trackExample}</pre>
						<CopyButton text={trackExample} label="Commands copied" />
					</div>
				</div>
			{/if}
		</FormSection>
	</FormPage>
{/if}
