<script lang="ts">
    import { api } from "$lib/stores/auth.svelte";
    import { artifacts } from "$lib/stores/artifacts.svelte";
    import { showToast } from "$lib/stores/toast.svelte";
    import DetailModal from "../DetailModal.svelte";
    import { Save } from "lucide-svelte";
    import type {
        ArtifactRepository,
        Artifact,
    } from "$lib/types/artifacts.svelte";

    let { artifact, repository, onclose } = $props<{
        artifact: Artifact;
        repository: ArtifactRepository;
        onclose: () => void;
    }>();

    let editName = $state(artifact.name);
    let editPath = $state(artifact.path);
    let editVersion = $state(artifact.version);
    let saving = $state(false);

    async function handleSave() {
        if (!editName) {
            showToast("Name is required", "error");
            return;
        }

        saving = true;
        try {
            await api.put(
                `/api/v1/artifacts/${repository.name}/${artifact.id}/rename`,
                {
                    name: editName,
                    path: editPath,
                    version: editVersion,
                },
            );

            await artifacts.fetchArtifacts(repository.name);
            showToast("Artifact updated successfully", "success");
            onclose();
        } catch (err) {
            showToast(
                err instanceof Error
                    ? err.message
                    : "Failed to update artifact",
                "error",
            );
        } finally {
            saving = false;
        }
    }

    function formatJson(str: string) {
        try {
            return JSON.stringify(JSON.parse(str), null, 2);
        } catch {
            return str;
        }
    }
</script>

<DetailModal title={`${artifact.name} Details`} {onclose}>
    <div class="space-y-6">
        <!-- WRITABLE FIELDS -->
        <div class="grid grid-cols-1 gap-4">
            <div>
                <label
                    for="artifact-detail-name"
                    class="block text-sm font-medium text-gray-700">Name</label
                >
                <input
                    id="artifact-detail-name"
                    type="text"
                    bind:value={editName}
                    class="block w-full px-3 py-2 text-sm rounded-md border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm"
                    placeholder="Artifact name"
                />
            </div>
            <div>
                <label
                    for="artifact-detail-path"
                    class="block text-sm font-medium text-gray-700"
                    >Path (optional)</label
                >
                <input
                    id="artifact-detail-path"
                    type="text"
                    bind:value={editPath}
                    class="block w-full px-3 py-2 text-sm rounded-md border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm"
                    placeholder="Path including directory structure"
                />
                <p class="mt-1 text-sm text-gray-500">
                    Leave as is to keep current directory structure
                </p>
            </div>
            <div>
                <label
                    for="artifact-detail-version"
                    class="block text-sm font-medium text-gray-700"
                    >Version</label
                >
                <input
                    id="artifact-detail-version"
                    type="text"
                    bind:value={editVersion}
                    class="block w-full px-3 py-2 text-sm rounded-md border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm"
                    placeholder="Version"
                />
            </div>
        </div>

        <!-- READ ONLY INFO -->
        <div class="bg-gray-50 px-4 py-3 rounded-lg">
            <dl class="grid grid-cols-2 gap-4">
                <div>
                    <dt class="text-sm font-medium text-gray-500">Size</dt>
                    <dd class="mt-1 text-sm text-gray-900">
                        {artifacts.formatSize(artifact.size)}
                    </dd>
                </div>
                <div>
                    <dt class="text-sm font-medium text-gray-500">MIME Type</dt>
                    <dd class="mt-1 text-sm text-gray-900">
                        {artifact.mime_type}
                    </dd>
                </div>
                <div>
                    <dt class="text-sm font-medium text-gray-500">Created</dt>
                    <dd class="mt-1 text-sm text-gray-900">
                        {new Date(artifact.created_at).toLocaleString()}
                    </dd>
                </div>
                <div>
                    <dt class="text-sm font-medium text-gray-500">Updated</dt>
                    <dd class="mt-1 text-sm text-gray-900">
                        {new Date(artifact.updated_at).toLocaleString()}
                    </dd>
                </div>
            </dl>
        </div>

        <!-- PROPS -->
        {#if artifact.properties}
            <div>
                <h4 class="text-sm font-medium text-gray-900">Properties</h4>
                <div class="mt-2 bg-gray-50 p-4 rounded-md">
                    <pre class="text-sm">{JSON.stringify(
                            artifact.properties,
                            null,
                            2,
                        )}</pre>
                </div>
            </div>
        {/if}

        <!-- METADATA -->
        {#if artifact.metadata}
            <div>
                <h4 class="text-sm font-medium text-gray-900">Metadata</h4>
                <div class="mt-2 bg-gray-50 p-4 rounded-md">
                    <pre class="text-sm">{formatJson(artifact.metadata)}</pre>
                </div>
            </div>
        {/if}

        <!-- ACTIONS -->
        <div class="flex justify-end gap-3">
            <button
                type="button"
                onclick={onclose}
                class="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
            >
                Cancel
            </button>
            <button
                type="button"
                onclick={handleSave}
                disabled={saving}
                class="inline-flex items-center px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:bg-blue-400"
            >
                <Save class="h-4 w-4 mr-2 {saving ? 'animate-spin' : ''}" />
                {saving ? "Saving..." : "Save Changes"}
            </button>
        </div>
    </div>
</DetailModal>
