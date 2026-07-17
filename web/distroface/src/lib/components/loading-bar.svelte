<script lang="ts">
	import { fade } from 'svelte/transition';
	import { networkStore } from '$lib/stores/network.svelte';
</script>

{#if networkStore.visible}
	<div
		class="pointer-events-none fixed inset-x-0 top-0 z-60 h-0.5 overflow-hidden"
		out:fade={{ duration: 200 }}
	>
		<div class="loading-slide h-full w-1/3 rounded-full bg-primary"></div>
	</div>
{/if}

<style>
	.loading-slide {
		animation: slide 1.1s ease-in-out infinite;
	}
	@keyframes slide {
		0% {
			transform: translateX(-100%);
		}
		100% {
			transform: translateX(400%);
		}
	}
</style>
