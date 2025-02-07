<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { Upload, X } from "lucide-svelte";
  import type { ArtifactRepository } from "$lib/types/artifacts.svelte";

  let { repository, onclose } = $props<{
      repository: ArtifactRepository;
      onclose: () => void;
  }>();
  
  let files = $state<FileList | null>(null);
  let version = $state("");
  let path = $state("");
  let loading = $state(false);
  let error = $state<string | null>(null);

  async function handleSubmit() {
      if (!files?.length) {
          error = "Please select a file to upload";
          return;
      }

      if (!version.trim()) {
          error = "Version is required";
          return;
      }

      loading = true;
      try {
          await artifacts.uploadArtifact(
              repository.name,
              files[0],
              version,
              path || files[0].name
          );
          onclose();
      } catch (err) {
          error = err instanceof Error ? err.message : "Failed to upload artifact";
      } finally {
          loading = false;
      }
  }
</script>

<div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
  <div class="fixed inset-0 z-10 overflow-y-auto">
      <div class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0">
          <div class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6">
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
                          Upload Artifact to {repository.name}
                      </h3>

                      <form class="mt-4 space-y-4" onsubmit={handleSubmit}>
                          {#if error}
                              <div class="rounded-md bg-red-50 p-4 text-sm text-red-700">
                                  {error}
                              </div>
                          {/if}

                          <div>
                              <span class="block text-sm font-medium text-gray-700">
                                  File
                              </span>
                              <div class="mt-1 flex justify-center rounded-md border-2 border-dashed border-gray-300 px-6 pt-5 pb-6">
                                  <div class="space-y-1 text-center">
                                      <Upload class="mx-auto h-12 w-12 text-gray-400" />
                                      <div class="flex text-sm text-gray-600">
                                          <label class="relative cursor-pointer rounded-md bg-white font-medium text-blue-600 focus-within:outline-none focus-within:ring-2 focus-within:ring-blue-500 focus-within:ring-offset-2 hover:text-blue-500">
                                              <span>Upload a file</span>
                                              <input
                                                  type="file"
                                                  class="sr-only"
                                                  bind:files
                                              />
                                          </label>
                                          <p class="pl-1">or drag and drop</p>
                                      </div>
                                      {#if files?.[0]}
                                          <p class="text-sm text-gray-500">
                                              Selected: {files[0].name}
                                          </p>
                                      {:else}
                                          <p class="text-xs text-gray-500">
                                              Any file up to 10GB
                                          </p>
                                      {/if}
                                  </div>
                              </div>
                          </div>

                          <div>
                              <label for="version" class="block text-sm font-medium text-gray-700">
                                  Version
                              </label>
                              <input
                                type="text"
                                id="version"
                                bind:value={version}
                                class="block w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                                placeholder="1.0.0"
                                required
                              />
                          </div>

                          <div>
                              <label for="path" class="block text-sm font-medium text-gray-700">
                                  Path (optional)
                              </label>
                              <input
                                type="text"
                                id="path"
                                bind:value={path}
                                class="block w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                                placeholder="folder/artifact.zip"
                              />
                          </div>

                          <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
                              <button
                                  type="submit"
                                  disabled={loading}
                                  class="inline-flex w-full justify-center rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-500 sm:ml-3 sm:w-auto"
                              >
                                  {loading ? 'Uploading...' : 'Upload'}
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
