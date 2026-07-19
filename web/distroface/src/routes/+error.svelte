<script lang="ts">
	import { page } from '$app/state';
</script>

<hgroup class="folio">
	<p class="kicker">Error</p>
	<h1>{page.status}</h1>
	<p class="sub">
		{page.error?.message || 'This page does not exist.'} Return to the <a href="/">registry</a>.
	</p>
</hgroup>
