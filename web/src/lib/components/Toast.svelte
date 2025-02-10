<script lang="ts">
    import { toasts, removeToast } from "$lib/stores/toast.svelte";
</script>

<div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
    {#each $toasts as toast (toast.id)}
        <div
            class="flex items-center p-4 rounded-lg shadow-lg min-w-[300px]
               {toast.type === 'success'
                ? 'bg-green-100 text-green-800'
                : toast.type === 'error'
                  ? 'bg-red-100 text-red-800'
                  : 'bg-blue-100 text-blue-800'}"
        >
            <span class="flex-1">{toast.message}</span>
            <button
                type="button"
                onclick={() => removeToast(toast.id)}
                class="ml-4 text-current opacity-70 hover:opacity-100"
            >
                ×
            </button>
        </div>
    {/each}
</div>
