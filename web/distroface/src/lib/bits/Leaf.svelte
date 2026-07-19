<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		no,
		title,
		aside,
		children
	}: { no: string; title: string; aside?: Snippet; children: Snippet } = $props();
</script>

<section class="leaf">
	<span class="no">{no}</span>
	<div class="body">
		<header class="leaf-head">
			<h2>{title}</h2>
			{#if aside}
				<span class="aside">{@render aside()}</span>
			{/if}
		</header>
		{@render children()}
	</div>
</section>
