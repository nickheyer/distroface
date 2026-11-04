<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { HardDriveDownload, AlertTriangle, RefreshCw } from '$lib/icons';

	let {
		label,
		error = '',
		title = '',
		syncing = false,
		syncingLabel = 'Syncing'
	}: {
		label: string;
		error?: string;
		title?: string;
		syncing?: boolean;
		syncingLabel?: string;
	} = $props();
</script>

{#if syncing}
	<Badge {title}>
		<RefreshCw class="animate-spin" />{syncingLabel}
	</Badge>
{:else if error}
	<Badge variant="destructive" {title}>
		<HardDriveDownload />{label}
	</Badge>
	<span title={error}>
		<AlertTriangle class="size-3.5 text-destructive" />
	</span>
{:else}
	<Badge variant="outline" {title}>
		<HardDriveDownload />{label}
	</Badge>
{/if}
