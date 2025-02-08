<script lang="ts">
  import { auth } from "$lib/stores/auth.svelte";
  import SettingsPanel from "$lib/components/settings/SettingsPanel.svelte";
</script>

{#if auth.hasRole('admins')}
  <SettingsPanel />
{:else}
  <div class="text-center py-12">
    <p class="text-gray-500">You don't have permission to view settings.</p>
  </div>
{/if}
