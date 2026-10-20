<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Tabs from '$lib/components/ui/tabs';
	import PageHeader from '$lib/components/page-header.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import {
		Bell,
		Inbox,
		CheckCheck,
		Check,
		Pin,
		PinOff,
		MailOpen,
		CircleDot,
		GitPullRequest,
		GitCommitHorizontal,
		Tag,
		GitBranch,
		Users,
		ArrowRightLeft,
		Search
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { notificationStore } from '$lib/stores/notification.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { NotificationStatus, type Notification } from '$lib/proto/distroface/v1/notification_pb';
	import type { GitTransfer } from '$lib/proto/distroface/v1/git_pb';

	const kindIcons: Record<string, typeof Bell> = {
		issue: CircleDot,
		pull: GitPullRequest,
		commit: GitCommitHorizontal,
		release: Tag,
		repository: GitBranch,
		collaboration: Users,
		transfer: ArrowRightLeft
	};

	let notifications = $state<Notification[]>([]);
	// Repository transfers addressed to the viewer, accepted or declined here
	let transfers = $state<GitTransfer[]>([]);
	let transferBusy = $state('');

	async function loadTransfers() {
		try {
			const resp = await rpcClient.git.listTransfers({ page: { pageSize: 50 } });
			transfers = resp.transfers;
		} catch {
			transfers = [];
		}
	}

	async function acceptTransfer(t: GitTransfer) {
		transferBusy = t.id;
		try {
			const resp = await rpcClient.git.acceptTransfer({ id: t.id });
			transfers = transfers.filter((x) => x.id !== t.id);
			toast.success(`${t.fullName} is now ${t.toNamespace}/${resp.repository?.name ?? ''}`);
			if (resp.repository)
				await goto(resolve(`/${resp.repository.namespace}/${resp.repository.name}`));
		} catch {
			// error interceptor
		} finally {
			transferBusy = '';
		}
	}

	async function rejectTransfer(t: GitTransfer) {
		transferBusy = t.id;
		try {
			await rpcClient.git.rejectTransfer({ id: t.id });
			transfers = transfers.filter((x) => x.id !== t.id);
			toast.success('Transfer declined');
		} catch {
			// error interceptor
		} finally {
			transferBusy = '';
		}
	}
	const pager = new Pager(25);
	let loading = $state(true);
	let loaded = $state(false);
	let view = $state<'unread' | 'all'>('unread');
	let repoFilter = $state('');
	let busy = $state<Record<string, boolean>>({});
	let markingAll = $state(false);
	let seenSeq = notificationStore.seq;

	$effect(() => {
		if (!authStore.loading && !authStore.isAuthenticated) goto(resolve('/login'));
	});

	// Stream events refresh the list in place
	$effect(() => {
		const seq = notificationStore.seq;
		if (loaded && seq !== seenSeq) {
			seenSeq = seq;
			untrack(() => load());
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.notification.listNotifications({
				page: pager.request(),
				all: view === 'all',
				repoFullName: repoFilter.trim()
			});
			notifications = resp.notifications;
			pager.apply(resp.page);
			notificationStore.set(resp.unreadCount);
		} catch {
			notifications = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function changeView(v: string) {
		view = v === 'all' ? 'all' : 'unread';
		pager.reset();
		load();
	}

	function applyRepoFilter() {
		pager.reset();
		load();
	}

	async function toggleRead(n: Notification) {
		busy[n.id] = true;
		try {
			const unread = n.status !== NotificationStatus.UNREAD;
			const resp = await rpcClient.notification.markRead({ ids: [n.id], unread });
			notificationStore.set(resp.unreadCount);
			await load();
		} catch {
			// error interceptor
		} finally {
			delete busy[n.id];
		}
	}

	async function togglePin(n: Notification) {
		busy[n.id] = true;
		try {
			await rpcClient.notification.pinNotification({
				id: n.id,
				pinned: n.status !== NotificationStatus.PINNED
			});
			await load();
		} catch {
			// error interceptor
		} finally {
			delete busy[n.id];
		}
	}

	async function markAllRead() {
		markingAll = true;
		try {
			const resp = await rpcClient.notification.markAllRead({ repoFullName: repoFilter.trim() });
			notificationStore.set(resp.unreadCount);
			toast.success('All caught up');
			await load();
		} catch {
			// error interceptor
		} finally {
			markingAll = false;
		}
	}

	// Opening a notification reads it
	async function open(n: Notification) {
		if (n.status === NotificationStatus.UNREAD) {
			try {
				const resp = await rpcClient.notification.markRead({ ids: [n.id], unread: false });
				notificationStore.set(resp.unreadCount);
			} catch {
				// Navigation proceeds regardless
			}
		}
	}

	function subject(n: Notification): string {
		const num = Number(n.number);
		if (num > 0) return `#${num}`;
		if (n.sha) return n.sha.slice(0, 7);
		return '';
	}

	onMount(() => {
		load();
		loadTransfers();
	});
</script>

<svelte:head>
	<title>Notifications - Distroface</title>
</svelte:head>

<PageHeader
	title="Notifications"
	subtitle="Activity on repositories you watch or take part in"
	icon={Bell}
>
	{#snippet actions()}
		<Button
			variant="outline"
			onclick={markAllRead}
			disabled={markingAll || notificationStore.unread === 0}
		>
			<CheckCheck />
			{markingAll ? 'Marking' : 'Mark all read'}
		</Button>
	{/snippet}
</PageHeader>

<div class="space-y-4">
	{#if transfers.length > 0}
		<div class="box divide-y">
			{#each transfers as t (t.id)}
				<div class="flex flex-wrap items-center gap-3 px-4 py-3">
					<ArrowRightLeft class="size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-48 flex-1 text-sm">
						<span class="font-medium">{t.requestedBy}</span> wants to transfer
						<span class="font-medium">{t.fullName}</span>
						to <span class="font-medium">{t.toNamespace}</span>
						{#if t.expiresAt}
							<span class="ml-1 text-xs text-muted-foreground"
								>expires {relativeTime(timestampDate(t.expiresAt))}</span
							>
						{/if}
					</div>
					<div class="flex items-center gap-2">
						<Button size="sm" onclick={() => acceptTransfer(t)} disabled={transferBusy === t.id}
							>Accept</Button
						>
						<Button
							size="sm"
							variant="outline"
							onclick={() => rejectTransfer(t)}
							disabled={transferBusy === t.id}>Decline</Button
						>
					</div>
				</div>
			{/each}
		</div>
	{/if}
	<div class="flex flex-wrap items-center justify-between gap-3">
		<Tabs.Root value={view} onValueChange={changeView}>
			<Tabs.List>
				<Tabs.Trigger value="unread">
					Unread
					{#if notificationStore.unread > 0}
						<Badge class="tabular-nums">{notificationStore.unread}</Badge>
					{/if}
				</Tabs.Trigger>
				<Tabs.Trigger value="all">All</Tabs.Trigger>
			</Tabs.List>
		</Tabs.Root>
		<div class="relative w-full sm:w-72">
			<Search class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
			<Input
				bind:value={repoFilter}
				placeholder="Filter by repository, owner/name"
				aria-label="Filter by repository"
				class="pl-8"
				onkeydown={(e) => {
					if (e.key === 'Enter') applyRepoFilter();
				}}
				onblur={applyRepoFilter}
			/>
		</div>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 5 }, i (i)}
				<div class="flex items-start gap-3 px-4 py-3">
					<Skeleton class="mt-0.5 size-4 rounded-sm" />
					<div class="flex-1 space-y-2">
						<Skeleton class="h-3 w-40" />
						<Skeleton class="h-4 w-72" />
						<Skeleton class="h-3 w-32" />
					</div>
				</div>
			{/each}
		</div>
	{:else if notifications.length === 0}
		<EmptyState
			icon={Inbox}
			message={view === 'unread' ? 'All caught up' : 'No notifications'}
			description={view === 'unread'
				? 'Unread and pinned notifications show here.'
				: 'Activity on watched repositories lands here.'}
		/>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each notifications as n (n.id)}
				{@const Icon = kindIcons[n.kind] ?? Bell}
				{@const unread = n.status === NotificationStatus.UNREAD}
				{@const pinned = n.status === NotificationStatus.PINNED}
				<div class="flex items-start gap-3 px-4 py-3 {unread ? 'bg-primary/3' : ''}">
					<div class="relative mt-0.5 shrink-0">
						<Icon class="size-4 {unread ? 'text-primary' : 'text-muted-foreground'}" />
						{#if unread}
							<span class="absolute -top-1 -right-1 size-2 rounded-full bg-primary"></span>
						{/if}
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
							<span class="truncate">{n.repoFullName}</span>
							{#if subject(n)}
								<span class="font-mono">{subject(n)}</span>
							{/if}
							{#if pinned}
								<Pin class="size-3" />
							{/if}
						</div>
						<!-- eslint-disable svelte/no-navigation-without-resolve -->
						<a
							href={n.htmlUrl || '#'}
							class="mt-0.5 block truncate text-sm transition-colors hover:text-primary {unread
								? 'font-semibold'
								: 'font-medium'}"
							onclick={() => open(n)}
						>
							{n.title || n.kind}
						</a>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
						<p class="mt-1 text-xs text-muted-foreground">
							{#if n.reason}
								<Badge variant="outline" class="mr-1.5 capitalize">{n.reason}</Badge>
							{/if}
							{#if n.updatedBy}
								{n.updatedBy}
							{/if}
							{#if n.updatedAt}
								<span class="mx-1 text-muted-foreground/50">/</span>{relativeTime(
									timestampDate(n.updatedAt)
								)}
							{/if}
						</p>
					</div>
					<div class="flex shrink-0 items-center gap-0.5">
						<Button
							variant="ghost-muted"
							size="icon-sm"
							disabled={!!busy[n.id]}
							onclick={() => toggleRead(n)}
							aria-label={unread ? 'Mark read' : 'Mark unread'}
							title={unread ? 'Mark read' : 'Mark unread'}
						>
							{#if unread}
								<Check />
							{:else}
								<MailOpen />
							{/if}
						</Button>
						<Button
							variant="ghost-muted"
							size="icon-sm"
							disabled={!!busy[n.id]}
							onclick={() => togglePin(n)}
							aria-label={pinned ? 'Unpin' : 'Pin'}
							title={pinned ? 'Unpin' : 'Pin'}
						>
							{#if pinned}
								<PinOff />
							{:else}
								<Pin />
							{/if}
						</Button>
					</div>
				</div>
			{/each}
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>
