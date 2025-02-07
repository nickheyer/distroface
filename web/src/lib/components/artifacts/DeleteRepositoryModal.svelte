<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { AlertCircle } from "lucide-svelte";
  import type { ArtifactRepository } from "$lib/types/artifacts.svelte";

  let { repository, onclose } = $props<{
    repository: ArtifactRepository;
    onclose: () => void;
  }>();

  let loading = $state(false);
  let error = $state<string | null>(null);

  async function handleDelete() {
    loading = true;
    try {
      await artifacts.deleteRepository(repository.name);
      onclose();
    } catch (err) {
      error =
        err instanceof Error ? err.message : "Failed to delete repository";
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
        <div class="sm:flex sm:items-start">
          <div
            class="mx-auto flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full bg-red-100 sm:mx-0 sm:h-10 sm:w-10"
          >
            <AlertCircle class="h-6 w-6 text-red-600" />
          </div>
          <div class="mt-3 text-center sm:ml-4 sm:mt-0 sm:text-left">
            <h3 class="text-base font-semibold leading-6 text-gray-900">
              Delete Repository
            </h3>
            <div class="mt-2">
              <p class="text-sm text-gray-500">
                Are you sure you want to delete <span class="font-semibold"
                  >{repository.name}</span
                >? This action cannot be undone and all artifacts will be
                permanently deleted.
              </p>
            </div>
          </div>
        </div>

        {#if error}
          <div class="mt-4 rounded-md bg-red-50 p-4 text-sm text-red-700">
            {error}
          </div>
        {/if}

        <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
          <button
            type="button"
            onclick={handleDelete}
            disabled={loading}
            class="inline-flex w-full justify-center rounded-md bg-red-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-red-500 sm:ml-3 sm:w-auto"
          >
            {loading ? "Deleting..." : "Delete Repository"}
          </button>
          <button
            type="button"
            onclick={() => onclose()}
            class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  </div>
</div>
