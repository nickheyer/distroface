<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { AlertCircle, Package, Search, Upload, Loader2 } from "lucide-svelte";
  import { formatDistance } from "date-fns";

  let { initialRepo = null } = $props();

  let uploadModalOpen = $state(false);
  let currentFile = $state<File | null>(null);
  let uploadVersion = $state("");
  let uploadPath = $state("");
  let error = $state<string | null>(null);

  const filteredRepos = $derived(artifacts.filtered);
  const isLoading = $derived(artifacts.loading);
  const hasUploadInProgress = $derived(Object.keys(artifacts.uploadProgress).length > 0);

  // FILE SELECTION
  function handleFileChange(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      currentFile = input.files[0];
      // DEFAULT VERSION TO FILE NAME WITHOUT EXTENSION
      uploadVersion = currentFile.name.replace(/\.[^/.]+$/, "");
      uploadPath = currentFile.name;
    }
  }

  // HANDLE UPLOAD
  async function handleUpload() {
    if (!currentFile || !uploadVersion || !uploadPath || !artifacts.currentRepo) {
      error = "Please fill in all required fields";
      return;
    }

    try {
      await artifacts.uploadArtifact(
        artifacts.currentRepo.name,
        currentFile,
        uploadVersion,
        uploadPath
      );
      uploadModalOpen = false;
      currentFile = null;
      uploadVersion = "";
      uploadPath = "";
      error = null;
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to upload artifact";
    }
  }

  // INITIAL LOAD
  $effect(() => {
    artifacts.fetchRepositories().catch(console.error);
    if (initialRepo) {
      artifacts.currentRepo = initialRepo;
    }
  });

  // LOAD ARTIFACTS WHEN REPO CHANGES
  $effect(() => {
    if (artifacts.currentRepo) {
      artifacts.fetchArtifacts(artifacts.currentRepo.name).catch(console.error);
    }
  });
</script>

<div class="space-y-6">
  <!-- HEADER -->
  <div class="sm:flex sm:items-center sm:justify-between">
    <div>
      <h1 class="text-2xl font-semibold text-gray-900">Artifact Repository</h1>
      <p class="mt-2 text-sm text-gray-700">
        Store and manage your build artifacts
      </p>
    </div>

    <!-- SEARCH AND UPLOAD -->
    <div class="mt-4 sm:mt-0 sm:flex sm:space-x-4">
      <div class="relative">
        <input
          type="text"
          placeholder="Search repositories..."
          bind:value={artifacts.searchTerm}
          class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-md focus:ring-blue-500 focus:border-blue-500 text-sm"
        />
        <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
      </div>

      <button
        onclick={() => uploadModalOpen = true}
        disabled={!artifacts.currentRepo || hasUploadInProgress}
        class="inline-flex items-center px-4 py-2 border border-transparent rounded-md shadow-sm text-sm font-medium text-white bg-blue-600 hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500 disabled:bg-gray-400"
      >
        <Upload class="h-4 w-4 mr-2" />
        Upload Artifact
      </button>
    </div>
  </div>

  <!-- ERROR MESSAGE -->
  {#if error}
    <div class="rounded-md bg-red-50 p-4">
      <div class="flex">
        <AlertCircle class="h-5 w-5 text-red-400" />
        <div class="ml-3">
          <h3 class="text-sm font-medium text-red-800">Error</h3>
          <div class="mt-2 text-sm text-red-700">
            <p>{error}</p>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <!-- LOADING STATE -->
  {#if isLoading}
    <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
  <!-- EMPTY STATE -->
  {:else if filteredRepos.length === 0}
    <div class="text-center py-12">
      <Package class="mx-auto h-12 w-12 text-gray-400" />
      <h3 class="mt-2 text-sm font-medium text-gray-900">No repositories found</h3>
      <p class="mt-1 text-sm text-gray-500">
        {artifacts.searchTerm ? "Try adjusting your search terms" : "Create a repository to get started"}
      </p>
    </div>
  <!-- REPOSITORY LIST -->
  {:else}
    <div class="bg-white shadow overflow-hidden sm:rounded-md">
      <ul class="divide-y divide-gray-200">
        {#each filteredRepos as repo}
          <li>
            <div class="px-4 py-4 flex items-center sm:px-6">
              <div class="min-w-0 flex-1 sm:flex sm:items-center sm:justify-between">
                <div>
                  <div class="flex text-sm">
                    <p class="font-medium text-blue-600 truncate">{repo.name}</p>
                    <p class="ml-1 flex-shrink-0 font-normal text-gray-500">
                      {repo.description}
                    </p>
                  </div>
                  <div class="mt-2 flex">
                    <div class="flex items-center text-sm text-gray-500">
                      <p>Created {formatDistance(new Date(repo.created_at), new Date(), { addSuffix: true })}</p>
                    </div>
                  </div>
                </div>
              </div>
              <div class="ml-5 flex-shrink-0">
                <button
                  onclick={() => artifacts.currentRepo = repo}
                  class={`px-3 py-2 text-sm font-medium rounded-md ${
                    artifacts.currentRepo?.id === repo.id
                      ? 'bg-blue-100 text-blue-700'
                      : 'text-gray-700 hover:bg-gray-50'
                  }`}
                >
                  {artifacts.currentRepo?.id === repo.id ? 'Selected' : 'Select'}
                </button>
              </div>
            </div>
          </li>
        {/each}
      </ul>
    </div>
  {/if}
</div>

<!-- UPLOAD MODAL -->
{#if uploadModalOpen}
  <div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
    <div class="fixed inset-0 z-10 overflow-y-auto">
      <div class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0">
        <div class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6">
          <div class="sm:flex sm:items-start">
            <div class="mt-3 text-center sm:mt-0 sm:text-left w-full">
              <h3 class="text-base font-semibold leading-6 text-gray-900">
                Upload Artifact
              </h3>
              <div class="mt-2">
                <div class="space-y-4">
                  <div>
                    <label for="artifact-file-input" class="block text-sm font-medium text-gray-700">
                      File
                    </label>
                    <input
                      id="artifact-file-input"
                      type="file"
                      onchange={handleFileChange}
                      class="mt-1 block w-full text-sm text-gray-500
                        file:mr-4 file:py-2 file:px-4
                        file:rounded-md file:border-0
                        file:text-sm file:font-semibold
                        file:bg-blue-50 file:text-blue-700
                        hover:file:bg-blue-100"
                    />
                  </div>
                  <div>
                    <label for="artifact-version-input" class="block text-sm font-medium text-gray-700">
                      Version
                    </label>
                    <input
                      type="text"
                      id="artifact-version-input"
                      bind:value={uploadVersion}
                      class="mt-1 block w-full rounded-md border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm"
                      placeholder="e.g., 1.0.0"
                    />
                  </div>
                  <div>
                    <label for="artifact-path" class="block text-sm font-medium text-gray-700">
                      Path
                    </label>
                    <input
                      type="text"
                      id="artifact-path"
                      bind:value={uploadPath}
                      class="mt-1 block w-full rounded-md border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm"
                      placeholder="path/to/artifact"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
          {#if hasUploadInProgress}
            <div class="mt-4">
              <div class="w-full bg-gray-200 rounded-full h-2.5">
                {#each Object.entries(artifacts.uploadProgress) as [id, progress]}
                  <div
                    class="bg-blue-600 h-2.5 rounded-full transition-all duration-300"
                    style="width: {progress}%"
                  ></div>
                {/each}
              </div>
            </div>
          {/if}
          <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
            <button
              type="button"
              onclick={handleUpload}
              disabled={hasUploadInProgress}
              class="inline-flex w-full justify-center rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-500 sm:ml-3 sm:w-auto disabled:bg-gray-400"
            >
              Upload
            </button>
            <button
              type="button"
              onclick={() => uploadModalOpen = false}
              class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
            >
              Cancel
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
{/if}
