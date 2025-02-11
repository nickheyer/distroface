<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { clickOutside } from "$lib/actions/clickOutside";
  import { auth } from "$lib/stores/auth.svelte";
  import { formatDistance } from "date-fns";
  import {
    Package,
    Search,
    Upload,
    Plus,
    Trash2,
    Lock,
    Globe,
    MoreVertical,
    Loader2,
  } from "lucide-svelte";
  import CreateRepositoryModal from "$lib/components/artifacts/CreateRepositoryModal.svelte";
  import DeleteRepositoryModal from "$lib/components/artifacts/DeleteRepositoryModal.svelte";
  import UploadArtifactModal from "$lib/components/artifacts/UploadArtifactModal.svelte";
  import type { ArtifactRepository } from "$lib/types/artifacts.svelte";

  // STATE
  let createModalOpen = $state(false);
  let uploadModalOpen = $state(false);
  let deleteModalOpen = $state(false);
  let selectedRepo = $state<ArtifactRepository | null>(null);
  let menuOpen = $state<string | null>(null);

  // LOAD REPOS ON MOUNT
  $effect(() => {
    artifacts.fetchRepositories().catch(console.error);
  });

  function formatDate(date: string) {
    return formatDistance(new Date(date), new Date(), { addSuffix: true });
  }
</script>

<div class="space-y-6">
  <!-- HEADER -->
  <div class="sm:flex sm:items-center sm:justify-between">
    <div>
      <h1 class="text-2xl font-semibold text-gray-900">
        Artifact Repositories
      </h1>
      <p class="mt-2 text-sm text-gray-700">
        Manage and organize your build artifacts and dependencies
      </p>
    </div>
    <div class="mt-4 sm:mt-0">
      <button
        onclick={() => (createModalOpen = true)}
        class="inline-flex items-center px-4 py-2 border border-transparent rounded-md shadow-sm text-sm font-medium text-white bg-blue-600 hover:bg-blue-700"
      >
        <Plus class="h-4 w-4 mr-2" />
        New Repository
      </button>
    </div>
  </div>

  <!-- SEARCH -->
  <div class="relative">
    <input
      type="text"
      placeholder="Search repositories..."
      bind:value={artifacts.repoSearchTerm}
      class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
    />
    <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
  </div>

  <!-- REPOSITORY GRID -->
  {#if artifacts.loading}
    <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
  {:else if artifacts.filteredRepos.length === 0}
    <div class="text-center py-12 bg-white rounded-lg shadow">
      <Package class="mx-auto h-12 w-12 text-gray-400" />
      <h3 class="mt-2 text-sm font-medium text-gray-900">
        No repositories found
      </h3>
      <p class="mt-1 text-sm text-gray-500">
        Get started by creating your first artifact repository
      </p>
      <div class="mt-6">
        <button
          onclick={() => (createModalOpen = true)}
          class="inline-flex items-center px-4 py-2 border border-transparent rounded-md shadow-sm text-sm font-medium text-white bg-blue-600 hover:bg-blue-700"
        >
          <Plus class="h-4 w-4 mr-2" />
          Create Repository
        </button>
      </div>
    </div>
  {:else}
    <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
      {#each artifacts.filteredRepos as repo}
        <div
          class="bg-white shadow rounded-lg hover:shadow-lg transition-all duration-200"
        >
          <div class="p-6 flex flex-col h-full">
            <!-- HEADER -->
            <div class="flex justify-between items-start">
              <a href="/artifacts/{repo.name}" class="group flex-1 min-w-0">
                <div class="flex items-start">
                  <Package
                    class="h-6 w-6 flex-shrink-0 text-gray-400 group-hover:text-blue-500 transition-colors"
                  />
                  <div class="ml-3 min-w-0">
                    <h3
                      class="text-lg font-semibold text-gray-900 group-hover:text-blue-600 truncate"
                    >
                      {repo.name}
                    </h3>
                    <div class="mt-2 prose prose-sm max-w-none text-gray-600">
                      {#if repo.description}
                        <p
                          class="whitespace-pre-line leading-relaxed break-words line-clamp-2"
                        >
                          {repo.description}
                        </p>
                      {:else}
                        <p class="text-gray-400 italic">
                          No description provided
                        </p>
                      {/if}
                    </div>
                  </div>
                </div>
              </a>

              <!-- ACTIONS MENU -->
              <div class="relative flex-shrink-0 ml-4">
                <button
                  onclick={() =>
                    (menuOpen = menuOpen === repo.name ? null : repo.name)}
                  class="p-1 rounded-full hover:bg-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2"
                >
                  <MoreVertical class="h-5 w-5 text-gray-400" />
                </button>

                {#if menuOpen === repo.name}
                  <div
                    class="absolute right-0 mt-2 w-48 rounded-md shadow-lg bg-white ring-1 ring-black ring-opacity-5 z-10"
                    use:clickOutside={() => (menuOpen = null)}
                  >
                    <div class="py-1">
                      <button
                        onclick={() => {
                          selectedRepo = repo;
                          uploadModalOpen = true;
                          menuOpen = null;
                        }}
                        class="flex w-full items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100"
                      >
                        <Upload class="h-4 w-4 mr-2" />
                        Upload Artifact
                      </button>
                      <button
                        onclick={() => {
                          selectedRepo = repo;
                          deleteModalOpen = true;
                          menuOpen = null;
                        }}
                        class="flex w-full items-center px-4 py-2 text-sm text-red-600 hover:bg-red-50"
                      >
                        <Trash2 class="h-4 w-4 mr-2" />
                        Delete Repository
                      </button>
                    </div>
                  </div>
                {/if}
              </div>
            </div>

            <!-- METADATA -->
            <div class="mt-6 flex items-center justify-between text-sm">
              <div class="flex items-center text-gray-500">
                {#if repo.private}
                  <Lock class="h-4 w-4 mr-1" />
                  <span class="font-medium">Private</span>
                {:else}
                  <Globe class="h-4 w-4 mr-1 text-green-500" />
                  <span class="font-medium text-green-600">Public</span>
                {/if}
              </div>
              <span class="text-gray-500 font-medium">
                Updated {formatDate(repo.updated_at)}
              </span>
            </div>

            <!-- STATS -->
            <div class="mt-6 pt-4 border-t border-gray-100">
              <div class="flex justify-between text-sm">
                <span class="font-medium text-gray-600"
                  >{Object.keys(artifacts.artifacts[repo.id] || {}).length} artifacts</span
                >
                <span class="font-medium text-gray-600"
                  >{artifacts.sumArtifacts(artifacts.artifacts[repo.id] || [])} total</span
                >
              </div>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <!-- MODALS -->
  {#if createModalOpen}
    <CreateRepositoryModal onclose={() => (createModalOpen = false)} />
  {/if}

  {#if uploadModalOpen && selectedRepo}
    <UploadArtifactModal
      repository={selectedRepo}
      onclose={() => {
        uploadModalOpen = false;
        selectedRepo = null;
      }}
    />
  {/if}

  {#if deleteModalOpen && selectedRepo}
    <DeleteRepositoryModal
      repository={selectedRepo}
      onclose={() => {
        deleteModalOpen = false;
        selectedRepo = null;
      }}
    />
  {/if}
</div>
