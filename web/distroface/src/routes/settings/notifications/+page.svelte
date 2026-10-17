<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { Button } from '$lib/components/ui/button';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Bell, Mail, Save } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { errText } from '$lib/act.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { NotificationMode } from '$lib/proto/distroface/v1/notification_pb';

	const modeOptions = [
		{ value: NotificationMode.PARTICIPATING, label: 'Participating', help: 'Threads you author, are assigned, mentioned, or review' },
		{ value: NotificationMode.ALL, label: 'All activity', help: 'Every issue, pull request, release, and push on watched repositories' },
		{ value: NotificationMode.NONE, label: 'Nothing', help: 'No notifications' }
	];

	let loading = $state(true);
	let loadError = $state('');
	let saving = $state(false);
	// Mailed links carry a signed token that stands in for a session
	const unsubscribeToken = $derived(page.url.searchParams.get('token') ?? '');

	let webMode = $state<NotificationMode>(NotificationMode.PARTICIPATING);
	let emailMode = $state<NotificationMode>(NotificationMode.PARTICIPATING);
	let emailMentions = $state(true);
	let emailReviewRequests = $state(true);
	let emailAssignments = $state(true);
	let emailOwnActivity = $state(false);

	const mailEnabled = $derived(configStore.settings?.mail?.enabled === true);
	const modeLabel = (m: NotificationMode) => modeOptions.find((o) => o.value === m)?.label ?? 'Participating';
	const modeHelp = (m: NotificationMode) => modeOptions.find((o) => o.value === m)?.help;

	function normalize(m: NotificationMode | undefined): NotificationMode {
		return m || NotificationMode.PARTICIPATING;
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.notification.getNotificationPrefs({ unsubscribeToken }, silentCallOptions);
			const p = resp.prefs;
			webMode = normalize(p?.webMode);
			emailMode = normalize(p?.emailMode);
			emailMentions = p?.emailMentions ?? true;
			emailReviewRequests = p?.emailReviewRequests ?? true;
			emailAssignments = p?.emailAssignments ?? true;
			emailOwnActivity = p?.emailOwnActivity ?? false;
		} catch (err) {
			loadError = errText(err);
		} finally {
			loading = false;
		}
	}

	async function save() {
		saving = true;
		try {
			await rpcClient.notification.updateNotificationPrefs({
				unsubscribeToken,
				prefs: { webMode, emailMode, emailMentions, emailReviewRequests, emailAssignments, emailOwnActivity }
			});
			toast.success('Preferences saved');
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
		<h2 class="section-title">Notifications</h2>
		<p class="section-subtitle">What reaches your inbox and your email.</p>
		{#if unsubscribeToken}
			<p class="mt-2 text-xs text-muted-foreground">You arrived from an email link, changes here apply to the account that received it.</p>
		{/if}
	</div>

	{#if loading}
		<Skeleton class="h-40 w-full rounded-xl" />
		<Skeleton class="h-64 w-full rounded-xl" />
	{:else if loadError}
		<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
			<p class="text-sm text-destructive">{loadError}</p>
			<Button variant="outline" size="sm" onclick={load}>Retry</Button>
		</div>
	{:else}
		<FormCard title="Web" description="Entries in the notification inbox and the bell count." icon={Bell}>
			<FormField label="Deliver" id="web-mode" help={modeHelp(webMode)}>
				<Select type="single" value={String(webMode)} onValueChange={(v) => { if (v) webMode = Number(v) as NotificationMode; }}>
					<SelectTrigger id="web-mode" class="w-full sm:w-64">{modeLabel(webMode)}</SelectTrigger>
					<SelectContent>
						{#each modeOptions as o (o.value)}
							<SelectItem value={String(o.value)}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
		</FormCard>

		<FormCard title="Email" description={mailEnabled ? 'Messages sent to your account address.' : 'The instance mailer is off, these apply once an administrator enables it.'} icon={Mail}>
			<div class="space-y-3">
				<FormField label="Deliver" id="email-mode" help={modeHelp(emailMode)}>
					<Select type="single" value={String(emailMode)} onValueChange={(v) => { if (v) emailMode = Number(v) as NotificationMode; }}>
						<SelectTrigger id="email-mode" class="w-full sm:w-64">{modeLabel(emailMode)}</SelectTrigger>
						<SelectContent>
							{#each modeOptions as o (o.value)}
								<SelectItem value={String(o.value)}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				{#if emailMode !== NotificationMode.NONE}
					<FormField label="Mentions" horizontal help="Someone mentions you in a comment">
						<Switch bind:checked={emailMentions} />
					</FormField>
					<FormField label="Review requests" horizontal help="You or a team you belong to is asked to review">
						<Switch bind:checked={emailReviewRequests} />
					</FormField>
					<FormField label="Assignments" horizontal help="An issue or pull request is assigned to you">
						<Switch bind:checked={emailAssignments} />
					</FormField>
					<FormField label="Your own activity" horizontal help="Copies of actions you took yourself">
						<Switch bind:checked={emailOwnActivity} />
					</FormField>
				{/if}
			</div>
			{#snippet footer()}
				<Button onclick={save} disabled={saving} class="gap-2">
					<Save class="h-4 w-4" />
					{saving ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>
	{/if}
</div>
