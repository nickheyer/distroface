<script lang="ts">
	let {
		label = 'delete',
		disabled = false,
		onconfirm
	}: { label?: string; disabled?: boolean; onconfirm: () => void | Promise<void> } = $props();

	let armed = $state(false);
	let busy = $state(false);

	async function go() {
		busy = true;
		try {
			await onconfirm();
		} finally {
			busy = false;
			armed = false;
		}
	}
</script>

{#if armed}
	<span class="caps wax-ink">confirm?</span>
	<button class="rowact" disabled={busy} onclick={go}>yes</button>
	<button class="rowact plain" disabled={busy} onclick={() => (armed = false)}>no</button>
{:else}
	<button class="rowact" {disabled} onclick={() => (armed = true)}>{label}</button>
{/if}
