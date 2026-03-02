<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Badge } from '$lib/components/ui/badge';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { User, Save } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';

	let displayName = $state('');
	let email = $state('');
	let loading = $state(true);
	let saving = $state(false);

	function getInitials(): string {
		const name = displayName || authStore.user?.username || '?';
		return name
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.user.getUser({
				username: authStore.user?.username ?? ''
			});
			if (resp.user) {
				displayName = resp.user.displayName;
				email = resp.user.email;
			}
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function save() {
		saving = true;
		try {
			await rpcClient.user.updateUser({
				displayName: displayName || undefined,
				email: email || undefined
			});
			await authStore.validateSession();
			toast.success('Profile updated');
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}

	onMount(load);
</script>

<div class="space-y-6">
	<div>
		<h2 class="section-title">Profile</h2>
		<p class="section-subtitle">Update your personal information.</p>
	</div>

	{#if loading}
		<div class="space-y-6">
			<div class="flex items-center gap-4">
				<Skeleton class="h-16 w-16 rounded-full" />
				<div class="space-y-2">
					<Skeleton class="h-5 w-32" />
					<Skeleton class="h-4 w-24" />
				</div>
			</div>
			<Skeleton class="h-40 w-full rounded-xl" />
		</div>
	{:else}
		<div class="flex items-center gap-4 p-5 rounded-xl border border-border/60 bg-card">
			<Avatar class="h-14 w-14">
				<AvatarFallback class="text-lg bg-primary/10 text-primary font-semibold">
					{getInitials()}
				</AvatarFallback>
			</Avatar>
			<div class="flex-1 min-w-0">
				<p class="font-semibold">{authStore.user?.username}</p>
				<div class="flex items-center gap-2 mt-1">
					<Badge variant="outline" class="text-xs">
						{authStore.user?.authProvider === 'local'
							? 'Local account'
							: authStore.user?.authProvider}
					</Badge>
					{#each authStore.user?.roles ?? [] as role}
						<Badge
							variant="secondary"
							class="text-xs"
						>{role}</Badge>
					{/each}
				</div>
			</div>
		</div>

		<form
			onsubmit={(e) => {
				e.preventDefault();
				save();
			}}
		>
			<FormCard title="Personal Information" icon={User}>
				<div class="space-y-3">
					<FormField
						label="Display Name"
						id="display-name"
						help="Shown on your profile and alongside your repositories."
					>
						<Input
							id="display-name"
							bind:value={displayName}
							placeholder="Your display name"
						/>
					</FormField>

					<FormField
						label="Email"
						id="email"
						help="Used for account notifications. Not publicly visible."
					>
						<Input
							id="email"
							type="email"
							bind:value={email}
							placeholder="you@example.com"
						/>
					</FormField>
				</div>
				{#snippet footer()}
					<Button type="submit" disabled={saving} class="gap-2">
						<Save class="h-4 w-4" />
						{saving ? 'Saving...' : 'Save Changes'}
					</Button>
				{/snippet}
			</FormCard>
		</form>
	{/if}
</div>
