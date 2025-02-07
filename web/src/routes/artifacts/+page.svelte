<script lang="ts">
    import { auth, login } from "$lib/stores/auth.svelte";
    import { artifacts } from "$lib/stores/artifacts.svelte";
    import CreateRepositoryModal from "$lib/components/artifacts/CreateRepositoryModal.svelte";

    let dragOverGlobal = $state(false);
    let droppedFilesGlobal: FileList | null = null;
    import DeleteRepositoryModal from "$lib/components/artifacts/DeleteRepositoryModal.svelte";
    import UploadArtifactModal from "$lib/components/artifacts/UploadArtifactModal.svelte";
    import type {
        Artifact,
        ArtifactRepository,
    } from "$lib/types/artifacts.svelte";
    import {
        Package,
        Upload,
        Trash2,
        Plus,
        Search,
        Globe,
        Lock,
        Loader2,
    } from "lucide-svelte";
    import { formatDistance } from "date-fns";

    let createModalOpen = $state(false);
    let uploadModalOpen = $state(false);
    let deleteModalOpen = $state(false);
    let selectedRepo = $state<ArtifactRepository | null>(null);
    let uploadFiles = $state<FileList | null>(null);

    $effect(() => {
        artifacts.fetchRepositories().catch(console.error);
    });

    function openUploadModal(repo: ArtifactRepository) {
        selectedRepo = repo;
        uploadModalOpen = true;
    }

    function openDeleteModal(repo: ArtifactRepository) {
        selectedRepo = repo;
        deleteModalOpen = true;
    }

    function handleDragOverGlobal(e: DragEvent) {
        e.preventDefault();
        e.stopPropagation();
        dragOverGlobal = true;
    }
    function handleDragLeaveGlobal(e: DragEvent) {
        e.preventDefault();
        e.stopPropagation();
        dragOverGlobal = false;
    }
    function handleDropGlobal(e: DragEvent) {
        e.preventDefault();
        e.stopPropagation();
        dragOverGlobal = false;
        if (e.dataTransfer?.files.length) {
            droppedFilesGlobal = e.dataTransfer.files;
            uploadFiles = droppedFilesGlobal;
            if (selectedRepo) {
                uploadModalOpen = true;
            }
        }
    }
</script>

<div class="space-y-6">
    <!-- HEADER -->
    <div class="sm:flex sm:items-center sm:justify-between">
        <div>
            <h1 class="text-2xl font-semibold text-gray-900">
                Artifact Storage Repositories
            </h1>
            <p class="mt-2 text-sm text-gray-700">
                Manage and organize your build artifacts and dependencies
            </p>
        </div>
        <div class="mt-4 sm:mt-0 sm:ml-16 sm:flex-none">
            <button
                onclick={() => (createModalOpen = true)}
                class="inline-flex items-center justify-center rounded-md border border-transparent bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 sm:w-auto"
            >
                <Plus class="h-4 w-4 mr-2" />
                New Repository
            </button>
        </div>
    </div>

    <!-- SEARCH -->
    <div class="flex flex-col sm:flex-row gap-4">
        <div class="relative">
            <input
                type="text"
                placeholder="Search repositories..."
                class="block w-full pl-10 pr-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                bind:value={artifacts.searchTerm}
            />
            <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
        </div>
    </div>

    <!-- REPO GRID -->
    {#if artifacts.loading}
    <div
      class="flex justify-center py-12"
      ondragover={handleDragOverGlobal}
      ondragleave={handleDragLeaveGlobal}
      ondrop={handleDropGlobal}
      role="application"
    >
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
    {:else if artifacts.filtered.length === 0}
    <div
      class="text-center py-12"
      ondragover={handleDragOverGlobal}
      ondragleave={handleDragLeaveGlobal}
      ondrop={handleDropGlobal}
      role="application"
    >
      <Package class="mx-auto h-12 w-12 text-gray-400" />
      <h3 class="mt-2 text-sm font-medium text-gray-900">No artifact repositories</h3>
      <p class="mt-1 text-sm text-gray-500">
        Get started by creating a new artifact repository
      </p>
    </div>
    {:else}
        <div class="grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-3"
            ondragover={handleDragOverGlobal}
            ondragleave={handleDragLeaveGlobal}
            ondrop={handleDropGlobal}
            role="application"
        >
            {#each artifacts.filtered as repo}
                <div
                    class="relative rounded-lg border border-gray-300 bg-white px-6 py-5 shadow-sm hover:border-gray-400 focus-within:ring-2 focus-within:ring-blue-500 focus-within:ring-offset-2"
                >
                    <div class="flex items-center space-x-3">
                        <div class="flex-shrink-0">
                            <Package class="h-6 w-6 text-gray-400" />
                        </div>
                        <div class="min-w-0 flex-1 hover:underline">
                            <a
                                href={`/artifacts/${repo.name}`}
                                class="focus:outline-none"
                            >
                                <p class="text-sm font-medium text-gray-900">
                                    {repo.name}
                                </p>
                                <p class="truncate text-sm text-gray-500">
                                    {repo.description}
                                </p>
                            </a>
                        </div>
                        <div class="flex-shrink-0">
                            {#if repo.private}
                                <Lock class="h-4 w-4 text-gray-500" />
                            {:else}
                                <Globe class="h-4 w-4 text-green-500" />
                            {/if}
                        </div>
                    </div>
                    <div class="mt-4 flex items-center justify-between text-sm">
                        <div class="text-gray-500">
                            {artifacts.artifacts[repo.id]?.length || 0} artifacts
                        </div>
                        <div class="flex space-x-2">
                            <button
                                onclick={() => openUploadModal(repo)}
                                class="inline-flex items-center rounded-md bg-white px-2.5 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2"
                            >
                                <Upload class="h-4 w-4 mr-1" />
                                Upload
                            </button>
                            <button
                                onclick={() => openDeleteModal(repo)}
                                class="inline-flex items-center rounded-md bg-white px-2.5 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-offset-2"
                            >
                                <Trash2 class="h-4 w-4 mr-1" />
                                Delete
                            </button>
                        </div>
                    </div>
                </div>
            {/each}
        </div>
    {/if}
</div>

{#if createModalOpen}
    <CreateRepositoryModal onclose={() => (createModalOpen = false)} />
{/if}

{#if uploadModalOpen && selectedRepo}
    <UploadArtifactModal
        repository={selectedRepo}
        initialFiles={uploadFiles}
        onclose={() => {
            uploadModalOpen = false;
            selectedRepo = null;
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
