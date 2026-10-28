<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Badge } from '$lib/components/ui/badge';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Save, Loader, Upload, Trash } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';

	// Matches the server cap
	const AVATAR_MAX_BYTES = 1024 * 1024;

	let displayName = $state('');
	let email = $state('');
	let loading = $state(true);
	let saving = $state(false);
	let avatarBusy = $state(false);
	let fileInput = $state<HTMLInputElement | null>(null);

	const isLocal = $derived(authStore.user?.authProvider === 'local');
	const avatarUrl = $derived(authStore.user?.avatarUrl ?? '');

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

	async function uploadAvatar(ev: Event) {
		const input = ev.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = '';
		if (!file) return;
		if (file.size > AVATAR_MAX_BYTES) {
			toast.error('Pick an image under 1 MB');
			return;
		}
		avatarBusy = true;
		try {
			const data = new Uint8Array(await file.arrayBuffer());
			const resp = await rpcClient.user.setAvatar({ data, contentType: file.type });
			authStore.applyUser(resp.user);
			toast.success('Picture updated');
		} catch {
			// error interceptor
		} finally {
			avatarBusy = false;
		}
	}

	async function removeAvatar() {
		avatarBusy = true;
		try {
			const resp = await rpcClient.user.removeAvatar({});
			authStore.applyUser(resp.user);
			toast.success('Picture removed');
		} catch {
			// error interceptor
		} finally {
			avatarBusy = false;
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
		<FormSection
			title="Picture"
			description={isLocal
				? 'Shown beside your name across the instance.'
				: 'Your identity provider supplies your picture.'}
		>
			<div class="flex flex-wrap items-center gap-5 rounded-lg border bg-card p-4">
				<UserAvatar
					username={authStore.user?.username ?? ''}
					{displayName}
					{avatarUrl}
					class="size-20 ring-1 ring-border"
					fallbackClass="text-2xl"
				/>
				<div class="min-w-0 flex-1 space-y-2">
					{#if isLocal}
						<p class="text-sm text-muted-foreground">
							PNG, JPEG, GIF, or WebP, up to 1 MB. Square images look best.
						</p>
						<div class="flex flex-wrap gap-2">
							<input
								bind:this={fileInput}
								type="file"
								accept="image/png,image/jpeg,image/gif,image/webp"
								class="hidden"
								onchange={uploadAvatar}
							/>
							<Button
								variant="outline"
								size="sm"
								onclick={() => fileInput?.click()}
								disabled={avatarBusy}
							>
								{#if avatarBusy}
									<Loader class="animate-spin" />
								{:else}
									<Upload />
								{/if}
								{avatarUrl ? 'Replace' : 'Upload'}
							</Button>
							{#if avatarUrl}
								<Button
									variant="ghost-muted"
									size="sm"
									onclick={removeAvatar}
									disabled={avatarBusy}
								>
									<Trash />
									Remove
								</Button>
							{/if}
						</div>
					{:else}
						<p class="text-sm text-muted-foreground">
							Change it with your {authStore.user?.authProvider} account and sign in again to refresh
							it here.
						</p>
					{/if}
				</div>
			</div>
		</FormSection>

		<FormSection title="Account" description="Sign-in method and roles granted to you.">
			<div class="flex items-center gap-4 rounded-lg border bg-card p-4">
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
							<Loader class="animate-spin" />
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
