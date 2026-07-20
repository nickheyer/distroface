<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { HardDriveDownload, AlertTriangle, RefreshCw } from '@lucide/svelte';

	let {
		label,
		error = '',
		title = '',
		syncing = false
	}: { label: string; error?: string; title?: string; syncing?: boolean } = $props();
</script>

{#if syncing}
	<Badge variant="outline" class="text-xs gap-1 border-amber-500/30 text-amber-600 dark:text-amber-400" {title}>
		<RefreshCw class="h-2.5 w-2.5 animate-spin" />Syncing
	</Badge>
{:else}
	<Badge variant="outline" class="text-xs gap-1 border-sky-500/30 text-sky-600 dark:text-sky-400" {title}>
		<HardDriveDownload class="h-2.5 w-2.5" />{label}
	</Badge>
{/if}
{#if error && !syncing}
	<span title={error}>
		<AlertTriangle class="h-3.5 w-3.5 text-destructive" />
	</span>
{/if}
