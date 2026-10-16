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
		Bell, Inbox, CheckCheck, Check, Pin, PinOff, MailOpen, CircleDot, GitPullRequest,
		GitCommitHorizontal, Tag, GitBranch, Users, ArrowRightLeft, Search
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { notificationStore } from '$lib/stores/notification.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { NotificationStatus, type Notification } from '$lib/proto/distroface/v1/notification_pb';

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
			await rpcClient.notification.pinNotification({ id: n.id, pinned: n.status !== NotificationStatus.PINNED });
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

	onMount(load);
</script>

<svelte:head>
	<title>Notifications - Distroface</title>
</svelte:head>

<PageHeader title="Notifications" subtitle="Activity on repositories you watch or take part in" icon={Bell}>
	{#snippet actions()}
		<Button variant="outline" size="sm" onclick={markAllRead} disabled={markingAll || notificationStore.unread === 0}>
			<CheckCheck class="h-4 w-4 mr-1.5" />
			{markingAll ? 'Marking...' : 'Mark all read'}
		</Button>
	{/snippet}
</PageHeader>

<div class="space-y-4">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<Tabs.Root value={view} onValueChange={changeView}>
			<Tabs.List>
				<Tabs.Trigger value="unread">
					Unread
					{#if notificationStore.unread > 0}
						<Badge variant="secondary" class="ml-1 h-4 px-1.5 text-[10px] tabular-nums">{notificationStore.unread}</Badge>
					{/if}
				</Tabs.Trigger>
				<Tabs.Trigger value="all">All</Tabs.Trigger>
			</Tabs.List>
		</Tabs.Root>
		<div class="relative w-full sm:w-72">
			<Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
			<Input
				bind:value={repoFilter}
				placeholder="Filter by repository, owner/name"
				aria-label="Filter by repository"
				class="pl-8 h-9"
				onkeydown={(e) => { if (e.key === 'Enter') applyRepoFilter(); }}
				onblur={applyRepoFilter}
			/>
		</div>
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 5 }, i (i)}
				<Skeleton class="h-16 w-full rounded-lg" />
			{/each}
		</div>
	{:else if notifications.length === 0}
		<EmptyState
			icon={Inbox}
			message={view === 'unread' ? 'All caught up' : 'No notifications'}
			description={view === 'unread' ? 'Unread and pinned notifications show here.' : 'Activity on watched repositories lands here.'}
		/>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each notifications as n (n.id)}
				{@const Icon = kindIcons[n.kind] ?? Bell}
				{@const unread = n.status === NotificationStatus.UNREAD}
				{@const pinned = n.status === NotificationStatus.PINNED}
				<div class="flex items-start gap-3 px-4 py-3 {unread ? 'bg-primary/[0.03]' : ''}">
					<div class="relative mt-0.5 h-8 w-8 rounded-lg bg-muted/60 flex items-center justify-center shrink-0">
						<Icon class="h-4 w-4 {unread ? 'text-primary' : 'text-muted-foreground'}" />
						{#if unread}
							<span class="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-primary"></span>
						{/if}
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex items-center gap-2 text-[12px] text-muted-foreground min-w-0">
							<span class="truncate">{n.repoFullName}</span>
							{#if subject(n)}
								<span class="font-mono">{subject(n)}</span>
							{/if}
							{#if pinned}
								<Pin class="h-3 w-3" />
							{/if}
						</div>
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						<a href={n.htmlUrl || '#'} class="block text-sm mt-0.5 truncate hover:text-primary transition-colors {unread ? 'font-semibold' : 'font-medium'}" onclick={() => open(n)}>
							{n.title || n.kind}
						</a>
						<p class="text-[12px] text-muted-foreground mt-1">
							{#if n.reason}
								<Badge variant="outline" class="text-[10px] h-4 px-1.5 mr-1.5 capitalize">{n.reason}</Badge>
							{/if}
							{#if n.updatedBy}
								{n.updatedBy}
							{/if}
							{#if n.updatedAt}
								<span class="mx-1">/</span>{relativeTime(timestampDate(n.updatedAt))}
							{/if}
						</p>
					</div>
					<div class="flex items-center gap-0.5 shrink-0">
						<Button
							variant="ghost" size="icon" class="h-7 w-7"
							disabled={!!busy[n.id]}
							onclick={() => toggleRead(n)}
							aria-label={unread ? 'Mark read' : 'Mark unread'}
							title={unread ? 'Mark read' : 'Mark unread'}
						>
							{#if unread}
								<Check class="h-3.5 w-3.5" />
							{:else}
								<MailOpen class="h-3.5 w-3.5 text-muted-foreground" />
							{/if}
						</Button>
						<Button
							variant="ghost" size="icon" class="h-7 w-7"
							disabled={!!busy[n.id]}
							onclick={() => togglePin(n)}
							aria-label={pinned ? 'Unpin' : 'Pin'}
							title={pinned ? 'Unpin' : 'Pin'}
						>
							{#if pinned}
								<PinOff class="h-3.5 w-3.5" />
							{:else}
								<Pin class="h-3.5 w-3.5 text-muted-foreground" />
							{/if}
						</Button>
					</div>
				</div>
			{/each}
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>
