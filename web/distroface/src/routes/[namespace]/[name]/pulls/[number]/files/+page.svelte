<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
</script>

<p class="text-sm text-muted-foreground">
	Redirecting to <a
		href="{resolve('/[namespace]/[name]/pulls/[number]', {
			namespace: page.params.namespace ?? '',
			name: page.params.name ?? '',
			number: page.params.number ?? ''
		})}?tab=files"
		class="link">files changed</a
	>
</p>
