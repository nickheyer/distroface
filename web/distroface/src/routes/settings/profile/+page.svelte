<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Badge } from '$lib/components/ui/badge';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Save, Loader2 } from '@lucide/svelte';
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

<FormPage
	level="section"
	title="Profile"
	description="How you appear to other people on this instance."
>
	{#if loading}
		<div class="space-y-6">
			<div class="flex items-center gap-4">
				<Skeleton class="size-12 rounded-full" />
				<div class="space-y-2">
					<Skeleton class="h-4 w-32" />
					<Skeleton class="h-3.5 w-24" />
				</div>
			</div>
			<Skeleton class="h-48 w-full rounded-lg" />
		</div>
	{:else}
		<FormSection title="Account" description="Sign-in method and roles granted to you.">
			<div class="flex items-center gap-4 rounded-lg border bg-card p-4">
				<Avatar class="size-12">
					<AvatarFallback class="text-base">{getInitials()}</AvatarFallback>
				</Avatar>
				<div class="min-w-0 flex-1">
					<p class="text-sm font-semibold">{authStore.user?.username}</p>
					<div class="mt-1 flex flex-wrap items-center gap-1.5">
						<Badge variant="outline">
							{authStore.user?.authProvider === 'local'
								? 'Local account'
								: authStore.user?.authProvider}
						</Badge>
						{#each authStore.user?.roles ?? [] as role (role.id)}
							<Badge variant="secondary">{role.name}</Badge>
						{/each}
					</div>
				</div>
			</div>
		</FormSection>

		<FormSection title="Personal information" description="Shown on your profile and repositories.">
			<form
				class="form-fields"
				onsubmit={(e) => {
					e.preventDefault();
					save();
				}}
			>
				<FormField label="Display name" id="display-name" span="half">
					<Input id="display-name" bind:value={displayName} placeholder="Your display name" />
				</FormField>
				<FormField
					label="Email"
					id="email"
					help="Used for notifications, never shown publicly"
					span="half"
				>
					<Input id="email" type="email" bind:value={email} placeholder="you@example.com" />
				</FormField>
				<div class="flex flex-wrap items-center gap-2 pt-1">
					<Button type="submit" disabled={saving}>
						{#if saving}
							<Loader2 class="animate-spin" />
						{:else}
							<Save />
						{/if}
						Save changes
					</Button>
				</div>
			</form>
		</FormSection>
	{/if}
</FormPage>
