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
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Users, Plus, Trash, Clock } from '$lib/icons';
	import { GitAccessLevel, type GitCollaborator } from '$lib/proto/distroface/v1/git_pb';

	let { namespace, name, addHref }: { namespace: string; name: string; addHref: string } = $props();

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

	onMount(load);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Collaborators</h2>
			<p class="section-subtitle">
				People with direct access, org teams are managed on the org page.
			</p>
		</div>
		<Button class="shrink-0" href={addHref}>
			<Plus />
			Add collaborator
		</Button>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-32" />
						<Skeleton class="h-3 w-24" />
					</div>
					<Skeleton class="h-8 w-28" />
				</div>
			{/each}
		</div>
	{:else if rows.length === 0}
		<EmptyState
			icon={Users}
			message="No collaborators yet"
			description="Invite people by username and pick their access level."
		>
			{#snippet actions()}
				<Button variant="outline" href={addHref}>
					<Plus />
					Add collaborator
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>User</TableHead>
						<TableHead>Access</TableHead>
						<TableHead class="hidden sm:table-cell">Added</TableHead>
						<TableHead class="w-12"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each rows as c (c.userId)}
						<TableRow>
							<TableCell>
								<div class="flex min-w-0 flex-wrap items-center gap-2">
									<div class="min-w-0">
										<p class="truncate text-sm font-medium">{c.username}</p>
										{#if c.displayName && c.displayName !== c.username}
											<p class="truncate text-xs text-muted-foreground">{c.displayName}</p>
										{/if}
									</div>
									{#if !c.accepted}
										<Badge variant="warning"><Clock />Pending</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell>
								<Select
									type="single"
									value={String(c.permission)}
									onValueChange={(v) => {
										if (v) void update(c, v);
									}}
									disabled={updating === c.userId}
								>
									<SelectTrigger class="w-28" aria-label="Access level"
										>{levelLabel(c.permission)}</SelectTrigger
									>
									<SelectContent>
										{#each levels as l (l.value)}
											<SelectItem value={String(l.value)}>{l.label}</SelectItem>
										{/each}
									</SelectContent>
								</Select>
							</TableCell>
							<TableCell class="hidden text-muted-foreground sm:table-cell">
								{c.createdAt ? relativeTime(timestampDate(c.createdAt)) : '-'}
							</TableCell>
							<TableCell class="text-right">
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="hover:text-destructive"
									aria-label="Remove {c.username}"
									onclick={() => {
										removeTarget = c;
										removeOpen = true;
									}}
								>
									<Trash />
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

<ConfirmDialog
	bind:open={removeOpen}
	title="Remove collaborator"
	confirmLabel="Remove"
	onConfirm={remove}
	loading={removing}
	icon={Trash}
>
	{#snippet description()}
		<strong>{removeTarget?.username}</strong> loses direct access to this repository.
	{/snippet}
</ConfirmDialog>
