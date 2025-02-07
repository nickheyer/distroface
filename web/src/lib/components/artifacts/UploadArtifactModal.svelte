<script lang="ts">
    import { artifacts } from "$lib/stores/artifacts.svelte";
    import { Upload, X } from "lucide-svelte";
    import type { ArtifactRepository } from "$lib/types/artifacts.svelte";

    let {
        repository,
        initialFiles = null,
        onclose,
    } = $props<{
        repository: ArtifactRepository;
        initialFiles?: FileList | null;
        onclose: () => void;
    }>();

    let files = $state<FileList | null>(initialFiles);
    let version = $state("");
    let path = $state("");
    let loading = $state(false);
    let error = $state<string | null>(null);
    let dragOver = $state(false);
    let uploadProgress = $state(0);

    $effect(() => {
        if (files?.[0]) {
            version = files[0].name.replace(/\.[^/.]+$/, "");
            path = files[0].name;
        }
    });

    $effect(() => {
        const progresses = Object.values(artifacts.uploadProgress);
        if (progresses.length > 0) {
            uploadProgress = progresses[0];
        }
    });

    function handleDragOver(e: DragEvent) {
        e.preventDefault();
        dragOver = true;
    }

    function handleDragLeave() {
        dragOver = false;
    }

    function handleDrop(e: DragEvent) {
        e.preventDefault();
        dragOver = false;

        if (e.dataTransfer?.files) {
            files = e.dataTransfer.files;
        }
    }

    function handleFileInput(e: Event) {
        const input = e.target as HTMLInputElement;
        if (input.files) {
            files = input.files;
        }
    }

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
                path || files[0].name,
            );
            onclose();
        } catch (err) {
            error =
                err instanceof Error
                    ? err.message
                    : "Failed to upload artifact";
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
                        onclick={onclose}
                    >
                        <span class="sr-only">Close</span>
                        <X class="h-6 w-6" />
                    </button>
                </div>

                <div class="sm:flex sm:items-start">
                    <div class="mt-3 text-center sm:mt-0 sm:text-left w-full">
                        <h3
                            class="text-lg font-semibold leading-6 text-gray-900"
                        >
                            Upload Artifact to {repository.name}
                        </h3>

                        <form class="mt-4 space-y-4" onsubmit={handleSubmit}>
                            {#if error}
                                <div
                                    class="rounded-md bg-red-50 p-4 text-sm text-red-700"
                                >
                                    {error}
                                </div>
                            {/if}

                            <!-- DROPZONE -->
                            <div
                                class="mt-1 flex justify-center rounded-lg border-2 border-dashed px-6 py-10
                         {dragOver
                                    ? 'border-blue-500 bg-blue-50'
                                    : 'border-gray-300'}"
                                ondragover={handleDragOver}
                                ondragleave={handleDragLeave}
                                ondrop={handleDrop}
                                role="application"
                            >
                                <div class="text-center">
                                    <Upload
                                        class="mx-auto h-12 w-12 text-gray-400"
                                    />
                                    <div
                                        class="mt-4 flex text-sm leading-6 text-gray-600"
                                    >
                                        <label
                                            for="file-upload"
                                            class="relative cursor-pointer rounded-md bg-white font-semibold text-blue-600 focus-within:outline-none focus-within:ring-2 focus-within:ring-blue-600 focus-within:ring-offset-2 hover:text-blue-500"
                                        >
                                            <span>Upload a file</span>
                                            <input
                                                id="file-upload"
                                                name="file-upload"
                                                type="file"
                                                class="sr-only"
                                                onchange={handleFileInput}
                                            />
                                        </label>
                                        <p class="pl-1">or drag and drop</p>
                                    </div>
                                    {#if files?.[0]}
                                        <p class="text-sm text-gray-600 mt-2">
                                            Selected: {files[0].name}
                                        </p>
                                    {:else}
                                        <p class="text-xs text-gray-500 mt-2">
                                            Any file up to 10GB
                                        </p>
                                    {/if}
                                </div>
                            </div>

                            <div>
                                <label
                                    for="version"
                                    class="block text-sm font-medium text-gray-700"
                                >
                                    Version
                                </label>
                                <input
                                    type="text"
                                    id="version"
                                    bind:value={version}
                                    class="mt-1 block w-full px-4 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                                    placeholder="1.0.0"
                                    required
                                />
                            </div>

                            <div>
                                <label
                                    for="path"
                                    class="block text-sm font-medium text-gray-700"
                                >
                                    Path (optional)
                                </label>
                                <input
                                    type="text"
                                    id="path"
                                    bind:value={path}
                                    class="mt-1 block w-full px-4 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500"
                                    placeholder="folder/artifact.zip"
                                />
                            </div>

                            <div
                                class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse"
                            >
                                <button
                                    type="submit"
                                    disabled={loading}
                                    class="inline-flex w-full justify-center rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-500 sm:ml-3 sm:w-auto disabled:bg-gray-400"
                                >
                                    {loading ? "Uploading..." : "Upload"}
                                </button>
                                <button
                                    type="button"
                                    onclick={onclose}
                                    class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                                >
                                    Cancel
                                </button>
                            </div>

                            {#if loading && uploadProgress > 0}
                                <div
                                    class="absolute bottom-0 left-0 right-0 bg-gray-100 h-1"
                                >
                                    <div
                                        class="bg-blue-600 h-full transition-all duration-300"
                                        style="width: {uploadProgress}%"
                                    ></div>
                                </div>
                            {/if}
                        </form>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>
