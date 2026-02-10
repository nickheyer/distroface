<script lang="ts">
	import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { onMount } from 'svelte';
	import { CheckCircle, XCircle, Home } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';

	let healthStatus = $state<{ status: string; version: string } | null>(null);
	let healthError = $state<string | null>(null);
	let isLoading = $state(true);

	async function checkHealth() {
		try {
			const response = await rpcClient.health.check({});
			healthStatus = { status: response.status, version: response.version };
			healthError = null;
		} catch (error: any) {
			healthError = error.message || 'Failed to connect';
			healthStatus = null;
		} finally {
			isLoading = false;
		}
	}

	onMount(() => {
		checkHealth();
	});
</script>

<div class="flex-1 space-y-6 h-full p-6">
	<div class="flex items-center gap-4 pb-4 border-b border-border/40">
		<div class="h-14 w-14 rounded-2xl bg-gradient-to-br from-primary/20 to-primary/10 flex items-center justify-center shadow-lg">
			<Home class="h-7 w-7 text-primary" />
		</div>
		<div class="space-y-1">
			<h2 class="text-3xl font-bold tracking-tight">Distroface</h2>
			<p class="text-sm text-muted-foreground">Welcome to your application</p>
		</div>
	</div>

	<div class="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
		<Card class="border-border/50 hover:border-primary/30 transition-all hover:shadow-lg">
			<CardHeader class="flex flex-row items-center justify-between space-y-0 pb-2">
				<CardTitle class="text-sm font-medium">API Health</CardTitle>
				{#if isLoading}
					<div class="h-5 w-5 border-2 border-primary/30 border-t-primary rounded-full animate-spin"></div>
				{:else if healthStatus}
					<CheckCircle class="h-5 w-5 text-green-500" />
				{:else}
					<XCircle class="h-5 w-5 text-red-500" />
				{/if}
			</CardHeader>
			<CardContent>
				{#if isLoading}
					<p class="text-sm text-muted-foreground">Checking...</p>
				{:else if healthStatus}
					<div class="flex items-center gap-2">
						<Badge variant="outline" class="bg-green-500/10 text-green-500 border-green-500/20">
							{healthStatus.status}
						</Badge>
						<span class="text-xs text-muted-foreground">v{healthStatus.version}</span>
					</div>
				{:else}
					<p class="text-sm text-red-500">{healthError}</p>
				{/if}
			</CardContent>
		</Card>
	</div>
</div>
