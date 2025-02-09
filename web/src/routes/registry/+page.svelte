<script lang="ts">
    import {
        Search,
        Trash2,
        Tag as TagIcon,
        Package,
        Loader2,
        AlertCircle,
        Lock,
        Globe,
    } from "lucide-svelte";
    import Tag from "$lib/components/Tag.svelte";
    import { registry } from "$lib/stores/registry.svelte";
    import { auth, api } from "$lib/stores/auth.svelte";
    import { formatDistance } from "date-fns";
    import type { ImageRepository, VisibilityUpdateRequest } from '$lib/types/registry.svelte';
    import { showToast } from "$lib/stores/toast.svelte";
    import Toast from "$lib/components/Toast.svelte";

    let error = $state<string | null>(null);
    let deleteConfirmation = $state<{
        repository: string;
        tag: string;
    } | null>(null);

    let visibilityModal = $state<{
        repository: ImageRepository | null;
        action: "public" | "private";
    } | null>(null);
    let request = $state<VisibilityUpdateRequest | null>(null);

    // MODAL
    async function toggleVisibility(repository: ImageRepository) {
        request = {
            id: repository.id,
            private: !repository.private,
        };

        visibilityModal = {
            repository,
            action: request.private ? 'private' : 'public'
        };
    }

    async function confirmVisibilityChange() {
        if (!visibilityModal?.repository) return;

        try {
            await api.post("/api/v1/repositories/visibility", request);
            await registry.fetchRepositories();
            visibilityModal = null;
        } catch (err) {
            error =
                err instanceof Error
                    ? err.message
                    : "Failed to update visibility";
        }
    }

    $effect(() => {
        if (auth.isAuthenticated) {
            registry.fetchRepositories();
        }
    });

    $effect(() => {
        if (error) {
            showToast(error, 'error');
            error = null;
        }
    })

    async function handleDelete() {
        if (!deleteConfirmation) return;

        try {
            await registry.deleteTag(
                deleteConfirmation.repository,
                deleteConfirmation.tag,
            );
            deleteConfirmation = null;
        } catch (e) {
            console.log("Unabled to delete!", e);
        }
    }

    function formatDate(date: string): string {
        return formatDistance(new Date(date), new Date(), { addSuffix: true });
    }
</script>

<div class="space-y-6">
    <!-- HEADER AND SEARCH -->
    <div class="sm:flex sm:items-center sm:justify-between gap-4">
        <div>
            <h1 class="text-2xl font-semibold text-gray-900">
                Container Registry
            </h1>
            <p class="mt-1 text-sm text-gray-600">
                Manage and monitor your container images and tags
            </p>
        </div>
        <div class="mt-4 sm:mt-0">
            <div class="relative">
                <input
                    type="text"
                    placeholder="Search repositories..."
                    bind:value={registry.searchTerm}
                    class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-lg
                           focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500
                           text-sm placeholder-gray-400"
                />
                <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
            </div>
        </div>
    </div>

    <!-- ERROR STATE -->
    <Toast></Toast>

    <!-- LOADING STATE -->
    {#if registry.loading}
        <div class="flex justify-center py-12">
            <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
        </div>
    {:else}
        <!-- EMPTY STATE -->
        {#if registry.repositories.length === 0}
            <div
                class="text-center py-12 px-4 rounded-lg border-2 border-dashed border-gray-300 bg-white"
            >
                <Package class="mx-auto h-12 w-12 text-gray-400" />
                <h3 class="mt-2 text-sm font-semibold text-gray-900">
                    No repositories
                </h3>
                <p class="mt-1 text-sm text-gray-500">
                    Get started by pushing your first container image
                </p>
                <div class="mt-6">
                    <button
                        type="button"
                        class="inline-flex items-center px-4 py-2 border border-transparent shadow-sm text-sm
                               font-medium rounded-md text-white bg-blue-600 hover:bg-blue-700
                               focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500"
                    >
                        <Package class="h-4 w-4 mr-2" />
                        View Documentation
                    </button>
                </div>
            </div>
        {:else}
            <!-- REPOSITORY LIST -->
            <div class="bg-white shadow-sm rounded-lg divide-y divide-gray-200">
                {#each registry.filtered as repository (repository.name)}
                    <div class="p-6 space-y-4">
                        <div class="flex items-center justify-between">
                            <div>
                                <h3
                                    class="text-lg font-medium text-gray-900 flex items-center"
                                >
                                    <Package
                                        class="h-5 w-5 mr-2 text-gray-500"
                                    />
                                    {repository.name}
                                </h3>
                                <button
                                    onclick={() => toggleVisibility(repository)}
                                    class="mt-1 inline-flex items-center px-2 py-1 rounded-md text-sm
                                        {repository.private
                                        ? 'text-gray-600 bg-gray-100 hover:bg-gray-200'
                                        : 'text-green-600 bg-green-100 hover:bg-green-200'}"
                                >
                                    {#if repository.private}
                                        <Lock class="h-4 w-4 mr-1" />
                                        Private
                                    {:else}
                                        <Globe class="h-4 w-4 mr-1" />
                                        Public
                                    {/if}
                                </button>
                                <p class="mt-1 text-sm text-gray-500">
                                    Last updated {formatDate(
                                        repository.updated_at,
                                    )}
                                </p>
                            </div>
                            <div class="text-sm text-gray-500">
                                {repository.tags.length} tags
                            </div>
                        </div>

                        <!-- TAGS -->
                        <div class="space-y-2">
                            {#each repository.tags as tag (tag.name)}
                                <div
                                    class="flex items-center justify-between py-2 px-4 bg-gray-50 rounded-lg"
                                >
                                    <div class="flex items-center space-x-3">
                                        <Tag
                                            name={repository.name}
                                            tag={tag.name}
                                        />
                                        <span class="text-sm text-gray-500">
                                            {registry.formatSize(tag.size)}
                                        </span>
                                    </div>
                                    <div class="flex items-center space-x-4">
                                        <span class="text-sm text-gray-500">
                                            {formatDate(tag.created)}
                                        </span>
                                        <button
                                            type="button"
                                            class="text-gray-400 hover:text-red-600 transition-colors duration-150"
                                            onclick={() =>
                                                (deleteConfirmation = {
                                                    repository: repository.name,
                                                    tag: tag.name,
                                                })}
                                        >
                                            <Trash2 class="h-4 w-4" />
                                        </button>
                                    </div>
                                </div>
                            {/each}
                        </div>
                    </div>
                {/each}
            </div>
        {/if}
    {/if}
</div>

<!-- DELETE CONFIRMATION MODAL -->
{#if deleteConfirmation}
    <div
        class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50"
    >
        <div class="fixed inset-0 z-10 overflow-y-auto">
            <div
                class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0"
            >
                <div
                    class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6"
                >
                    <div class="sm:flex sm:items-start">
                        <div
                            class="mx-auto flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full bg-red-100 sm:mx-0 sm:h-10 sm:w-10"
                        >
                            <AlertCircle class="h-6 w-6 text-red-600" />
                        </div>
                        <div
                            class="mt-3 text-center sm:ml-4 sm:mt-0 sm:text-left"
                        >
                            <h3
                                class="text-base font-semibold leading-6 text-gray-900"
                            >
                                Delete Tag
                            </h3>
                            <div class="mt-2">
                                <p class="text-sm text-gray-500">
                                    Are you sure you want to delete
                                    <span class="font-semibold"
                                        >{deleteConfirmation.tag}</span
                                    >
                                    from
                                    <span class="font-semibold"
                                        >{deleteConfirmation.repository}</span
                                    >? This action cannot be undone.
                                </p>
                            </div>
                        </div>
                    </div>
                    <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
                        <button
                            type="button"
                            class="inline-flex w-full justify-center rounded-md bg-red-600 px-3 py-2 text-sm
                                   font-semibold text-white shadow-sm hover:bg-red-500 sm:ml-3 sm:w-auto"
                            onclick={handleDelete}
                        >
                            Delete
                        </button>
                        <button
                            type="button"
                            class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2
                                   text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset
                                   ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                            onclick={() => (deleteConfirmation = null)}
                        >
                            Cancel
                        </button>
                    </div>
                </div>
            </div>
        </div>
    </div>
{/if}

{#if visibilityModal}
    <div
        class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50"
    >
        <div class="fixed inset-0 z-10 overflow-y-auto">
            <div
                class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0"
            >
                <div
                    class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6"
                >
                    <div class="sm:flex sm:items-start">
                        <div
                            class="mx-auto flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full {visibilityModal.action ===
                            'private'
                                ? 'bg-yellow-100'
                                : 'bg-blue-100'} sm:mx-0 sm:h-10 sm:w-10"
                        >
                            {#if visibilityModal.action === "private"}
                                <Lock class="h-6 w-6 text-yellow-600" />
                            {:else}
                                <Globe class="h-6 w-6 text-blue-600" />
                            {/if}
                        </div>
                        <div
                            class="mt-3 text-center sm:ml-4 sm:mt-0 sm:text-left"
                        >
                            <h3
                                class="text-base font-semibold leading-6 text-gray-900"
                            >
                                Make Repository {visibilityModal.action ===
                                "private"
                                    ? "Private"
                                    : "Public"}
                            </h3>
                            <div class="mt-2">
                                <p class="text-sm text-gray-500">
                                    {#if visibilityModal.action === "private"}
                                        Are you sure you want to make <span
                                            class="font-semibold"
                                            >{visibilityModal?.repository
                                                ?.name}</span
                                        > private? This will hide it from the public
                                        registry.
                                    {:else}
                                        Are you sure you want to make <span
                                            class="font-semibold"
                                            >{visibilityModal?.repository
                                                ?.name}</span
                                        > public? This will make it visible to all
                                        users.
                                    {/if}
                                </p>
                            </div>
                        </div>
                    </div>
                    <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
                        <button
                            type="button"
                            class="inline-flex w-full justify-center rounded-md px-3 py-2 text-sm font-semibold text-white shadow-sm sm:ml-3 sm:w-auto
                                   {visibilityModal.action === 'private'
                                ? 'bg-yellow-600 hover:bg-yellow-500'
                                : 'bg-blue-600 hover:bg-blue-500'}"
                            onclick={confirmVisibilityChange}
                        >
                            {visibilityModal.action === "private"
                                ? "Make Private"
                                : "Make Public"}
                        </button>
                        <button
                            type="button"
                            class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                            onclick={() => (visibilityModal = null)}
                        >
                            Cancel
                        </button>
                    </div>
                </div>
            </div>
        </div>
    </div>
{/if}
