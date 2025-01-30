<script lang="ts">
    import { auth, login } from "$lib/stores/auth.svelte";
    import { goto } from "$app/navigation";
    import { Loader2 } from 'lucide-svelte';

    let username = $state("");
    let password = $state("");
    let loading = $state(false);

    async function handleSubmit(event: Event) {
        event.preventDefault();
        loading = true;
        
        try {
            await login(username, password);
            if (auth.isAuthenticated) {
                await goto("/registry");
            }
        } catch (e) {
            // HANDLED
        } finally {
            loading = false;
        }
    }
</script>

<div class="min-h-screen bg-gradient-to-br from-gray-50 to-gray-100 flex items-center justify-center py-12 px-4 sm:px-6 lg:px-8">
    <div class="bg-white shadow-xl rounded-2xl max-w-md w-full space-y-8 p-8 sm:p-10">
        <!-- HEADER -->
        <div class="text-center">
            <h1 class="text-3xl font-bold text-gray-900">DistroFace</h1>
            <p class="mt-2 text-sm text-gray-600">
                Container Registry Management
            </p>
        </div>

        <form class="mt-8 space-y-6" onsubmit={handleSubmit}>
            {#if auth.error}
                <div class="rounded-lg bg-red-50 p-4 text-sm text-red-700 flex items-center space-x-2" role="alert">
                    <svg class="h-5 w-5 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"/>
                    </svg>
                    <span>{auth.error}</span>
                </div>
            {/if}

            <div class="space-y-6">
                <div>
                    <label for="username" class="block text-sm font-medium text-gray-700">
                        Username
                    </label>
                    <div class="mt-1">
                        <input
                            id="username"
                            name="username"
                            type="text"
                            required
                            class="appearance-none block w-full px-3 py-2 border border-gray-300 rounded-lg shadow-sm 
                                   placeholder-gray-400 focus:outline-none focus:ring-blue-500 focus:border-blue-500 sm:text-sm
                                   disabled:bg-gray-50 disabled:text-gray-500 disabled:border-gray-200"
                            bind:value={username}
                            disabled={loading}
                        />
                    </div>
                </div>

                <div>
                    <label for="password" class="block text-sm font-medium text-gray-700">
                        Password
                    </label>
                    <div class="mt-1">
                        <input
                            id="password"
                            name="password"
                            type="password"
                            required
                            class="appearance-none block w-full px-3 py-2 border border-gray-300 rounded-lg shadow-sm 
                                   placeholder-gray-400 focus:outline-none focus:ring-blue-500 focus:border-blue-500 sm:text-sm
                                   disabled:bg-gray-50 disabled:text-gray-500 disabled:border-gray-200"
                            bind:value={password}
                            disabled={loading}
                        />
                    </div>
                </div>
            </div>

            <button
                type="submit"
                disabled={loading}
                class="group relative w-full flex justify-center py-2.5 px-4 border border-transparent rounded-lg
                       text-sm font-medium text-white bg-blue-600 hover:bg-blue-700 focus:outline-none 
                       focus:ring-2 focus:ring-offset-2 focus:ring-blue-500 disabled:bg-blue-400
                       disabled:cursor-not-allowed transition-colors duration-150 ease-in-out"
            >
                {#if loading}
                    <Loader2 class="animate-spin -ml-1 mr-2 h-4 w-4" />
                    Signing in...
                {:else}
                    Sign in
                {/if}
            </button>

            <div class="text-center text-xs text-gray-500">
                <p>Protected by DistroFace Authentication</p>
            </div>
        </form>
    </div>
</div>
