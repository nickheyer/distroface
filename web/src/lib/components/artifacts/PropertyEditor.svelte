<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { X, Plus, Minus, Save } from "lucide-svelte";
  import type { ArtifactRepository, Artifact } from "$lib/types/artifacts.svelte";

  let { artifact, repository, onclose } = $props<{
      artifact: Artifact;
      repository: ArtifactRepository;
      onclose: () => void;
  }>();

  interface Property {
    key: string;
    value: string;
  }

  let properties = $state<Property[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);

  // INIT PROPERTIES FROM EXISTING
  $effect(() => {
    try {
      const artifactProps = artifact.properties || {};
      console.log(artifactProps)
      properties = Object.entries(artifactProps).map(([key, value]) => ({
        key,
        value: typeof value === 'string' ? value : JSON.stringify(value),
      }));
    } catch (err) {
      properties = [];
    }
  });

  function addProperty() {
    properties = [...properties, { key: "", value: "" }];
  }

  function removeProperty(index: number) {
    properties = properties.filter((_, i) => i !== index);
  }

  async function handleSubmit() {
    loading = true;
    error = null;
    try {
      const propObj = properties.reduce((acc, { key, value }) => {
              if (key.trim()) {
                  acc[key.trim()] = value.trim();
              }
              return acc;
          }, {} as Record<string, string>);

      // UPDATE PROPERTIES
      await artifacts.updateProperties(repository.name, artifact.id, propObj);
      onclose();
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to update properties";
    } finally {
      loading = false;
    }
  }
</script>

<div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
  <div class="fixed inset-0 z-10 overflow-y-auto">
    <div
      class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0"
    >
      <div
        class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6"
      >
        <div class="absolute right-0 top-0 pr-4 pt-4">
          <button
            type="button"
            class="rounded-md bg-white text-gray-400 hover:text-gray-500"
            onclick={() => onclose()}
          >
            <X class="h-6 w-6" />
          </button>
        </div>

        <div class="sm:flex sm:items-start">
          <div class="mt-3 text-center sm:mt-0 sm:text-left w-full">
            <h3 class="text-lg font-semibold leading-6 text-gray-900">
              Edit Properties
            </h3>
            <p class="mt-1 text-sm text-gray-500">
              {artifact.name} (version {artifact.version})
            </p>

            {#if error}
              <div class="mt-4 rounded-md bg-red-50 p-4 text-sm text-red-700">
                {error}
              </div>
            {/if}

            <div class="mt-4 space-y-4">
              {#each properties as property, i}
                <div class="flex items-center space-x-2">
                  <input
                    type="text"
                    placeholder="Property name"
                    bind:value={property.key}
                    class="block w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                  />
                  <input
                    type="text"
                    placeholder="Value"
                    bind:value={property.value}
                    class="block w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                  />
                  <button
                    type="button"
                    onclick={() => removeProperty(i)}
                    class="p-2 text-gray-400 hover:text-red-500"
                  >
                    <Minus class="h-5 w-5" />
                  </button>
                </div>
              {/each}

              <button
                type="button"
                onclick={addProperty}
                class="inline-flex items-center px-3 py-2 border border-gray-300 shadow-sm text-sm leading-4 font-medium rounded-md text-gray-700 bg-white hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500"
              >
                <Plus class="h-4 w-4 mr-2" />
                Add Property
              </button>
            </div>

            <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
              <button
                type="button"
                onclick={handleSubmit}
                disabled={loading}
                class="inline-flex w-full justify-center rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-500 sm:ml-3 sm:w-auto"
              >
                {#if loading}
                  <Save class="h-4 w-4 mr-2 animate-spin" />
                  Saving...
                {:else}
                  <Save class="h-4 w-4 mr-2" />
                  Save Changes
                {/if}
              </button>
              <button
                type="button"
                onclick={() => onclose()}
                class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
