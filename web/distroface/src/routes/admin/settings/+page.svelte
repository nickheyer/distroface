<script lang="ts">
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';
	import { systemGroups } from '$lib/settings-specs';
	import Leaf from '$lib/bits/Leaf.svelte';
	import SettingsDesk from '$lib/bits/SettingsDesk.svelte';
</script>

<Leaf no="01" title="Settings">
	<p class="note" style="margin-bottom: 0.9rem">
		Every runtime setting of the instance, applied live. Unset fields fall back to the config file
		and the built-in defaults; each field notes the value in force and where it comes from.
		Organizations and portals may override their own subset.
	</p>
	<SettingsDesk scopeType={SettingsScopeType.SYSTEM} groups={systemGroups} />
</Leaf>
