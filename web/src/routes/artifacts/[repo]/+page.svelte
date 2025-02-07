<script lang="ts">
    import { showToast } from '$lib/stores/toast.svelte';
    import { auth } from "$lib/stores/auth.svelte";
    import { artifacts } from "$lib/stores/artifacts.svelte";
    import { page } from "$app/state";
    import { formatDistance } from "date-fns";
    import {
        Package,
        Upload,
        Download,
        Trash2,
        Edit,
        Loader2,
        AlertCircle,
    } from "lucide-svelte";
    import type {
        ArtifactRepository,
        Artifact,
    } from "$lib/types/artifacts.svelte";
    import UploadArtifactModal from "$lib/components/artifacts/UploadArtifactModal.svelte";
    import MetadataEditor from "$lib/components/artifacts/MetadataEditor.svelte";

    // STATE
    let repository = $state<ArtifactRepository | null>(null);
    let artifacts_list = $state<Artifact[]>([]);
    let loading = $state(true);
    let error = $state("");
    let uploadModalOpen = $state(false);
    let metadataModalOpen = $state(false);
    let selectedArtifact = $state<Artifact | null>(null);
    let dragOver = $state(false);

    function handleDragOver(e: DragEvent) {
        e.preventDefault(); // PREVENT DEFAULT
        dragOver = true; // SETTING DRAG STATE
    }

    function handleDragLeave() {
        dragOver = false;
    }

    // THIS IS ARBITRARY AS HELL, BUT I CANT PROPOGATE UNTIL I SAVE TO VARIABLE
    let droppedFiles: FileList | null = null;
    function handleDrop(e: DragEvent) {
        e.preventDefault();
        dragOver = false;

        if (e.dataTransfer?.files.length) {
            droppedFiles = e.dataTransfer.files;
            uploadModalOpen = true;
        }
    }

    async function handleOperation<T>(
        operation: () => Promise<T>,
        successMessage: string,
        errorPrefix: string
    ): Promise<T | undefined> {
        try {
            loading = true;
            const result = await operation();
            showToast(successMessage, 'success');
            return result;
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'An unexpected error occurred';
            showToast(`${errorPrefix}: ${errorMessage}`, 'error');
            return undefined;
        } finally {
            loading = false;
        }
    }

    async function loadRepository() {
        loading = true;
        error = "";
        const repoName = page.params.repo;

        try {
            await artifacts.fetchRepositories();
            repository =
                artifacts.repositories.find((r) => r.name === repoName) || null;

            if (repository) {
                await artifacts.fetchArtifacts(repository.name);
                artifacts_list = artifacts.artifacts[repository.id] || [];
            } else {
                error = "Repository not found";
            }
        } catch (err) {
            error =
                err instanceof Error
                    ? err.message
                    : "Failed to load repository";
        } finally {
            loading = false;
        }
    }

    async function deleteArtifact(artifact: Artifact) {
        if (!repository?.name) return;
        
        if (confirm(`Are you sure you want to delete ${artifact.name}?`)) {
            await handleOperation(
            async () => {
                if (!repository?.name) return;
                await artifacts.deleteArtifact(repository.name, artifact.version, artifact.name);
                await loadRepository();
            },
            `Successfully deleted ${artifact.name}`,
            'Failed to delete artifact'
            );
        }
    }

    async function downloadArtifact(artifact: Artifact) {
    if (!repository) return;
    
    try {
        const response = await fetch(
            `/api/v1/artifacts/${repository.name}/${artifact.version}/${artifact.name}`,
            {
                headers: {
                    Authorization: `Bearer ${auth.token}`
                }
            }
        );
        
        if (!response.ok) throw new Error('Download failed');
        const blob = await response.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = artifact.name;
        document.body.appendChild(a);
        a.click();
        window.URL.revokeObjectURL(url);
        document.body.removeChild(a);
    } catch (err) {
        error = err instanceof Error ? err.message : 'Download failed';
    }
}

    $effect(() => {
        loadRepository();
    });
</script>

<div class="space-y-6">
    {#if loading}
        <div class="flex justify-center py-12">
            <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
        </div>
    {:else if error}
        <div class="rounded-lg bg-red-50 p-4">
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
    {:else if repository}
        <!-- REPOSITORY HEADER -->
        <div class="bg-white shadow-sm rounded-lg p-6">
            <div class="flex items-center justify-between">
                <div class="flex items-center">
                    <Package class="h-8 w-8 text-gray-400" />
                    <div class="ml-4">
                        <h1 class="text-2xl font-semibold text-gray-900">
                            {repository.name}
                        </h1>
                        <p class="mt-1 text-sm text-gray-500">
                            {repository.description || "No description"}
                        </p>
                    </div>
                </div>
                <button
                    type="button"
                    onclick={() => (uploadModalOpen = true)}
                    class="inline-flex items-center px-4 py-2 border border-transparent rounded-md shadow-sm text-sm font-medium text-white bg-blue-600 hover:bg-blue-700"
                >
                    <Upload class="h-4 w-4 mr-2" />
                    Upload Artifact
                </button>
            </div>
        </div>

        <!-- DROP ZONE -->
        <div
            class="border-2 border-dashed rounded-lg p-12 text-center transition-colors
               {dragOver ? 'border-blue-500 bg-blue-50' : 'border-gray-300'}"
            ondragover={handleDragOver}
            ondragleave={handleDragLeave}
            ondrop={handleDrop}
            role='application'
        >
            <div class="space-y-2">
                <Upload class="mx-auto h-12 w-12 text-gray-400" />
                <p class="text-sm text-gray-600">
                    Drag and drop artifacts here or
                    <button
                        type="button"
                        onclick={() => (uploadModalOpen = true)}
                        class="text-blue-600 hover:text-blue-700 font-medium"
                    >
                        browse
                    </button>
                </p>
            </div>
        </div>

        <!-- ARTIFACTS TABLE -->
        {#if artifacts_list.length === 0}
            <div class="text-center py-12 bg-white rounded-lg shadow-sm">
                <Package class="mx-auto h-12 w-12 text-gray-400" />
                <h3 class="mt-2 text-sm font-medium text-gray-900">
                    No artifacts
                </h3>
                <p class="mt-1 text-sm text-gray-500">
                    Get started by uploading your first artifact
                </p>
            </div>
        {:else}
            <div class="bg-white shadow-sm rounded-lg overflow-hidden">
                <table class="min-w-full divide-y divide-gray-200">
                    <thead>
                        <tr>
                            <th
                                scope="col"
                                class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider"
                                >Name</th
                            >
                            <th
                                scope="col"
                                class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider"
                                >Version</th
                            >
                            <th
                                scope="col"
                                class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider"
                                >Size</th
                            >
                            <th
                                scope="col"
                                class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider"
                                >Updated</th
                            >
                            <th
                                scope="col"
                                class="px-6 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider"
                                >Actions</th
                            >
                        </tr>
                    </thead>
                    <tbody class="divide-y divide-gray-200">
                        {#each artifacts_list as artifact}
                            <tr>
                                <td
                                    class="px-6 py-4 whitespace-nowrap text-sm font-medium text-gray-900"
                                >
                                    {artifact.name}
                                </td>
                                <td
                                    class="px-6 py-4 whitespace-nowrap text-sm text-gray-500"
                                >
                                    {artifact.version}
                                </td>
                                <td
                                    class="px-6 py-4 whitespace-nowrap text-sm text-gray-500"
                                >
                                    {artifacts.formatSize(artifact.size)}
                                </td>
                                <td
                                    class="px-6 py-4 whitespace-nowrap text-sm text-gray-500"
                                >
                                    {formatDistance(
                                        new Date(artifact.updated_at),
                                        new Date(),
                                        { addSuffix: true },
                                    )}
                                </td>
                                <td
                                    class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium"
                                >
                                    <div class="flex justify-end space-x-2">
                                        <button
                                            type="button"
                                            onclick={() =>
                                                downloadArtifact(artifact)}
                                            class="text-blue-600 hover:text-blue-900"
                                            aria-label={`Download ${artifact.name}`}
                                        >
                                            <Download class="h-4 w-4" />
                                        </button>
                                        <button
                                            type="button"
                                            onclick={() => {
                                                selectedArtifact = artifact;
                                                metadataModalOpen = true;
                                            }}
                                            class="text-gray-600 hover:text-gray-900"
                                            aria-label={`Edit metadata for ${artifact.name}`}
                                        >
                                            <Edit class="h-4 w-4" />
                                        </button>
                                        <button
                                            type="button"
                                            onclick={() =>
                                                deleteArtifact(artifact)}
                                            class="text-red-600 hover:text-red-900"
                                            aria-label={`Delete ${artifact.name}`}
                                        >
                                            <Trash2 class="h-4 w-4" />
                                        </button>
                                    </div>
                                </td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
        {/if}

        <!-- MODALS -->
        {#if uploadModalOpen}
            <UploadArtifactModal
                {repository}
                onclose={() => (uploadModalOpen = false)}
            />
        {/if}

        {#if metadataModalOpen && selectedArtifact}
            <MetadataEditor
                artifact={selectedArtifact}
                {repository}
                onclose={() => {
                    metadataModalOpen = false;
                    selectedArtifact = null;
                }}
            />
        {/if}
    {/if}
</div>
