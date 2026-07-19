<script lang="ts">
	import type { Lister } from '$lib/list.svelte';

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let { lister, unit = 'entries' }: { lister: Lister<any>; unit?: string } = $props();
</script>

{#if lister.total > lister.rows.length || lister.hasBack || lister.hasNext}
	<div class="tally">
		<span class="count">{lister.rows.length} of {lister.total} {unit}</span>
		<span class="turn">
			<button class="rowact plain" disabled={!lister.hasBack || lister.busy} onclick={() => lister.back()}
				>← previous</button>
			<button class="rowact plain" disabled={!lister.hasNext || lister.busy} onclick={() => lister.forward()}
				>next →</button>
		</span>
	</div>
{/if}
