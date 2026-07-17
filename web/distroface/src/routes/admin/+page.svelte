<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import StatCard from '$lib/components/stat-card.svelte';
	import { Users, Package, Building2, Key, Shield, Globe, HardDrive, Archive } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { User, Repository } from '$lib/proto/distroface/v1/types_pb';
	import type { GetStorageUsageResponse } from '$lib/proto/distroface/v1/configuration_pb';

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
					{ label: 'Local Auth', enabled: authConfig.localEnabled, icon: Shield },
					{ label: 'SSO / OIDC', enabled: authConfig.oidcEnabled, icon: Globe },
					{ label: 'Registration', enabled: authConfig.registrationEnabled, icon: Users },
					{ label: 'Anonymous Access', enabled: authConfig.anonymousAccess, icon: Package }
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
				rpcClient.auth.getAuthConfig({}, silentCallOptions)
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
			storageUsage = await rpcClient.configuration.getStorageUsage({}, silentCallOptions);
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
				<Skeleton class="h-18 rounded-xl" />
			{/each}
		</div>
		<div class="grid gap-6 lg:grid-cols-2">
			<Skeleton class="h-64 rounded-xl" />
			<Skeleton class="h-64 rounded-xl" />
		</div>
	</div>
{:else}
	<div class="space-y-6">
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			<StatCard label="Users" value={userCount} icon={Users} href="/admin/users" />
			<StatCard label="Repositories" value={repoCount} icon={Package} />
			<StatCard label="Organizations" value={orgCount} icon={Building2} />
			<StatCard label="Roles" value={roleCount} icon={Key} href="/admin/roles" />
		</div>

		<div class="grid gap-6 lg:grid-cols-2">
			<div class="rounded-xl border border-border/60 overflow-hidden">
				<div class="px-4 py-3 bg-muted/20 border-b border-border/40">
					<h3 class="text-sm font-semibold">Recent Users</h3>
				</div>
				{#if recentUsers.length === 0}
					<div class="px-4 py-10 text-center text-sm text-muted-foreground">
						No users yet
					</div>
				{:else}
					<div class="divide-y divide-border/40">
						{#each recentUsers as user (user.id)}
							<a
								href={resolve(`/${user.username}`)}
								class="flex items-center gap-3 px-4 py-3 hover:bg-muted/20 transition-colors"
							>
								<Avatar class="h-8 w-8">
									<AvatarFallback class="text-[10px] bg-primary/10 text-primary font-medium">
										{getInitials(user.displayName || user.username)}
									</AvatarFallback>
								</Avatar>
								<div class="flex-1 min-w-0">
									<span class="text-sm font-medium block truncate">{user.username}</span>
									<span class="text-xs text-muted-foreground">
										{user.roles.map((r) => r.name).join(', ')}
									</span>
								</div>
								<span class="text-xs text-muted-foreground shrink-0">
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
					<div class="rounded-xl border border-border/60 overflow-hidden">
						<div class="px-4 py-3 bg-muted/20 border-b border-border/40">
							<h3 class="text-sm font-semibold">Authentication</h3>
						</div>
						<div class="divide-y divide-border/40">
							{#each authStatusItems as item (item.label)}
								<div class="flex items-center gap-3 px-4 py-2.5">
									<item.icon class="h-4 w-4 text-muted-foreground" />
									<span class="text-sm flex-1">{item.label}</span>
									<div class="flex items-center gap-1.5">
										<span class="status-dot {item.enabled ? 'status-dot-active' : 'status-dot-inactive'}"></span>
										<span class="text-xs text-muted-foreground">{item.enabled ? 'On' : 'Off'}</span>
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/if}

				<div class="rounded-xl border border-border/60 overflow-hidden">
					<div class="px-4 py-3 bg-muted/20 border-b border-border/40">
						<h3 class="text-sm font-semibold">Recent Repositories</h3>
					</div>
					{#if recentRepos.length === 0}
						<div class="px-4 py-10 text-center text-sm text-muted-foreground">
							No repositories yet
						</div>
					{:else}
						<div class="divide-y divide-border/40">
							{#each recentRepos as repo (repo.id)}
								<a
									href={resolve(`/${repo.namespace}/${repo.name}`)}
									class="flex items-center gap-3 px-4 py-3 hover:bg-muted/20 transition-colors"
								>
									<Package class="h-4 w-4 text-muted-foreground shrink-0" />
									<span class="text-sm font-medium truncate flex-1">{repo.fullName}</span>
									<span class="text-xs text-muted-foreground shrink-0">
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
			<div class="rounded-xl border border-border/60 overflow-hidden">
				<div class="px-4 py-3 bg-muted/20 border-b border-border/40">
					<h3 class="text-sm font-semibold">Storage</h3>
				</div>
				<div class="grid lg:grid-cols-2 divide-y lg:divide-y-0 lg:divide-x divide-border/40">
					<div>
						<div class="flex items-center gap-3 px-4 py-2.5 border-b border-border/40 bg-muted/10">
							<HardDrive class="h-4 w-4 text-muted-foreground" />
							<span class="text-sm font-medium flex-1">Registry Images</span>
							<span class="text-sm font-semibold">{formatBytes(Number(storageUsage.registryBytes))}</span>
						</div>
						{#if storageUsage.registryNamespaces.length === 0}
							<div class="px-4 py-6 text-center text-sm text-muted-foreground">No image data</div>
						{:else}
							<div class="divide-y divide-border/40">
								{#each storageUsage.registryNamespaces as ns (ns.name)}
									<div class="flex items-center gap-3 px-4 py-2.5">
										<span class="text-sm truncate flex-1">{ns.name}</span>
										<span class="text-xs text-muted-foreground shrink-0">
											{ns.count} {ns.count === 1 ? 'repo' : 'repos'}
										</span>
										<span class="text-xs font-medium shrink-0 w-20 text-right">{formatBytes(Number(ns.bytes))}</span>
									</div>
								{/each}
							</div>
						{/if}
					</div>
					<div>
						<div class="flex items-center gap-3 px-4 py-2.5 border-b border-border/40 bg-muted/10">
							<Archive class="h-4 w-4 text-muted-foreground" />
							<span class="text-sm font-medium flex-1">Artifacts</span>
							<span class="text-sm font-semibold">{formatBytes(Number(storageUsage.artifactBytes))}</span>
						</div>
						{#if storageUsage.artifactRepos.length === 0}
							<div class="px-4 py-6 text-center text-sm text-muted-foreground">No artifact data</div>
						{:else}
							<div class="divide-y divide-border/40">
								{#each storageUsage.artifactRepos as repo (repo.name)}
									<div class="flex items-center gap-3 px-4 py-2.5">
										<span class="text-sm truncate flex-1">{repo.name}</span>
										<span class="text-xs text-muted-foreground shrink-0">
											{repo.count} {repo.count === 1 ? 'artifact' : 'artifacts'}
										</span>
										<span class="text-xs font-medium shrink-0 w-20 text-right">{formatBytes(Number(repo.bytes))}</span>
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
