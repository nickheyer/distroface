<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { formatDistance } from "date-fns";
  import {
    AlertCircle,
    Package,
    Search,
    Upload,
    Loader2,
    Download,
    Trash2,
    Edit,
    PlusCircle,
  } from "lucide-svelte";
  import CreateRepositoryModal from "./CreateRepositoryModal.svelte";
  import DeleteRepositoryModal from "./DeleteRepositoryModal.svelte";
  import UploadArtifactModal from "./UploadArtifactModal.svelte";
  import MetadataEditor from "./MetadataEditor.svelte";
  import type {
    Artifact,
    ArtifactRepository,
  } from "$lib/types/artifacts.svelte";

  let { initialRepo = null } = $props<{
    initialRepo?: ArtifactRepository | null;
  }>();

  // MODAL STATES
  let createModalOpen = $state(false);
  let uploadModalOpen = $state(false);
  let deleteModalOpen = $state(false);
  let metadataModalOpen = $state(false);
  let selectedArtifact = $state<Artifact | null>(null);
  let selectedRepo = $state<ArtifactRepository | null>(null);
  let selectedUploadRepo = $state<ArtifactRepository | null>(null);
  let uploadFiles = $state<FileList | null>(null);

  // Get current artifacts for selected repo
  let currentArtifacts = $state<Artifact[]>([]);

  $effect(() => {
    if (artifacts.currentRepo) {
      currentArtifacts = artifacts.artifacts[artifacts.currentRepo.id] || [];
    } else {
      currentArtifacts = [];
    }
  });

  // INITIALIZE
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

  function handleDrop(e: DragEvent, repo: ArtifactRepository) {
    e.preventDefault();

    if (e.dataTransfer?.files) {
      uploadFiles = e.dataTransfer.files;
      selectedUploadRepo = repo;
      uploadModalOpen = true;
    }
  }

  function handleDelete(repo: ArtifactRepository, e: Event) {
    e.preventDefault();
    e.stopPropagation();
    selectedRepo = repo;
    deleteModalOpen = true;
  }

  function handleUpload(repo: ArtifactRepository, e: Event) {
    e.preventDefault();
    e.stopPropagation();
    selectedUploadRepo = repo;
    uploadModalOpen = true;
  }

  function selectRepository(repo: ArtifactRepository) {
    artifacts.currentRepo = repo;
  }

  async function downloadArtifact(artifact: Artifact) {
    if (!artifacts.currentRepo) return;
    const url = `/api/v1/artifacts/${artifacts.currentRepo.name}/${artifact.version}/${artifact.name}`;
    window.open(url, "_blank");
  }

  async function deleteArtifact(artifact: Artifact) {
    if (!artifacts.currentRepo) return;

    if (confirm(`Are you sure you want to delete ${artifact.name}?`)) {
      try {
        await artifacts.deleteArtifact(
          artifacts.currentRepo.name,
          artifact.version,
          encodeURIComponent(artifact.name),
        );
        await artifacts.fetchArtifacts(artifacts.currentRepo.name);
      } catch (err) {
        console.error("Failed to delete artifact:", err);
      }
    }
  }
</script>

<div class="space-y-8">
  <!-- HEADER -->
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-semibold text-gray-900">Artifact Repository</h1>
      <p class="mt-2 text-sm text-gray-700">
        Store and manage your build artifacts
      </p>
    </div>
    <button
      type="button"
      onclick={() => (createModalOpen = true)}
      class="inline-flex items-center px-4 py-2 border border-transparent rounded-md shadow-sm text-sm font-medium text-white bg-blue-600 hover:bg-blue-700"
    >
      <PlusCircle class="h-4 w-4 mr-2" />
      New Repository
    </button>
  </div>

  <!-- SEARCH AND FILTERS -->
  <div class="relative">
    <input
      type="text"
      placeholder="Search repositories..."
      bind:value={artifacts.searchTerm}
      class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-lg focus:ring-blue-500 focus:border-blue-500 text-sm"
    />
    <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
  </div>

  <!-- REPOSITORY GRID -->
  {#if artifacts.loading}
    <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
  {:else if artifacts.filtered.length === 0}
    <div class="text-center py-12">
      <Package class="mx-auto h-12 w-12 text-gray-400" />
      <h3 class="mt-2 text-sm font-medium text-gray-900">
        No repositories found
      </h3>
      <p class="mt-1 text-sm text-gray-500">
        {artifacts.searchTerm
          ? "Try adjusting your search terms"
          : "Create a repository to get started"}
      </p>
    </div>
  {:else}
    <div class="grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-3">
      {#each artifacts.filtered as repo}
        <div
          class="relative group rounded-lg border border-gray-200 bg-white p-6 hover:border-blue-300 transition-all duration-150"
          ondragover={(e) => e.preventDefault()}
          ondrop={(e) => handleDrop(e, repo)}
          role="application"
        >
          <div class="flex flex-col h-full">
            <div class="flex items-center space-x-3">
              <Package class="h-6 w-6 text-gray-400" />
              <div class="flex-1 min-w-0">
                <a
                  href={`/artifacts/${repo.name}`}
                  class="block"
                  onclick={(e) => {
                    e.preventDefault();
                    selectRepository(repo);
                  }}
                >
                  <h3 class="text-sm font-medium text-gray-900 truncate">
                    {repo.name}
                  </h3>
                  <p class="text-sm text-gray-500 truncate">
                    {repo.description || "No description"}
                  </p>
                </a>
              </div>
            </div>

            <div class="mt-4 flex items-center justify-between text-sm">
              <span class="text-gray-500">
                {artifacts.artifacts[repo.id]?.length || 0} artifacts
              </span>
              <div class="flex space-x-2">
                <button
                  type="button"
                  onclick={(e) => handleUpload(repo, e)}
                  class="inline-flex items-center p-2 rounded-md text-gray-500 hover:text-blue-600 hover:bg-blue-50"
                >
                  <Upload class="h-4 w-4" />
                </button>
                <button
                  type="button"
                  onclick={(e) => handleDelete(repo, e)}
                  class="inline-flex items-center p-2 rounded-md text-gray-500 hover:text-red-600 hover:bg-red-50"
                >
                  <Trash2 class="h-4 w-4" />
                </button>
              </div>
            </div>

            <div class="mt-2 text-xs text-gray-500">
              Updated {formatDistance(new Date(repo.updated_at), new Date(), {
                addSuffix: true,
              })}
            </div>
          </div>
        </div>
      {/each}
    </div>

    <!-- Artifact List Section -->
    {#if artifacts.currentRepo}
      <div class="mt-8">
        <div class="bg-white shadow rounded-lg">
          <div class="px-4 py-5 sm:p-6">
            <h3 class="text-lg font-medium text-gray-900">
              Artifacts in {artifacts.currentRepo.name}
            </h3>

            {#if currentArtifacts.length === 0}
              <div class="text-center py-8">
                <p class="text-gray-500">
                  No artifacts found in this repository.
                </p>
              </div>
            {:else}
              <div class="mt-4 space-y-4">
                {#each currentArtifacts as artifact}
                  <div
                    class="flex items-center justify-between p-4 bg-gray-50 rounded-lg"
                  >
                    <div class="flex items-center space-x-4">
                      <Package class="h-5 w-5 text-gray-400" />
                      <div>
                        <h4 class="text-sm font-medium text-gray-900">
                          {artifact.name}
                        </h4>
                        <p class="text-sm text-gray-500">
                          Version {artifact.version}
                        </p>
                      </div>
                    </div>
                    <div class="flex items-center space-x-2">
                      <span class="text-sm text-gray-500">
                        {artifacts.formatSize(artifact.size)}
                      </span>
                      <div class="flex space-x-2">
                        <button
                          onclick={() => downloadArtifact(artifact)}
                          class="p-1 text-gray-400 hover:text-blue-600"
                          title="Download"
                        >
                          <Download class="h-4 w-4" />
                        </button>
                        <button
                          onclick={() => {
                            selectedArtifact = artifact;
                            metadataModalOpen = true;
                          }}
                          class="p-1 text-gray-400 hover:text-gray-600"
                          title="Edit metadata"
                        >
                          <Edit class="h-4 w-4" />
                        </button>
                        <button
                          onclick={() => deleteArtifact(artifact)}
                          class="p-1 text-gray-400 hover:text-red-600"
                          title="Delete"
                        >
                          <Trash2 class="h-4 w-4" />
                        </button>
                      </div>
                    </div>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      </div>
    {/if}
  {/if}

  <!-- MODALS -->

  <!-- MODALS -->
  {#if createModalOpen}
    <CreateRepositoryModal onclose={() => (createModalOpen = false)} />
  {/if}

  {#if uploadModalOpen && selectedUploadRepo}
    <UploadArtifactModal
      repository={selectedUploadRepo}
      initialFiles={uploadFiles}
      onclose={() => {
        uploadModalOpen = false;
        selectedUploadRepo = null;
        uploadFiles = null;
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

  {#if metadataModalOpen && selectedArtifact && artifacts.currentRepo}
    <MetadataEditor
      artifact={selectedArtifact}
      repository={artifacts.currentRepo}
      onclose={() => {
        metadataModalOpen = false;
        selectedArtifact = null;
      }}
    />
  {/if}
</div>
