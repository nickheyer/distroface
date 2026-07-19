<script lang="ts">
	import type { Lister } from '$lib/list.svelte';

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let { lister, placeholder = 'name…' }: { lister: Lister<any>; placeholder?: string } = $props();

	let value = $state('');

	function submit(e: Event) {
		e.preventDefault();
		lister.text = value.trim();
		lister.first();
	}
</script>

<form class="findline" onsubmit={submit}>
	<span class="caps faint">find</span>
	<input type="search" bind:value {placeholder} aria-label="find" />
</form>
