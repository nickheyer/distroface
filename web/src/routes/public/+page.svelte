<script lang="ts">
  import { auth, api } from "$lib/stores/auth.svelte";
  import {
    Search,
    Package,
    Lock,
    Globe,
    AlertCircle,
    Loader2,
  } from "lucide-svelte";
  import type { ImageRepository, VisibilityUpdateRequest } from '$lib/types/registry.svelte';
  import { formatDistance } from "date-fns";
  import Tag from "$lib/components/Tag.svelte";

  // STATE
  let repositories = $state<ImageRepository[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let searchTerm = $state("");
  let metrics = $state({ totalSize: 0, totalImages: 0 });
  let visibilityModal = $state<{
    repository: ImageRepository | null;
    action: "public" | "private";
  } | null>(null);
  let request = $state<VisibilityUpdateRequest| null>(null);

  // MODAL
  async function toggleVisibility(repository: ImageRepository) {
    request = {
      id: repository.id,
      private: !repository.private
    };


    visibilityModal = {
            repository,
            action: request.private ? 'private' : 'public'
        };
  }

  async function confirmVisibilityChange() {
    if (!visibilityModal?.repository) return;
    
    try {
        await api.post('/api/v1/repositories/visibility', request);
        await fetchRepositories();
        visibilityModal = null;
    } catch (err) {
        error = err instanceof Error ? err.message : 'Failed to update visibility';
    }
  }

  // FETCH DATA
  async function fetchRepositories() {
    try {
      const response = await api.get("/api/v1/repositories/public");
      const data = await response.json();
      repositories = data.images || [];
      metrics = {
        totalSize: data.total_size || 0,
        totalImages: data.total_images || 0,
      };
    } catch (err) {
      error =
        err instanceof Error ? err.message : "Failed to fetch repositories";
    } finally {
      loading = false;
    }
  }

  function formatSize(bytes: number): string {
    const units = ["B", "KB", "MB", "GB", "TB"];
    let size = bytes;
    let unitIndex = 0;
    while (size >= 1024 && unitIndex < units.length - 1) {
      size /= 1024;
      unitIndex++;
    }
    return `${size.toFixed(1)} ${units[unitIndex]}`;
  }

  // COMPUTED
  const filteredRepositories = $derived(
    repositories.filter((repo) =>
      repo.name.toLowerCase().includes(searchTerm.toLowerCase())
    )
  );

  // INITIAL FETCH
  $effect(() => {
    if (auth.isAuthenticated) {
      fetchRepositories();
    }
  });
</script>

<div class="space-y-6">
  <!-- HEADER AND SEARCH -->
  <div class="sm:flex sm:items-center sm:justify-between">
    <div>
      <h1 class="text-2xl font-semibold text-gray-900">Public Registry</h1>
      <p class="mt-2 text-sm text-gray-700">
        Browse and manage public container images
      </p>
    </div>
    <div class="mt-4 sm:mt-0 sm:ml-16 sm:flex-none">
      <div class="relative">
        <input
          type="text"
          placeholder="Search repositories..."
          bind:value={searchTerm}
          class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-md leading-5
                         bg-white placeholder-gray-500 focus:outline-none focus:ring-1
                         focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
        />
        <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
      </div>
    </div>
  </div>

  <!-- METRICS CARDS -->
  <div class="grid grid-cols-1 gap-5 sm:grid-cols-2">
    <div class="bg-white overflow-hidden shadow rounded-lg">
      <div class="px-4 py-5 sm:p-6">
        <div class="flex items-center">
          <div class="flex-shrink-0 bg-blue-500 rounded-md p-3">
            <Package class="h-6 w-6 text-white" />
          </div>
          <div class="ml-5">
            <dl>
              <dt class="text-sm font-medium text-gray-500 truncate">
                Total Images
              </dt>
              <dd class="mt-1 text-3xl font-semibold text-gray-900">
                {metrics.totalImages}
              </dd>
            </dl>
          </div>
        </div>
      </div>
    </div>

    <div class="bg-white overflow-hidden shadow rounded-lg">
      <div class="px-4 py-5 sm:p-6">
        <div class="flex items-center">
          <div class="flex-shrink-0 bg-green-500 rounded-md p-3">
            <Globe class="h-6 w-6 text-white" />
          </div>
          <div class="ml-5">
            <dl>
              <dt class="text-sm font-medium text-gray-500 truncate">
                Total Size
              </dt>
              <dd class="mt-1 text-3xl font-semibold text-gray-900">
                {formatSize(metrics.totalSize)}
              </dd>
            </dl>
          </div>
        </div>
      </div>
    </div>
  </div>

  {#if loading}
    <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
  {:else if error}
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
  {:else if filteredRepositories.length === 0}
    <div class="text-center py-12">
      <Package class="mx-auto h-12 w-12 text-gray-400" />
      <h3 class="mt-2 text-sm font-medium text-gray-900">
        No repositories found
      </h3>
      <p class="mt-1 text-sm text-gray-500">
        {searchTerm
          ? "Try adjusting your search terms"
          : "Start by making some of your repositories public"}
      </p>
    </div>
  {:else}
    <!-- REPOSITORY LIST -->
    <div class="bg-white shadow overflow-hidden sm:rounded-md">
      <ul class="divide-y divide-gray-200">
        {#each filteredRepositories as repo}
          <li>
            <div class="px-4 py-4 sm:px-6">
              <div class="flex items-center justify-between">
                <div class="flex items-center">
                  <div class="flex-shrink-0">
                    <Package class="h-6 w-6 text-gray-400" />
                  </div>
                  <div class="ml-4">
                    <div class="flex items-center">
                      <h3 class="text-lg font-medium text-gray-900">
                        {repo.name}
                      </h3>
                      {#if repo.owner === auth.user?.username}
                        <button
                          onclick={() => toggleVisibility(repo)}
                          class="ml-2 inline-flex items-center px-2 py-1 rounded-md text-sm
                             {repo.private
                            ? 'text-gray-600 bg-gray-100 hover:bg-gray-200'
                            : 'text-green-600 bg-green-100 hover:bg-green-200'}"
                        >
                          {#if repo.private}
                            <Lock class="h-4 w-4 mr-1" />
                            Private
                          {:else}
                            <Globe class="h-4 w-4 mr-1" />
                            Public
                          {/if}
                        </button>
                      {/if}
                    </div>
                    <div class="mt-2 flex items-center text-sm text-gray-500">
                      <span class="mr-2">Owner: {repo.owner}</span>
                      <span class="mr-2">•</span>
                      <span>Size: {formatSize(repo.size)}</span>
                    </div>
                  </div>
                </div>
                <div class="flex flex-col items-end">
                  <span class="text-sm text-gray-500">
                    Updated {formatDistance(
                      new Date(repo.updated_at),
                      new Date(),
                      { addSuffix: true }
                    )}
                  </span>
                  <div class="mt-2 flex flex-wrap gap-2 justify-end">
                    {#each repo.tags as tag}
                      <Tag {tag} name={repo.name} />
                    {/each}
                  </div>
                </div>
              </div>
            </div>
          </li>
        {/each}
      </ul>
    </div>
  {/if}
</div>

{#if visibilityModal}
    <div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
        <div class="fixed inset-0 z-10 overflow-y-auto">
            <div class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0">
                <div class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6">
                    <div class="sm:flex sm:items-start">
                        <div class="mx-auto flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full {visibilityModal.action === 'private' ? 'bg-yellow-100' : 'bg-blue-100'} sm:mx-0 sm:h-10 sm:w-10">
                            {#if visibilityModal.action === 'private'}
                                <Lock class="h-6 w-6 text-yellow-600" />
                            {:else}
                                <Globe class="h-6 w-6 text-blue-600" />
                            {/if}
                        </div>
                        <div class="mt-3 text-center sm:ml-4 sm:mt-0 sm:text-left">
                            <h3 class="text-base font-semibold leading-6 text-gray-900">
                                Make Repository {visibilityModal.action === 'private' ? 'Private' : 'Public'}
                            </h3>
                            <div class="mt-2">
                                <p class="text-sm text-gray-500">
                                    {#if visibilityModal && visibilityModal.repository && visibilityModal.action === 'private'}
                                        Are you sure you want to make <span class="font-semibold">{visibilityModal.repository.name}</span> private? 
                                        This will hide it from the public registry.
                                    {:else}
                                        Are you sure you want to make <span class="font-semibold">{visibilityModal ? visibilityModal.repository?.name : "(Unknown Repository)"}</span> public? 
                                        This will make it visible to all users.
                                    {/if}
                                </p>
                            </div>
                        </div>
                    </div>
                    <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
                        <button
                            type="button"
                            class="inline-flex w-full justify-center rounded-md px-3 py-2 text-sm font-semibold text-white shadow-sm sm:ml-3 sm:w-auto
                                   {visibilityModal.action === 'private' ? 
                                       'bg-yellow-600 hover:bg-yellow-500' : 
                                       'bg-blue-600 hover:bg-blue-500'}"
                            onclick={confirmVisibilityChange}
                        >
                            {visibilityModal.action === 'private' ? 'Make Private' : 'Make Public'}
                        </button>
                        <button
                            type="button"
                            class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                            onclick={() => visibilityModal = null}
                        >
                            Cancel
                        </button>
                    </div>
                </div>
            </div>
        </div>
    </div>
{/if}
