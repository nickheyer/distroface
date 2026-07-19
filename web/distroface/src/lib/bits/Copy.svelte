<script lang="ts">
	let { text, label = 'copy' }: { text: string; label?: string } = $props();

	let done = $state(false);

	async function copy() {
		await navigator.clipboard.writeText(text);
		done = true;
		setTimeout(() => (done = false), 1400);
	}
</script>

<button class="rowact plain" onclick={copy}>{done ? 'copied' : label}</button>
