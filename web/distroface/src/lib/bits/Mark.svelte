<script lang="ts">
	import type { MarkKind } from '$lib/fmt';

	let { kind, label, title }: { kind: MarkKind; label: string; title?: string } = $props();
</script>

<span class="mark {kind}" {title}>{label}</span>
