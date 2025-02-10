<script lang="ts">
    import { onMount } from "svelte";
    import { Save, AlertCircle } from "lucide-svelte";
    import { api } from "$lib/stores/auth.svelte";

    let settings = {
        tokenExpiry: 60,
        sessionTimeout: 1440,
        passwordPolicy: {
            minLength: 8,
            requireUpper: true,
            requireLower: true,
            requireNumber: true,
            requireSpecial: false,
        },
        allowAnonymous: false,
    };

    let isEditing = false;
    let error: string | null = null;

    onMount(async () => {
        try {
            const response = await api.get("/api/v1/settings/auth");
            if (response.ok) {
                settings = await response.json();
            }
        } catch (err) {
            console.error("Failed to load settings:", err);
        }
    });

    async function handleSave() {
        try {
            const response = await api.put("/api/v1/settings/auth", settings);
            if (!response.ok) {
                throw new Error("Failed to save settings");
            }
            isEditing = false;
            error = null;
        } catch (err) {
            error =
                err instanceof Error ? err.message : "Failed to save settings";
        }
    }
</script>

<div class="space-y-6">
    <div class="flex justify-between items-center">
        {#if isEditing}
            <div class="flex space-x-2">
                <button
                    on:click={() => (isEditing = false)}
                    class="px-3 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
                >
                    Cancel
                </button>
                <button
                    on:click={handleSave}
                    class="flex items-center px-3 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
                >
                    <Save class="w-4 h-4 mr-2" />
                    Save Changes
                </button>
            </div>
        {:else}
            <button
                on:click={() => (isEditing = true)}
                class="px-3 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
            >
                Edit Settings
            </button>
        {/if}
    </div>

    {#if error}
        <div class="rounded-md bg-red-50 p-4">
            <div class="flex">
                <AlertCircle class="h-5 w-5 text-red-400" />
                <div class="ml-3 text-sm text-red-700">{error}</div>
            </div>
        </div>
    {/if}

    <div class="bg-white shadow-sm rounded-lg divide-y divide-gray-200">
        <!-- Token Settings -->
        <div class="p-6">
            <h3 class="text-base font-medium text-gray-900">
                Token Configuration
            </h3>
            <div class="mt-4 grid grid-cols-2 gap-4">
                <div>
                    <label
                        for="token-expiry"
                        class="block text-sm font-medium text-gray-700"
                        >Token Expiry (minutes)</label
                    >
                    <input
                        id="token-expiry"
                        type="number"
                        bind:value={settings.tokenExpiry}
                        disabled={!isEditing}
                        class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
                    />
                </div>
                <div>
                    <label
                        for="session-timeout"
                        class="block text-sm font-medium text-gray-700"
                        >Session Timeout (minutes)</label
                    >
                    <input
                        id="session-timeout"
                        type="number"
                        bind:value={settings.sessionTimeout}
                        disabled={!isEditing}
                        class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
                    />
                </div>
            </div>
        </div>

        <!-- Password Policy -->
        <div class="p-6">
            <h3 class="text-base font-medium text-gray-900">Password Policy</h3>
            <div class="mt-4 space-y-4">
                <div>
                    <label
                        for="min-length"
                        class="block text-sm font-medium text-gray-700"
                        >Minimum Length</label
                    >
                    <input
                        id="min-length"
                        type="number"
                        bind:value={settings.passwordPolicy.minLength}
                        disabled={!isEditing}
                        class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
                    />
                </div>

                <div class="space-y-2">
                    <label class="flex items-center">
                        <input
                            type="checkbox"
                            bind:checked={settings.passwordPolicy.requireUpper}
                            disabled={!isEditing}
                            class="h-4 w-4 text-blue-600 rounded border-gray-300"
                        />
                        <span class="ml-2 text-sm text-gray-700"
                            >Require Uppercase</span
                        >
                    </label>

                    <label class="flex items-center">
                        <input
                            type="checkbox"
                            bind:checked={settings.passwordPolicy.requireLower}
                            disabled={!isEditing}
                            class="h-4 w-4 text-blue-600 rounded border-gray-300"
                        />
                        <span class="ml-2 text-sm text-gray-700"
                            >Require Lowercase</span
                        >
                    </label>

                    <label class="flex items-center">
                        <input
                            type="checkbox"
                            bind:checked={settings.passwordPolicy.requireNumber}
                            disabled={!isEditing}
                            class="h-4 w-4 text-blue-600 rounded border-gray-300"
                        />
                        <span class="ml-2 text-sm text-gray-700"
                            >Require Number</span
                        >
                    </label>

                    <label class="flex items-center">
                        <input
                            type="checkbox"
                            bind:checked={settings.passwordPolicy
                                .requireSpecial}
                            disabled={!isEditing}
                            class="h-4 w-4 text-blue-600 rounded border-gray-300"
                        />
                        <span class="ml-2 text-sm text-gray-700"
                            >Require Special Character</span
                        >
                    </label>
                </div>
            </div>
        </div>

        <!-- Anonymous Access -->
        <div class="p-6">
            <div class="flex items-center justify-between">
                <div>
                    <h3 class="text-base font-medium text-gray-900">
                        Anonymous Access
                    </h3>
                    <p class="text-sm text-gray-500">
                        Allow anonymous users to access public repositories
                    </p>
                </div>
                <label class="relative inline-flex items-center cursor-pointer">
                    <input
                        type="checkbox"
                        bind:checked={settings.allowAnonymous}
                        disabled={!isEditing}
                        class="sr-only peer"
                    />
                    <div
                        class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-blue-300 dark:peer-focus:ring-blue-800 rounded-full peer dark:bg-gray-700 peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-blue-600"
                    ></div>
                </label>
            </div>
        </div>
    </div>
</div>
