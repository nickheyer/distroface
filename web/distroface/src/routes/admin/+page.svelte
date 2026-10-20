<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import StatCard from '$lib/components/stat-card.svelte';
	import {
		Users,
		Package,
		Building2,
		Key,
		Shield,
		Globe,
		HardDrive,
		Archive
	} from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { User, Repository } from '$lib/proto/distroface/v1/types_pb';
	import type { GetStorageUsageResponse } from '$lib/proto/distroface/v1/gc_pb';

	let loading = $state(true);
	let userCount = $state(0);
	let repoCount = $state(0);
	let orgCount = $state(0);
	let roleCount = $state(0);
	let recentUsers = $state<User[]>([]);
	let recentRepos = $state<Repository[]>([]);
	let storageUsage = $state<GetStorageUsageResponse | null>(null);
	let authConfig = $state<{
		localEnabled: boolean;
		oidcEnabled: boolean;
		registrationEnabled: boolean;
		anonymousAccess: boolean;
	} | null>(null);

	const authStatusItems = $derived(
		authConfig
			? [
					{ label: 'Local auth', enabled: authConfig.localEnabled, icon: Shield },
					{ label: 'SSO / OIDC', enabled: authConfig.oidcEnabled, icon: Globe },
					{ label: 'Registration', enabled: authConfig.registrationEnabled, icon: Users },
					{ label: 'Anonymous access', enabled: authConfig.anonymousAccess, icon: Package }
				]
			: []
	);

	function getInitials(name: string): string {
		return name
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	async function loadDashboard() {
		loading = true;
		try {
			const [usersResp, reposResp, orgsResp, rolesResp, configResp] = await Promise.all([
				rpcClient.user.listUsers({ page: { pageSize: 5 } }, silentCallOptions),
				rpcClient.repository.listRepositories({ page: { pageSize: 5 } }, silentCallOptions),
				rpcClient.organization.listOrganizations({ page: { pageSize: 1 } }, silentCallOptions),
				rpcClient.role.listRoles({ page: { pageSize: 1 } }, silentCallOptions),
				rpcClient.auth.getAuthStatus({}, silentCallOptions)
			]);

			userCount = Number(usersResp.page?.totalCount ?? 0n);
			recentUsers = usersResp.users;
			repoCount = Number(reposResp.page?.totalCount ?? 0n);
			recentRepos = reposResp.repositories;
			orgCount = Number(orgsResp.page?.totalCount ?? 0n);
			roleCount = Number(rolesResp.page?.totalCount ?? 0n);
			authConfig = {
				localEnabled: configResp.localEnabled,
				oidcEnabled: configResp.oidcEnabled,
				registrationEnabled: configResp.registrationEnabled,
				anonymousAccess: configResp.anonymousAccess
			};
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}

		try {
			storageUsage = await rpcClient.gc.getStorageUsage({}, silentCallOptions);
		} catch {
			// storage scan is best-effort
		}
	}

	onMount(loadDashboard);
</script>

{#if loading}
	<div class="space-y-6">
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			{#each { length: 4 }, i (i)}
				<Skeleton class="h-18 rounded-lg" />
			{/each}
		</div>
		<div class="grid gap-6 lg:grid-cols-2">
			<Skeleton class="h-64 rounded-lg" />
			<Skeleton class="h-64 rounded-lg" />
		</div>
	</div>
{:else}
	<div class="space-y-6">
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			<StatCard label="Users" value={userCount} icon={Users} href="/admin/users" />
			<StatCard label="Image repositories" value={repoCount} icon={Package} />
			<StatCard label="Organizations" value={orgCount} icon={Building2} />
			<StatCard label="Roles" value={roleCount} icon={Key} href="/admin/roles" />
		</div>

		<div class="grid gap-6 lg:grid-cols-2">
			<div class="box">
				<h3 class="box-header">Recent users</h3>
				{#if recentUsers.length === 0}
					<div class="box-empty">No users yet</div>
				{:else}
					<div class="divide-y">
						{#each recentUsers as user (user.id)}
							<a
								href={resolve(`/${user.username}`)}
								class="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
							>
								<Avatar class="size-7">
									<AvatarFallback>{getInitials(user.displayName || user.username)}</AvatarFallback>
								</Avatar>
								<div class="min-w-0 flex-1">
									<span class="block truncate text-sm font-medium">{user.username}</span>
									<span class="text-xs text-muted-foreground">
										{user.roles.map((r) => r.name).join(', ')}
									</span>
								</div>
								<span class="shrink-0 text-xs text-muted-foreground">
									{#if user.createdAt}
										{relativeTime(timestampDate(user.createdAt))}
									{/if}
								</span>
							</a>
						{/each}
					</div>
				{/if}
			</div>

			<div class="space-y-6">
				{#if authConfig}
					<div class="box">
						<h3 class="box-header">Authentication</h3>
						<div class="divide-y">
							{#each authStatusItems as item (item.label)}
								<div class="flex items-center gap-3 px-4 py-2.5">
									<item.icon class="size-4 text-muted-foreground" />
									<span class="flex-1 text-sm">{item.label}</span>
									<div class="flex items-center gap-1.5">
										<span
											class="status-dot {item.enabled
												? 'status-dot-active'
												: 'status-dot-inactive'}"
										></span>
										<span class="text-xs text-muted-foreground">{item.enabled ? 'On' : 'Off'}</span>
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/if}

				<div class="box">
					<h3 class="box-header">Recent image repositories</h3>
					{#if recentRepos.length === 0}
						<div class="box-empty">No image repositories yet</div>
					{:else}
						<div class="divide-y">
							{#each recentRepos as repo (repo.id)}
								<a
									href={resolve(`/${repo.namespace}/${repo.name}`)}
									class="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
								>
									<Package class="size-4 shrink-0 text-muted-foreground" />
									<span class="flex-1 truncate text-sm font-medium">{repo.fullName}</span>
									<span class="shrink-0 text-xs text-muted-foreground">
										{#if repo.lastPushedAt}
											{relativeTime(timestampDate(repo.lastPushedAt))}
										{/if}
									</span>
								</a>
							{/each}
						</div>
					{/if}
				</div>
			</div>
		</div>

		{#if storageUsage}
			<div class="box">
				<h3 class="box-header">Storage</h3>
				<div class="grid divide-y lg:grid-cols-2 lg:divide-x lg:divide-y-0">
					<div>
						<div class="flex items-center gap-3 border-b px-4 py-2.5">
							<HardDrive class="size-4 text-muted-foreground" />
							<span class="flex-1 text-sm font-medium">Registry images</span>
							<span class="text-sm font-semibold tabular-nums"
								>{formatBytes(Number(storageUsage.registryBytes))}</span
							>
						</div>
						{#if storageUsage.registryNamespaces.length === 0}
							<div class="box-empty">No image data</div>
						{:else}
							<div class="divide-y">
								{#each storageUsage.registryNamespaces as ns (ns.name)}
									<div class="flex items-center gap-3 px-4 py-2.5">
										<span class="flex-1 truncate text-sm">{ns.name}</span>
										<span class="shrink-0 text-xs text-muted-foreground">
											{ns.count}
											{ns.count === 1 ? 'repo' : 'repos'}
										</span>
										<span class="w-20 shrink-0 text-right text-xs font-medium tabular-nums"
											>{formatBytes(Number(ns.bytes))}</span
										>
									</div>
								{/each}
							</div>
						{/if}
					</div>
					<div>
						<div class="flex items-center gap-3 border-b px-4 py-2.5">
							<Archive class="size-4 text-muted-foreground" />
							<span class="flex-1 text-sm font-medium">Artifacts</span>
							<span class="text-sm font-semibold tabular-nums"
								>{formatBytes(Number(storageUsage.artifactBytes))}</span
							>
						</div>
						{#if storageUsage.artifactRepos.length === 0}
							<div class="box-empty">No artifact data</div>
						{:else}
							<div class="divide-y">
								{#each storageUsage.artifactRepos as repo (repo.name)}
									<div class="flex items-center gap-3 px-4 py-2.5">
										<span class="flex-1 truncate text-sm">{repo.name}</span>
										<span class="shrink-0 text-xs text-muted-foreground">
											{repo.count}
											{repo.count === 1 ? 'artifact' : 'artifacts'}
										</span>
										<span class="w-20 shrink-0 text-right text-xs font-medium tabular-nums"
											>{formatBytes(Number(repo.bytes))}</span
										>
									</div>
								{/each}
							</div>
						{/if}
					</div>
				</div>
			</div>
		{/if}
	</div>
{/if}
