<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Bot } from '$lib/icons';
	import { cn } from '$lib/utils';

	// Marks accounts that act for agents
	let {
		class: className,
		title = 'Agent account'
	}: {
		class?: string;
		title?: string;
	} = $props();
</script>

<Badge variant="outline" class={cn('h-4 gap-0.5 px-1.5 text-2xs font-medium', className)} {title}>
	<Bot />bot
</Badge>
