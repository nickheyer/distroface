<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Database, Info, Terminal } from '@lucide/svelte';
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
	<Skeleton class="h-40 w-full rounded-xl" />
{:else}
	<div class="space-y-4">
		<h2 class="section-title">Git LFS</h2>

		{#if !configStore.lfsEnabled}
			<Alert>
				<Info class="h-4 w-4" />
				<AlertDescription>
					Large file storage is turned off for this instance. An administrator can enable it under Admin, Git.
				</AlertDescription>
			</Alert>
		{/if}

		<FormCard title="Large file storage" description="Pointer files in git, blobs in the artifact store." icon={Database}>
			<div class="space-y-4">
				<FormField
					label="Enable LFS"
					horizontal
					tag={act.tag}
					error={act.error}
					help="Clients with git-lfs upload tracked files through the batch API"
				>
					<Switch checked={enabled} disabled={act.busy || !configStore.lfsEnabled} onCheckedChange={toggle} />
				</FormField>

				<div class="grid gap-4 sm:grid-cols-2">
					<div class="rounded-lg border border-border/60 p-4">
						<p class="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">LFS objects</p>
						<p class="text-lg font-semibold tabular-nums mt-0.5">{formatBytes(Number(repo.lfsSizeBytes))}</p>
					</div>
					<div class="rounded-lg border border-border/60 p-4">
						<p class="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">Git objects</p>
						<p class="text-lg font-semibold tabular-nums mt-0.5">{formatBytes(Number(repo.sizeBytes))}</p>
					</div>
				</div>

				{#if enabled}
					<div class="rounded-lg border border-border/50 bg-muted/30 overflow-hidden">
						<div class="flex items-center gap-2 px-3 py-1.5 border-b border-border/40">
							<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
							<span class="flex-1 text-xs font-medium text-muted-foreground">Track files locally</span>
							<CopyButton text={trackExample} label="Commands copied!" />
						</div>
						<pre class="px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all select-all">{trackExample}</pre>
					</div>
				{/if}
			</div>
		</FormCard>
	</div>
{/if}
