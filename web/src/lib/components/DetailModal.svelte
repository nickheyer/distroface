<script lang="ts">
    import { X } from "lucide-svelte";
    
    let { title, onclose, children } = $props<{
        title: string;
        children: any;
        onclose: () => void;
    }>();
</script>

<div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
    <div class="fixed inset-0 z-10 overflow-y-auto">
        <div class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0">
            <div class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-2xl sm:p-6">
                <div class="absolute right-0 top-0 pr-4 pt-4">
                    <button
                        type="button"
                        onclick={onclose}
                        class="rounded-md bg-white text-gray-400 hover:text-gray-500"
                    >
                        <X class="h-6 w-6" />
                    </button>
                </div>
                <div class="sm:flex sm:items-start">
                    <div class="mt-3 text-center sm:mt-0 sm:text-left w-full">
                        <h3 class="text-lg font-semibold leading-6 text-gray-900">
                            {title}
                        </h3>
                        <div class="mt-4">
                            {@render children()}
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>
