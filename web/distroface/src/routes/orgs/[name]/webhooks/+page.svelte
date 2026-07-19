<script lang="ts">
	import { getContext } from 'svelte';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import WebhookDesk from '$lib/bits/WebhookDesk.svelte';

	const ctx = getContext<OrgCtx>(ORG_CTX);
</script>

<Leaf no="01" title="Webhooks">
	<p class="note" style="margin-bottom: 0.9rem">
		Called on registry events across every repository in the organization's namespace.
	</p>
	{#if ctx.org}
		{#key ctx.org.id}
			<WebhookDesk orgId={ctx.org.id} />
		{/key}
	{/if}
</Leaf>
