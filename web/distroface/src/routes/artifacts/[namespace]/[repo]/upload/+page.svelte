<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader2, Plus, Upload, X } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { formatBytes } from '$lib/utils';

	const SESSION_KEY = 'distroface_session';
	const namespace = $derived(page.params.namespace ?? '');
	const repoName = $derived(page.params.repo ?? '');
	const repoHref = $derived(
		resolve('/artifacts/[namespace]/[repo]', { namespace, repo: repoName })
	);
	const canPush = $derived(authStore.hasPermission('artifacts', 'push'));

	let uploadFile = $state<File | null>(null);
	let uploadVersion = $state('');
	let uploadPath = $state('');
	let uploadProps = $state<{ key: string; value: string }[]>([]);
	let uploading = $state(false);
	let uploadStage = $state('');

	function onFileSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		uploadFile = input.files?.[0] ?? null;
		if (uploadFile && !uploadPath) uploadPath = uploadFile.name;
	}

	async function doUpload() {
		if (!uploadFile || !uploadVersion.trim() || uploading) return;
		uploading = true;
		try {
			uploadStage = 'Initiating';
			const init = await rpcClient.artifact.initiateArtifactUpload({ repoName, namespace });

			uploadStage = 'Uploading';
			const token = localStorage.getItem(SESSION_KEY);
			const patchResp = await fetch(init.uploadUrl, {
				method: 'PATCH',
				headers: token ? { Authorization: `Bearer ${token}` } : {},
				body: uploadFile
			});
			if (!patchResp.ok) {
				toast.error(`Upload failed (${patchResp.status})`);
				return;
			}

			uploadStage = 'Finalizing';
			const properties: Record<string, string> = {};
			for (const row of uploadProps) {
				if (row.key.trim()) properties[row.key.trim()] = row.value;
			}
			await rpcClient.artifact.completeArtifactUpload({
				repoName,
				namespace,
				uploadId: init.uploadId,
				version: uploadVersion.trim(),
				path: uploadPath.trim(),
				properties
			});
			toast.success('Artifact uploaded');
			await goto(resolve('/artifacts/[namespace]/[repo]', { namespace, repo: repoName }));
		} catch {
			return;
		} finally {
			uploading = false;
			uploadStage = '';
		}
	}
</script>

<svelte:head>
	<title>Upload artifact - {namespace}/{repoName} - Distroface</title>
</svelte:head>

{#snippet propertyRows(
	rows: { key: string; value: string }[],
	onChange: (rows: { key: string; value: string }[]) => void
)}
	<div class="space-y-2">
		{#each rows as row, i (i)}
			<div class="flex items-center gap-2">
				<Input bind:value={row.key} placeholder="key" class="font-mono" aria-label="Property key" />
				<Input
					bind:value={row.value}
					placeholder="value"
					class="font-mono"
					aria-label="Property value"
				/>
				<Button
					variant="ghost-muted"
					size="icon"
					class="shrink-0"
					onclick={() => onChange(rows.filter((_, j) => j !== i))}
					aria-label="Remove property"
				>
					<X />
				</Button>
			</div>
		{/each}
	</div>
	<Button variant="outline" size="sm" onclick={() => onChange([...rows, { key: '', value: '' }])}>
		<Plus />
		Add property
	</Button>
{/snippet}

{#if !canPush}
	<EmptyState
		icon={Upload}
		message="You cannot upload artifacts"
		description="Ask an administrator for the artifacts push permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={repoHref}>Back to {repoName}</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Upload artifact"
		description="Re-uploading the same version and path replaces the artifact."
		backHref={repoHref}
		backLabel="{namespace}/{repoName}"
	>
		<FormSection title="File" description="What to upload and where it lands.">
			<FormField
				label="File"
				id="upload-file"
				required
				help={uploadFile ? `${uploadFile.name}, ${formatBytes(uploadFile.size)}` : undefined}
			>
				<Input id="upload-file" type="file" onchange={onFileSelected} />
			</FormField>
			<FormField label="Version" id="upload-version" required span="half">
				<Input
					id="upload-version"
					bind:value={uploadVersion}
					placeholder="1.0.0"
					class="font-mono"
				/>
			</FormField>
			<FormField label="Path" id="upload-path" help="Defaults to the file name" span="half">
				<Input
					id="upload-path"
					bind:value={uploadPath}
					placeholder="dist/app.zip"
					class="font-mono"
				/>
			</FormField>
		</FormSection>

		<FormSection title="Properties" description="Key value pairs stored with the file">
			{@render propertyRows(uploadProps, (rows) => (uploadProps = rows))}
		</FormSection>

		{#snippet footer()}
			<Button onclick={doUpload} disabled={uploading || !uploadFile || !uploadVersion.trim()}>
				{#if uploading}<Loader2 class="animate-spin" />{/if}
				{uploading ? uploadStage || 'Uploading' : 'Upload artifact'}
			</Button>
			<Button variant="ghost-muted" href={repoHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
