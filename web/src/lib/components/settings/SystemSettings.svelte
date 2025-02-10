<script lang="ts">
    import { onMount } from "svelte";
    import { AlertCircle } from "lucide-svelte";
    import { api } from "$lib/stores/auth.svelte";
    interface ServerConfig {
        port: string;
        domain: string;
        rsaKeyFile: string;
        tlsKeyFile: string;
        tlsCertFile: string;
        certBundle: string;
    }
    interface StorageConfig {
        rootDirectory: string;
    }
    interface DatabaseConfig {
        path: string;
    }
    interface AuthConfig {
        realm: string;
        service: string;
        issuer: string;
    }
    interface Config {
        server: ServerConfig;
        storage: StorageConfig;
        database: DatabaseConfig;
        auth: AuthConfig;
    }
    let settings: Config | null = null;
    let error: string | null = null;
    let loading = true;
    onMount(getConfig);
    async function getConfig() {
        try {
            const response = await api.get("/api/v1/settings/config");
            if (response.ok) {
                const res = await response.json();
                if (res) {
                    settings = res;
                }
            }
        } catch (err) {
            error = err instanceof Error ? err.message : "Failed to load settings";
        } finally {
            loading = false;
        }
    }
</script>

<div class="space-y-6">
    <!-- READONLY -->
    <div class="bg-yellow-50 border-l-4 border-yellow-400 p-4">
        <div class="flex">
            <AlertCircle class="h-5 w-5 text-yellow-400" />
            <div class="ml-3">
                <p class="text-sm text-yellow-700">
                    These settings are loaded from config.yml at startup.
                    To change them, update the file and restart the service.
                </p>
            </div>
        </div>
    </div>

    {#if error}
        <div class="rounded-md bg-red-50 p-4">
            <div class="flex">
                <AlertCircle class="h-5 w-5 text-red-400" />
                <div class="ml-3 text-sm text-red-700">{error}</div>
            </div>
        </div>
    {/if}

    {#if loading}
        <p>Loading settings...</p>
    {:else if settings}
        <div class="bg-white shadow-sm rounded-lg divide-y divide-gray-200">
            <!-- SERVER -->
            <div class="p-6">
                <h3 class="text-base font-medium text-gray-900">Server Configuration</h3>
                <div class="mt-4 grid grid-cols-2 gap-4">
                    <div>
                        <label for="server-domain" class="block text-sm font-medium text-gray-700">Domain</label>
                        <input
                            id="server-domain"
                            type="text"
                            value={settings.server.domain}
                            disabled
                            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 bg-gray-50 text-gray-500"
                        />
                    </div>
                    <div>
                        <label for="server-port" class="block text-sm font-medium text-gray-700">Port</label>
                        <input
                            id="server-port"
                            type="text"
                            value={settings.server.port}
                            disabled
                            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 bg-gray-50 text-gray-500"
                        />
                    </div>
                </div>
            </div>
            <!-- STORAGE -->
            <div class="p-6">
                <h3 class="text-base font-medium text-gray-900">Storage Configuration</h3>
                <div class="mt-4">
                    <label for="storage-root" class="block text-sm font-medium text-gray-700">Root Directory</label>
                    <input
                        id="storage-root"
                        type="text"
                        value={settings.storage.rootDirectory}
                        disabled
                        class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 bg-gray-50 text-gray-500"
                    />
                </div>
            </div>
            <!-- AUTH -->
            <div class="p-6">
                <h3 class="text-base font-medium text-gray-900">Authentication Configuration</h3>
                <div class="mt-4 space-y-4">
                    <div>
                        <label for="general-auth-realm" class="block text-sm font-medium text-gray-700">Realm</label>
                        <input
                            id="general-auth-realm"
                            type="text"
                            value={settings.auth.realm}
                            disabled
                            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 bg-gray-50 text-gray-500"
                        />
                    </div>
                    <div>
                        <label for="general-auth-service" class="block text-sm font-medium text-gray-700">Service</label>
                        <input
                            id="general-auth-service"
                            type="text"
                            value={settings.auth.service}
                            disabled
                            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 bg-gray-50 text-gray-500"
                        />
                    </div>
                    <div>
                        <label for="general-auth-issuer" class="block text-sm font-medium text-gray-700">Issuer</label>
                        <input
                            id="general-auth-issuer"
                            type="text"
                            value={settings.auth.issuer}
                            disabled
                            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 bg-gray-50 text-gray-500"
                        />
                    </div>
                </div>
            </div>
        </div>
    {:else}
        <p>Unable to load settings.</p>
    {/if}
</div>
