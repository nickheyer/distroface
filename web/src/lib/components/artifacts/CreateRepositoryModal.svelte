<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { Lock, Globe } from "lucide-svelte";

  let { onclose } = $props<{
    onclose: () => void;
  }>();
  
  let name = $state("");
  let description = $state("");
  let isPrivate = $state(true);
  let loading = $state(false);
  let error = $state<string | null>(null);

  async function handleSubmit() {
      if (!name.trim()) {
          error = "Repository name is required";
          return;
      }

      loading = true;
      try {
          await artifacts.createRepository(name, description, isPrivate);
          onclose();
      } catch (err) {
          error = err instanceof Error ? err.message : "Failed to create repository";
      } finally {
          loading = false;
      }
  }
</script>

<div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
  <div class="fixed inset-0 z-10 overflow-y-auto">
      <div class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0">
          <div class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6">
              <div class="sm:flex sm:items-start">
                  <div class="mt-3 text-center sm:mt-0 sm:text-left w-full">
                      <h3 class="text-lg font-semibold leading-6 text-gray-900">
                          Create New Repository
                      </h3>
                      
                      <form class="mt-4 space-y-4" onsubmit={handleSubmit}>
                          {#if error}
                              <div class="rounded-md bg-red-50 p-4 text-sm text-red-700">
                                  {error}
                              </div>
                          {/if}

                          <div>
                              <label for="name" class="block text-sm font-medium text-gray-700">
                                  Repository Name
                              </label>
                              <input
                                type="text"
                                id="name"
                                bind:value={name}
                                class="block w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                                placeholder="my-artifacts"
                                required
                              />
                          </div>

                          <div>
                              <label for="description" class="block text-sm font-medium text-gray-700">
                                  Description
                              </label>
                              <textarea
                                id="description"
                                bind:value={description}
                                rows="3"
                                class="block w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                                placeholder="Repository description..."
                              ></textarea>
                          </div>

                          <div class="flex items-center space-x-3">
                              <button
                                  type="button"
                                  class={`flex items-center px-3 py-2 rounded-md ${!isPrivate ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-800'}`}
                                  onclick={() => isPrivate = false}
                              >
                                  <Globe class="h-4 w-4 mr-2" />
                                  Public
                              </button>
                              <button
                                  type="button"
                                  class={`flex items-center px-3 py-2 rounded-md ${isPrivate ? 'bg-blue-100 text-blue-800' : 'bg-gray-100 text-gray-800'}`}
                                  onclick={() => isPrivate = true}
                              >
                                  <Lock class="h-4 w-4 mr-2" />
                                  Private
                              </button>
                          </div>

                          <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
                              <button
                                  type="submit"
                                  disabled={loading}
                                  class="inline-flex w-full justify-center rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-500 sm:ml-3 sm:w-auto"
                              >
                                  {loading ? 'Creating...' : 'Create Repository'}
                              </button>
                              <button
                                  type="button"
                                  onclick={() => onclose()}
                                  class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                              >
                                  Cancel
                              </button>
                          </div>
                      </form>
                  </div>
              </div>
          </div>
      </div>
  </div>
</div>
