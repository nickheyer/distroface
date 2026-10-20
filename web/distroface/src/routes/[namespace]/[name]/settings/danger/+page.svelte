<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import {
		Archive,
		ArchiveRestore,
		ArrowRightLeft,
		Pencil,
		Trash2,
		Clock,
		X,
		Info,
		Loader2
	} from '@lucide/svelte';
	import type { GitTransfer } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const isOwner = $derived(gitRepoStore.isOwner);

	let archiving = $state(false);

	const renameHref = $derived(
		resolve('/[namespace]/[name]/settings/danger/rename', { namespace, name })
	);
	const transferHref = $derived(
		resolve('/[namespace]/[name]/settings/danger/transfer', { namespace, name })
	);
	let pending = $state<GitTransfer[]>([]);
	let loadedFor = $state('');
	let rejecting = $state<string | null>(null);

	let deleteOpen = $state(false);
	let deleteLinked = $state(false);
	let confirmText = $state('');
	let deleting = $state(false);
	const confirmMatches = $derived(confirmText.trim() === `${namespace}/${name}`);

	$effect(() => {
		if (repo && repo.id !== loadedFor) {
			loadedFor = repo.id;
			void loadTransfers();
		}
	});

	// Transfers addressed to the caller, filtered to this repository
	async function loadTransfers() {
		try {
			const resp = await rpcClient.git.listTransfers({ page: { pageSize: 50 } }, silentCallOptions);
			pending = resp.transfers.filter((t) => t.repoId === repo?.id);
		} catch {
			pending = [];
		}
	}

	async function toggleArchive() {
		if (!repo) return;
		archiving = true;
		try {
			if (repo.isArchived) await rpcClient.git.unarchiveGitRepository({ namespace, name });
			else await rpcClient.git.archiveGitRepository({ namespace, name });
			toast.success(repo.isArchived ? 'Repository unarchived' : 'Repository archived');
			await gitRepoStore.reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			archiving = false;
		}
	}

	async function cancelTransfer(t: GitTransfer) {
		rejecting = t.id;
		try {
			await rpcClient.git.rejectTransfer({ id: t.id });
			toast.success('Transfer cancelled');
			pending = pending.filter((p) => p.id !== t.id);
		} catch {
			// Error interceptor already toasted
		} finally {
			rejecting = null;
		}
	}

	async function remove() {
		if (!confirmMatches) return;
		deleting = true;
		try {
			await rpcClient.git.deleteGitRepository({ namespace, name, deleteLinked });
			toast.success('Repository deleted');
			deleteOpen = false;
			gitRepoStore.clear();
			await goto(resolve('/[namespace]', { namespace }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<div class="space-y-6">
		<div>
			<h2 class="section-title">Danger zone</h2>
			<p class="section-subtitle">Archive, rename, transfer, or delete this repository.</p>
		</div>

		{#if !isOwner}
			<Alert>
				<Info />
				<AlertDescription
					>Only the repository owner can archive, rename, transfer, or delete it.</AlertDescription
				>
			</Alert>
		{/if}

		{#if pending.length > 0}
			<div class="box">
				<div class="box-header">
					<span class="flex items-center gap-2"
						><Clock class="size-4 text-warning" />Pending transfers</span
					>
				</div>
				<div class="divide-y">
					{#each pending as t (t.id)}
						<div
							class="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
						>
							<div class="min-w-0 text-sm">
								<p>To <strong>{t.toNamespace}</strong></p>
								<p class="text-xs text-muted-foreground">
									Requested {t.createdAt
										? relativeTime(timestampDate(t.createdAt))
										: ''}{t.expiresAt
										? `, expires ${timestampDate(t.expiresAt).toLocaleDateString()}`
										: ''}
								</p>
							</div>
							<Button
								variant="outline"
								size="sm"
								class="shrink-0 self-start sm:self-auto"
								onclick={() => cancelTransfer(t)}
								disabled={rejecting === t.id}
							>
								{#if rejecting === t.id}<Loader2 class="animate-spin" />{:else}<X />{/if}
								{rejecting === t.id ? 'Cancelling' : 'Cancel'}
							</Button>
						</div>
					{/each}
				</div>
			</div>
		{/if}

		<div class="box divide-y">
			<div class="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
				<div class="min-w-0">
					<p class="text-sm font-medium">
						{repo.isArchived ? 'Unarchive this repository' : 'Archive this repository'}
					</p>
					<p class="mt-0.5 text-sm text-muted-foreground">
						{repo.isArchived
							? 'Pushes, issues, and pull requests open up again.'
							: 'Read only for everyone, pushes and new issues are refused.'}
					</p>
				</div>
				<PermissionGate allowed={isOwner}>
					<Button
						variant="outline-destructive"
						class="shrink-0 self-start sm:self-auto"
						onclick={toggleArchive}
						disabled={archiving}
					>
						{#if archiving}<Loader2 class="animate-spin" />{:else if repo.isArchived}<ArchiveRestore
							/>{:else}<Archive />{/if}
						{archiving ? 'Working' : repo.isArchived ? 'Unarchive' : 'Archive'}
					</Button>
				</PermissionGate>
			</div>

			<div class="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
				<div class="min-w-0">
					<p class="text-sm font-medium">Rename</p>
					<p class="mt-0.5 text-sm text-muted-foreground">
						Name linked packages move with it, old clone urls redirect until the name is reused.
					</p>
				</div>
				<PermissionGate allowed={isOwner}>
					<Button
						variant="outline-destructive"
						class="shrink-0 self-start sm:self-auto"
						href={renameHref}
					>
						<Pencil />Rename
					</Button>
				</PermissionGate>
			</div>

			<div class="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
				<div class="min-w-0">
					<p class="text-sm font-medium">Transfer</p>
					<p class="mt-0.5 text-sm text-muted-foreground">
						Move to another user or organization. Users must accept, organizations you administer
						move at once.
					</p>
				</div>
				<PermissionGate allowed={isOwner}>
					<Button
						variant="outline-destructive"
						class="shrink-0 self-start sm:self-auto"
						href={transferHref}
					>
						<ArrowRightLeft />Transfer
					</Button>
				</PermissionGate>
			</div>

			<div class="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
				<div class="min-w-0">
					<p class="text-sm font-medium">Delete this repository</p>
					<p class="mt-0.5 text-sm text-muted-foreground">
						Removes every branch, issue, pull request, release, and wiki page, permanently.
					</p>
				</div>
				<PermissionGate allowed={isOwner}>
					<Button
						variant="outline-destructive"
						class="shrink-0 self-start sm:self-auto"
						onclick={() => {
							confirmText = '';
							deleteLinked = false;
							deleteOpen = true;
						}}
					>
						<Trash2 />Delete
					</Button>
				</PermissionGate>
			</div>
		</div>
	</div>
{/if}

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete repository"
	confirmLabel={deleting ? 'Deleting' : 'Delete repository'}
	confirmDisabled={!confirmMatches}
	onConfirm={remove}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Removes every branch, issue, pull request, release, and wiki page of
		<strong class="font-mono">{namespace}/{name}</strong>, permanently.
	{/snippet}
	<FormField
		label="Also delete linked packages"
		layout="toggle"
		help="Image and artifact repositories linked to this source are removed too. Leave off to keep them, the delete is refused while links exist"
	>
		<Checkbox bind:checked={deleteLinked} />
	</FormField>
	<FormField label="Confirm" id="delete-confirm" required help="Type {namespace}/{name} to confirm">
		<Input
			id="delete-confirm"
			bind:value={confirmText}
			placeholder="{namespace}/{name}"
			class="font-mono"
			autocomplete="off"
		/>
	</FormField>
</ConfirmDialog>
