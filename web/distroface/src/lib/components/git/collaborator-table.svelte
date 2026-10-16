<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Users, Plus, Trash2, Clock } from '@lucide/svelte';
	import { GitAccessLevel, type GitCollaborator } from '$lib/proto/distroface/v1/git_pb';

	let { namespace, name }: { namespace: string; name: string } = $props();

	const levels: { value: GitAccessLevel; label: string; help: string }[] = [
		{ value: GitAccessLevel.READ, label: 'Read', help: 'Clone and view every unit' },
		{ value: GitAccessLevel.WRITE, label: 'Write', help: 'Push, triage, and publish packages' },
		{ value: GitAccessLevel.ADMIN, label: 'Admin', help: 'Settings, protections, collaborators' }
	];
	const levelLabel = (v: GitAccessLevel) => levels.find((l) => l.value === v)?.label ?? 'Owner';

	let rows = $state<GitCollaborator[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);

	let addOpen = $state(false);
	let addUsername = $state('');
	let addLevel = $state<GitAccessLevel>(GitAccessLevel.WRITE);
	let adding = $state(false);

	let removeOpen = $state(false);
	let removeTarget = $state<GitCollaborator | null>(null);
	let removing = $state(false);
	let updating = $state<string | null>(null);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.git.listCollaborators(
				{ namespace, name, page: pager.request() },
				silentCallOptions
			);
			rows = resp.collaborators;
			pager.apply(resp.page);
		} catch {
			rows = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function add() {
		if (!addUsername) return;
		adding = true;
		try {
			await rpcClient.git.addCollaborator({ namespace, name, username: addUsername, permission: addLevel });
			toast.success('Invitation sent');
			addOpen = false;
			addUsername = '';
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			adding = false;
		}
	}

	async function update(c: GitCollaborator, v: string) {
		const permission = Number(v) as GitAccessLevel;
		if (permission === c.permission) return;
		updating = c.userId;
		try {
			await rpcClient.git.updateCollaborator({ namespace, name, username: c.username, permission });
			toast.success(`${c.username} is now ${levelLabel(permission).toLowerCase()}`);
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			updating = null;
		}
	}

	async function remove() {
		if (!removeTarget) return;
		removing = true;
		try {
			await rpcClient.git.removeCollaborator({ namespace, name, username: removeTarget.username });
			toast.success('Collaborator removed');
			removeOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			removing = false;
		}
	}

	async function searchUsers(query: string, pageToken: string) {
		const resp = await rpcClient.user.searchUsers(
			{ page: { pageSize: 20, pageToken, query: { text: query.trim(), filters: [] } } },
			silentCallOptions
		);
		return {
			items: resp.users.map((u) => ({
				value: u.username,
				label: u.username,
				description: u.displayName !== u.username ? u.displayName : undefined
			})),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	onMount(load);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Collaborators</h2>
			<p class="section-subtitle">People with direct access, org teams are managed on the org page.</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={() => (addOpen = true)}>
			<Plus class="h-4 w-4 mr-1.5" />Add Collaborator
		</Button>
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if rows.length === 0}
		<EmptyState icon={Users} message="No collaborators" description="Invite people by username and pick their access level.">
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={() => (addOpen = true)}>
					<Plus class="h-4 w-4 mr-1.5" />Add Collaborator
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead class="th">User</TableHead>
						<TableHead class="th">Access</TableHead>
						<TableHead class="th hidden sm:table-cell">Added</TableHead>
						<TableHead class="th w-12"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each rows as c (c.userId)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-2 min-w-0 flex-wrap">
									<div class="min-w-0">
										<p class="text-sm font-medium truncate">{c.username}</p>
										{#if c.displayName && c.displayName !== c.username}
											<p class="text-xs text-muted-foreground truncate">{c.displayName}</p>
										{/if}
									</div>
									{#if !c.accepted}
										<Badge variant="outline" class="text-[10px] gap-1 border-amber-500/30 text-amber-600 dark:text-amber-400">
											<Clock class="h-2.5 w-2.5" />Pending
										</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell class="py-3 px-3">
								<Select
									type="single"
									value={String(c.permission)}
									onValueChange={(v) => { if (v) void update(c, v); }}
									disabled={updating === c.userId}
								>
									<SelectTrigger class="h-8 w-28 text-[13px]" size="sm">{levelLabel(c.permission)}</SelectTrigger>
									<SelectContent>
										{#each levels as l (l.value)}
											<SelectItem value={String(l.value)}>{l.label}</SelectItem>
										{/each}
									</SelectContent>
								</Select>
							</TableCell>
							<TableCell class="py-3 px-3 text-sm text-muted-foreground hidden sm:table-cell">
								{c.createdAt ? relativeTime(timestampDate(c.createdAt)) : '-'}
							</TableCell>
							<TableCell class="py-3 px-3 text-right">
								<Button
									variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive"
									aria-label="Remove {c.username}"
									onclick={() => { removeTarget = c; removeOpen = true; }}
								>
									<Trash2 class="h-3.5 w-3.5" />
								</Button>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<FormPanel bind:open={addOpen} title="Add Collaborator" description="The user receives an invitation to accept." icon={Users}>
	<div class="space-y-3">
		<FormField label="User" id="collab-user" required>
			<AsyncSelect bind:selected={addUsername} placeholder="Search users..." searchPlaceholder="Username or name" fetchPage={searchUsers} />
		</FormField>
		<FormField label="Access level" id="collab-level" help={levels.find((l) => l.value === addLevel)?.help}>
			<Select type="single" value={String(addLevel)} onValueChange={(v) => { if (v) addLevel = Number(v) as GitAccessLevel; }}>
				<SelectTrigger id="collab-level" class="w-full">{levelLabel(addLevel)}</SelectTrigger>
				<SelectContent>
					{#each levels as l (l.value)}
						<SelectItem value={String(l.value)}>{l.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (addOpen = false)}>Cancel</Button>
		<Button onclick={add} disabled={adding || !addUsername}>{adding ? 'Adding...' : 'Add Collaborator'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={removeOpen} title="Remove Collaborator" confirmLabel="Remove" onConfirm={remove} loading={removing} icon={Trash2}>
	{#snippet description()}
		<strong>{removeTarget?.username}</strong> loses direct access to this repository.
	{/snippet}
</ConfirmDialog>
