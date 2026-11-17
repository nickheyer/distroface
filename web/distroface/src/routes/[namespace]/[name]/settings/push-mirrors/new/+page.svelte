<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import OutboundSshKeys from '$lib/components/git/outbound-ssh-keys.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import LimitInput from '$lib/components/limit-input.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const listHref = $derived(
		resolve('/[namespace]/[name]/settings/push-mirrors', { namespace, name })
	);

	let remoteUrl = $state('');
	let username = $state('');
	let authToken = $state('');
	let intervalMinutes = $state<number | undefined>(60);
	let syncOnPush = $state(true);
	let creating = $state(false);

	const isSsh = $derived(/^(ssh:\/\/|git@|[\w.-]+@)/.test(remoteUrl.trim()));

	async function create() {
		if (!remoteUrl.trim() || creating) return;
		creating = true;
		const ok = await rpcClient.git
			.createPushMirror({
				namespace,
				name,
				remoteUrl: remoteUrl.trim(),
				username: username.trim(),
				authToken,
				useSshKey: isSsh,
				intervalMinutes: Math.max(0, Math.trunc(Number(intervalMinutes) || 0)),
				syncOnPush
			})
			.then(
				() => true,
				() => false
			);
		creating = false;
		if (!ok) return;
		toast.success('Push mirror added');
		await goto(resolve('/[namespace]/[name]/settings/push-mirrors', { namespace, name }));
	}
</script>

<svelte:head>
	<title>Add push mirror - {namespace}/{name}</title>
</svelte:head>

<FormPage
	level="section"
	title="Add push mirror"
	description="Every branch and tag is force pushed and anything only on the remote is deleted. Credentials are stored encrypted and never shown again."
	backHref={listHref}
	backLabel="Push mirrors"
>
	<FormSection title="Destination" description="Where the repository is pushed.">
		<FormField
			label="Remote URL"
			id="pm-url"
			required
			help="https or ssh clone URL of the destination"
		>
			<Input
				id="pm-url"
				bind:value={remoteUrl}
				placeholder="https://github.com/owner/repo.git"
				class="font-mono"
				autocomplete="off"
			/>
		</FormField>
		{#if isSsh}
			<p class="text-sm text-muted-foreground">
				SSH remotes authenticate with these keys. Install any one of them on the remote with write
				access.
			</p>
			<OutboundSshKeys
				instance={repo?.instanceSshPublicKey}
				organization={repo?.orgSshPublicKey}
				repository={repo?.mirrorSshPublicKey}
				{namespace}
				{name}
				idPrefix="pm-key"
			/>
		{:else}
			<FormField
				label="Username"
				id="pm-user"
				help="Optional, some hosts accept the token alone"
				span="half"
			>
				<Input id="pm-user" bind:value={username} autocomplete="off" data-1p-ignore />
			</FormField>
			<FormField label="Token or password" id="pm-token" span="half">
				<PasswordInput
					id="pm-token"
					bind:value={authToken}
					placeholder="Token"
					autocomplete="new-password"
					data-1p-ignore
				/>
			</FormField>
		{/if}
	</FormSection>

	<FormSection title="Schedule" description="How often changes are pushed.">
		<FormField label="Interval" id="pm-interval" layout="inline">
			<LimitInput
				id="pm-interval"
				label="interval"
				unit="minutes"
				interval
				bind:value={intervalMinutes}
				seed={60}
			/>
		</FormField>
		<FormField
			label="Push after every push"
			layout="toggle"
			help="Mirror right after refs change here"
		>
			<Switch bind:checked={syncOnPush} />
		</FormField>
	</FormSection>

	{#snippet footer()}
		<Button onclick={create} disabled={creating || !remoteUrl.trim()}>
			{#if creating}<Loader class="animate-spin" />{/if}
			{creating ? 'Adding' : 'Add mirror'}
		</Button>
		<Button variant="ghost-muted" href={listHref}>Cancel</Button>
	{/snippet}
</FormPage>
