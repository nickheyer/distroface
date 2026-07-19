<script lang="ts">
	import { getContext } from 'svelte';
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';
	import { orgGroups } from '$lib/settings-specs';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import SettingsDesk from '$lib/bits/SettingsDesk.svelte';

	const ctx = getContext<OrgCtx>(ORG_CTX);
</script>

<Leaf no="01" title="Settings">
	<p class="note" style="margin-bottom: 0.9rem">
		Organization-level overrides of the instance settings. Unset fields inherit from the system,
		the config file, or the built-in defaults, in that order.
	</p>
	{#if ctx.org}
		{#key ctx.org.id}
			<SettingsDesk scopeType={SettingsScopeType.ORG} scopeId={ctx.org.id} groups={orgGroups} />
		{/key}
	{/if}
</Leaf>
