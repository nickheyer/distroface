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
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { Archive, ArchiveRestore, ArrowRightLeft, Pencil, Trash2, Clock, X, Info } from '@lucide/svelte';
	import type { GitTransfer } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const isOwner = $derived(gitRepoStore.isOwner);

	let archiving = $state(false);

	let renameOpen = $state(false);
	let newName = $state('');
	let renaming = $state(false);

	let transferOpen = $state(false);
	let newNamespace = $state('');
	let transferring = $state(false);
	let pending = $state<GitTransfer[]>([]);
	let loadedFor = $state('');
	let rejecting = $state<string | null>(null);

	let deleteOpen = $state(false);
	let deleteLinked = $state(false);
	let confirmText = $state('');
	let deleting = $state(false);
	const confirmMatches = $derived(confirmText.trim() === `${namespace}/${name}`);

	const nameValid = $derived(/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(newName.trim()) && newName.trim() !== name);

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

	async function rename() {
		if (!nameValid) return;
		renaming = true;
		try {
			const resp = await rpcClient.git.moveGitRepository({ namespace, name, newName: newName.trim() });
			toast.success('Repository renamed');
			renameOpen = false;
			await goto(resolve('/[namespace]/[name]/settings/danger', {
				namespace: resp.repository?.namespace ?? namespace,
				name: resp.repository?.name ?? newName.trim()
			}));
		} catch {
			// Error interceptor already toasted
		} finally {
			renaming = false;
		}
	}

	async function transfer() {
		const ns = newNamespace.trim();
		if (!ns || ns === namespace) return;
		transferring = true;
		try {
			const resp = await rpcClient.git.moveGitRepository({ namespace, name, newNamespace: ns });
			transferOpen = false;
			if (resp.transfer) {
				toast.success(`Transfer to ${ns} is waiting for acceptance`);
				pending = [resp.transfer, ...pending.filter((t) => t.id !== resp.transfer?.id)];
			} else {
				toast.success(`Moved to ${ns}`);
				await goto(resolve('/[namespace]/[name]/settings/danger', {
					namespace: resp.repository?.namespace ?? ns,
					name: resp.repository?.name ?? name
				}));
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			transferring = false;
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
	<Skeleton class="h-72 w-full rounded-xl" />
{:else}
	<div class="space-y-4">
		<h2 class="section-title">Danger zone</h2>

		{#if !isOwner}
			<Alert>
				<Info class="h-4 w-4" />
				<AlertDescription>Only the repository owner can archive, rename, transfer, or delete it.</AlertDescription>
			</Alert>
		{/if}

		{#if pending.length > 0}
			<div class="rounded-xl border border-amber-500/40 overflow-hidden">
				<div class="px-6 py-3 border-b border-amber-500/30 bg-amber-500/5 flex items-center gap-2">
					<Clock class="h-4 w-4 text-amber-600 dark:text-amber-400" />
					<h3 class="text-sm font-semibold text-amber-700 dark:text-amber-300">Pending transfers</h3>
				</div>
				<div class="divide-y divide-border/40">
					{#each pending as t (t.id)}
						<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-3">
							<div class="min-w-0 text-sm">
								<p>To <strong>{t.toNamespace}</strong></p>
								<p class="text-xs text-muted-foreground">
									Requested {t.createdAt ? relativeTime(timestampDate(t.createdAt)) : ''}{t.expiresAt ? `, expires ${timestampDate(t.expiresAt).toLocaleDateString()}` : ''}
								</p>
							</div>
							<Button variant="outline" size="sm" class="h-8 shrink-0 self-start sm:self-auto" onclick={() => cancelTransfer(t)} disabled={rejecting === t.id}>
								<X class="h-3.5 w-3.5 mr-1" />{rejecting === t.id ? 'Cancelling...' : 'Cancel'}
							</Button>
						</div>
					{/each}
				</div>
			</div>
		{/if}

		<div class="rounded-xl border border-destructive/40 overflow-hidden">
			<div class="divide-y divide-border/40">
				<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-4">
					<div class="min-w-0">
						<p class="text-sm font-medium">{repo.isArchived ? 'Unarchive this repository' : 'Archive this repository'}</p>
						<p class="text-[13px] text-muted-foreground mt-0.5">
							{repo.isArchived ? 'Pushes, issues, and pull requests open up again.' : 'Read only for everyone, pushes and new issues are refused.'}
						</p>
					</div>
					<PermissionGate allowed={isOwner}>
						<Button variant="outline" class="shrink-0 self-start sm:self-auto text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive" onclick={toggleArchive} disabled={archiving}>
							{#if repo.isArchived}<ArchiveRestore class="h-4 w-4 mr-1.5" />{:else}<Archive class="h-4 w-4 mr-1.5" />{/if}
							{archiving ? 'Working...' : repo.isArchived ? 'Unarchive' : 'Archive'}
						</Button>
					</PermissionGate>
				</div>

				<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-4">
					<div class="min-w-0">
						<p class="text-sm font-medium">Rename</p>
						<p class="text-[13px] text-muted-foreground mt-0.5">Name linked packages move with it, old clone urls stop working.</p>
					</div>
					<PermissionGate allowed={isOwner}>
						<Button variant="outline" class="shrink-0 self-start sm:self-auto text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive" onclick={() => { newName = name; renameOpen = true; }}>
							<Pencil class="h-4 w-4 mr-1.5" />Rename
						</Button>
					</PermissionGate>
				</div>

				<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-4">
					<div class="min-w-0">
						<p class="text-sm font-medium">Transfer</p>
						<p class="text-[13px] text-muted-foreground mt-0.5">Move to another user or organization. Users must accept, organizations you administer move at once.</p>
					</div>
					<PermissionGate allowed={isOwner}>
						<Button variant="outline" class="shrink-0 self-start sm:self-auto text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive" onclick={() => { newNamespace = ''; transferOpen = true; }}>
							<ArrowRightLeft class="h-4 w-4 mr-1.5" />Transfer
						</Button>
					</PermissionGate>
				</div>

				<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-4">
					<div class="min-w-0">
						<p class="text-sm font-medium">Delete this repository</p>
						<p class="text-[13px] text-muted-foreground mt-0.5">Removes every branch, issue, pull request, release, and wiki page, permanently.</p>
					</div>
					<PermissionGate allowed={isOwner}>
						<Button variant="outline" class="shrink-0 self-start sm:self-auto text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive" onclick={() => { confirmText = ''; deleteLinked = false; deleteOpen = true; }}>
							<Trash2 class="h-4 w-4 mr-1.5" />Delete
						</Button>
					</PermissionGate>
				</div>
			</div>
		</div>
	</div>
{/if}

<FormPanel bind:open={renameOpen} title="Rename Repository" description="Redirects are not kept, update remotes afterwards." icon={Pencil}>
	<FormField label="New name" id="rename-name" required help="Letters, digits, dots, dashes, and underscores">
		<Input id="rename-name" bind:value={newName} class="font-mono text-sm" />
	</FormField>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (renameOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={rename} disabled={renaming || !nameValid}>{renaming ? 'Renaming...' : 'Rename'}</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={transferOpen} title="Transfer Repository" description="Collaborators and settings travel with it." icon={ArrowRightLeft}>
	<FormField label="New namespace" id="transfer-ns" required help="A username or organization name">
		<Input id="transfer-ns" bind:value={newNamespace} placeholder="org-or-user" class="font-mono text-sm" />
	</FormField>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (transferOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={transfer} disabled={transferring || !newNamespace.trim() || newNamespace.trim() === namespace}>
			{transferring ? 'Transferring...' : 'Transfer'}
		</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={deleteOpen} title="Delete Repository" description="This cannot be undone." icon={Trash2}>
	<div class="space-y-3">
		<FormField label="Also delete linked packages" horizontal help="Image and artifact repositories linked to this source are removed too. Leave off to keep them, the delete is refused while links exist">
			<Checkbox bind:checked={deleteLinked} />
		</FormField>
		<FormField label="Confirm" id="delete-confirm" required help="Type {namespace}/{name} to confirm">
			<Input id="delete-confirm" bind:value={confirmText} placeholder="{namespace}/{name}" class="font-mono text-sm" autocomplete="off" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (deleteOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={remove} disabled={deleting || !confirmMatches}>{deleting ? 'Deleting...' : 'Delete Repository'}</Button>
	{/snippet}
</FormPanel>
