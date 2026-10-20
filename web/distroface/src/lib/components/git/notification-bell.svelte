<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Bell } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { notificationStore } from '$lib/stores/notification.svelte';

	let { class: className = '' }: { class?: string } = $props();

	const count = $derived(notificationStore.unread);

	onMount(() => {
		notificationStore.ensure();
		return () => notificationStore.stop();
	});
</script>

<Button
	href={resolve('/notifications')}
	variant="ghost-muted"
	size="icon"
	class="relative {className}"
	aria-label={count > 0 ? `Notifications, ${count} unread` : 'Notifications'}
	title="Notifications"
>
	<Bell />
	{#if count > 0}
		<span class="absolute top-1.5 right-1.5 size-2 rounded-full bg-primary" aria-hidden="true"
		></span>
	{/if}
</Button>
