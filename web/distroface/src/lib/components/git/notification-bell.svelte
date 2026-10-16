<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Bell } from '@lucide/svelte';
	import { notificationStore } from '$lib/stores/notification.svelte';

	let { class: className = '' }: { class?: string } = $props();

	const count = $derived(notificationStore.unread);
	const label = $derived(count > 99 ? '99+' : String(count));

	onMount(() => {
		notificationStore.ensure();
		return () => notificationStore.stop();
	});
</script>

<a
	href={resolve('/notifications')}
	class="relative inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors {className}"
	aria-label={count > 0 ? `Notifications, ${count} unread` : 'Notifications'}
	title="Notifications"
>
	<Bell class="h-4 w-4" />
	{#if count > 0}
		<span class="absolute -top-0.5 -right-0.5 min-w-4 h-4 px-1 rounded-full bg-primary text-primary-foreground text-[10px] font-semibold leading-4 text-center tabular-nums">
			{label}
		</span>
	{/if}
</a>
