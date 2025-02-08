<script lang="ts">
  import { onMount } from 'svelte';
  import { Save, Trash2, Plus } from 'lucide-svelte';
  import { api } from '$lib/stores/auth.svelte';

  // DEFAULTS BUT WE DONT REALLY NEED THIS
  let settings = {
    retention: {
      enabled: false,
      maxVersions: 5,
      maxAge: 30, // DAYS
      excludeLatest: true
    },
    storage: {
      maxFileSize: 1024, // MB
      allowedTypes: ['*/*'],
      compressionEnabled: true
    },
    properties: {
      required: ['version', 'build', 'branch'],
      indexed: ['version', 'build', 'branch', 'commit']
    },
    search: {
      maxResults: 100,
      defaultSort: 'created',
      defaultOrder: 'desc'
    }
  };

  let isEditing = false;
  let error: string | null = null;

  // LOAD IN SETTINGS
  onMount(async () => {
    try {
      const response = await api.get('/api/v1/settings/artifacts');
      if (response.ok) {
        settings = await response.json();
      }
    } catch (err) {
      console.error('Failed to load settings:', err);
    }
  });

  async function handleSave() {
    try {
      const response = await api.put('/api/v1/settings/artifacts', settings);
      if (!response.ok) {
        throw new Error('Failed to save settings');
      }
      // If successful:
      isEditing = false;
      error = null;
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to save settings';
    }
  }
</script>

<div class="space-y-6">
  <div class="flex justify-between items-center">
    {#if isEditing}
      <div class="flex space-x-2">
        <button
          on:click={() => (isEditing = false)}
          class="px-3 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
        >
          Cancel
        </button>
        <button
          on:click={handleSave}
          class="flex items-center px-3 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
        >
          <Save class="w-4 h-4 mr-2" />
          Save Changes
        </button>
      </div>
    {:else}
      <button
        on:click={() => (isEditing = true)}
        class="px-3 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
      >
        Edit Settings
      </button>
    {/if}
  </div>

  {#if error}
    <div class="rounded-md bg-red-50 p-4">
      <div class="text-sm text-red-700">{error}</div>
    </div>
  {/if}

  <div class="bg-white shadow-sm rounded-lg divide-y divide-gray-200">
    <!-- RETENTION POLICY -->
    <div class="p-6">
      <h3 class="text-base font-medium text-gray-900">Retention Policy</h3>
      <div class="mt-4 space-y-4">
        <div class="flex items-center">
          <input
            type="checkbox"
            id="retention-enabled"
            bind:checked={settings.retention.enabled}
            disabled={!isEditing}
            class="h-4 w-4 text-blue-600 rounded border-gray-300"
          />
          <label for="retention-enabled" class="ml-2 text-sm text-gray-700">
            Enable Automatic Cleanup
          </label>
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label for="max-versions" class="block text-sm font-medium text-gray-700"
              >Maximum Versions</label
            >
            <input
              id="max-versions"
              type="number"
              bind:value={settings.retention.maxVersions}
              disabled={!isEditing}
              class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
            />
          </div>
          <div>
            <label for="max-age-days" class="block text-sm font-medium text-gray-700"
              >Maximum Age (Days)</label
            >
            <input
              id="max-age-days"
              type="number"
              bind:value={settings.retention.maxAge}
              disabled={!isEditing}
              class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- PROPERTY CONFIGURATION -->
    <div class="p-6">
      <h3 class="text-base font-medium text-gray-900">Property Configuration</h3>
      <div class="mt-4 space-y-4">
        <div>
          <h5 class="block text-sm font-medium text-gray-700">Required Properties</h5>
          <div class="mt-2 space-y-2">
            {#each settings.properties.required as prop, index}
              <div class="flex items-center">
                <input
                  type="text"
                  bind:value={settings.properties.required[index]}
                  disabled={!isEditing}
                  class="block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
                />
                {#if isEditing}
                  <button
                    on:click={() => {
                      settings.properties.required = settings.properties.required.filter((_, i) => i !== index);
                    }}
                    class="ml-2 text-gray-400 hover:text-red-500"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                {/if}
              </div>
            {/each}
            {#if isEditing}
              <button
                on:click={() => {
                  settings.properties.required = [...settings.properties.required, ''];
                }}
                class="flex items-center text-sm text-blue-600 hover:text-blue-500"
              >
                <Plus class="h-4 w-4 mr-1" />
                Add Property
              </button>
            {/if}
          </div>
        </div>
      </div>
    </div>

    <!-- SEARCH CONFIG -->
    <div class="p-6">
      <h3 class="text-base font-medium text-gray-900">Search Configuration</h3>
      <div class="mt-4 grid grid-cols-2 gap-4">
        <div>
          <label for="default-sort-field" class="block text-sm font-medium text-gray-700"
            >Default Sort Field</label
          >
          <select
            id="default-sort-field"
            bind:value={settings.search.defaultSort}
            disabled={!isEditing}
            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
          >
            <option value="created">Created Date</option>
            <option value="updated">Updated Date</option>
            <option value="name">Name</option>
            <option value="size">Size</option>
          </select>
        </div>
        <div>
          <label for="default-sort-order" class="block text-sm font-medium text-gray-700"
            >Default Sort Order</label
          >
          <select
            id="default-sort-order"
            bind:value={settings.search.defaultOrder}
            disabled={!isEditing}
            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
          >
            <option value="desc">Descending</option>
            <option value="asc">Ascending</option>
          </select>
        </div>
      </div>
    </div>
  </div>
</div>
