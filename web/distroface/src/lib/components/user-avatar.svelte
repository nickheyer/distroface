<script lang="ts">
	import { Avatar, AvatarFallback, AvatarImage } from '$lib/components/ui/avatar';
	import { cn } from '$lib/utils';

	// Initials from a display name or username
	export function initialsOf(name: string): string {
		return (name || '?')
			.split(/[\s-_.]+/)
			.filter(Boolean)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	let {
		username,
		displayName = '',
		avatarUrl = '',
		class: className,
		fallbackClass
	}: {
		username: string;
		displayName?: string;
		avatarUrl?: string;
		class?: string;
		fallbackClass?: string;
	} = $props();

	const label = $derived(displayName || username);
</script>

<Avatar class={cn('size-8', className)}>
	{#if avatarUrl}
		<AvatarImage src={avatarUrl} alt={label} />
	{/if}
	<AvatarFallback class={fallbackClass}>{initialsOf(label)}</AvatarFallback>
</Avatar>
