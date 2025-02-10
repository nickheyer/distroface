<script lang="ts">
    import "../app.css";
    import {
        Menu,
        X,
        LogOut,
        Package,
        Users,
        Settings,
        ChevronDown,
        ArrowRight,
        Globe,
        User,
        Archive
    } from "lucide-svelte";
    import { auth } from "$lib/stores/auth.svelte";
    import { goto } from "$app/navigation";
    import { clickOutside } from "$lib/actions/clickOutside";

    let { children } = $props();
    
    // STATE
    let isSidebarOpen = $state(false);
    let isUserMenuOpen = $state(false);

    type NavItem = {
        name: string;
        href: string;
        Icon: typeof Package;
    };

    const navigation: NavItem[] = [
        { name: "Registry", href: "/registry", Icon: Package },
        { name: "Public", href: "/public", Icon: Globe },
        { name: "Artifacts", href: "/artifacts", Icon: Archive},
        { name: "Migration", href: "/migration", Icon: ArrowRight },
        { name: "Users", href: "/users", Icon: Users },
        { name: "Settings", href: "/settings", Icon: Settings },
    ];

    $effect(() => {
        if (!auth.isAuthenticated) {
            goto('/login');
        }
    });

    async function handleLogout() {
        auth.logout();
        await goto('/login');
    }
</script>

{#if auth.isAuthenticated}
    <div class="min-h-screen bg-gray-50">
        <!-- TOP NAVIGATION -->
        <nav class="bg-white shadow-sm">
            <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
                <div class="flex h-16 justify-between">
                    <div class="flex">
                        <!-- LOGO -->
                        <div class="flex flex-shrink-0 items-center">
                            <span class="text-xl font-bold text-gray-900">
                                DistroFace
                            </span>
                        </div>

                        <!-- DESKTOP NAVIGATION -->
                        <div class="hidden sm:ml-6 sm:flex sm:space-x-8">
                            {#each navigation as { name, href, Icon }}
                                <a
                                    {href}
                                    class="inline-flex items-center border-b-2 border-transparent px-1 pt-1 text-sm font-medium text-gray-500 hover:border-gray-300 hover:text-gray-700"
                                >
                                    <Icon class="h-4 w-4 mr-2" />
                                    {name}
                                </a>
                            {/each}
                        </div>
                    </div>

                    <!-- USER MENU -->
                    <div class="hidden sm:ml-6 sm:flex sm:items-center">
                        <div class="relative ml-3">
                            <div>
                                <button
                                    type="button"
                                    class="flex items-center rounded-full bg-white text-sm focus:outline-none focus:ring-2 focus:ring-registry-500 focus:ring-offset-2"
                                    onclick={() =>
                                        (isUserMenuOpen = !isUserMenuOpen)}
                                >
                                    <span class="sr-only">Open user menu</span>
                                    <div
                                        class="h-8 w-8 rounded-full bg-gray-200 flex items-center justify-center"
                                    >
                                        <span
                                            class="text-sm font-medium text-gray-600"
                                        >
                                            {auth.user?.username?.[0]?.toUpperCase() ??
                                                "U"}
                                        </span>
                                    </div>
                                    <ChevronDown
                                        class="ml-1 h-4 w-4 text-gray-400"
                                    />
                                </button>
                            </div>

                            {#if isUserMenuOpen}
                                <div
                                    class="absolute right-0 z-10 mt-2 w-48 origin-top-right rounded-md bg-white py-1 shadow-lg ring-1 ring-black ring-opacity-5 focus:outline-none"
                                    use:clickOutside={() =>
                                        (isUserMenuOpen = false)}
                                >
                                    <div
                                        class="px-4 py-2 text-sm text-gray-500"
                                    >
                                        <a
                                            href="/"
                                            class="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-100"
                                        >
                                            <User class="h-4 w-4 mr-2 inline" />
                                            Profile
                                        </a>
                                        <button
                                            onclick={handleLogout}
                                            class="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100"
                                        >
                                            <LogOut class="h-4 w-4 mr-2" />
                                            Sign out
                                        </button>
                                    </div>
                                </div>
                            {/if}
                        </div>
                    </div>

                    <!-- MOBILE MENU BUTTON -->
                    <div class="flex items-center sm:hidden">
                        <button
                            type="button"
                            class="inline-flex items-center justify-center rounded-md p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-500"
                            onclick={() => (isSidebarOpen = !isSidebarOpen)}
                        >
                            {#if isSidebarOpen}
                                <X class="h-6 w-6" />
                            {:else}
                                <Menu class="h-6 w-6" />
                            {/if}
                        </button>
                    </div>
                </div>
            </div>

            <!-- MOBILE NAVIGATION -->
            {#if isSidebarOpen}
                <div class="sm:hidden">
                    <div class="space-y-1 pb-3 pt-2">
                        {#each navigation as { name, href, Icon }}
                            <a
                                {href}
                                class="flex items-center border-l-4 border-transparent py-2 pl-3 pr-4 text-base font-medium text-gray-500 hover:border-gray-300 hover:bg-gray-50 hover:text-gray-700"
                            >
                                <Icon class="h-4 w-4 mr-2" />
                                {name}
                            </a>
                        {/each}
                        <button
                            onclick={handleLogout}
                            class="flex w-full items-center border-l-4 border-transparent py-2 pl-3 pr-4 text-base font-medium text-gray-500 hover:border-gray-300 hover:bg-gray-50 hover:text-gray-700"
                        >
                            <LogOut class="h-4 w-4 mr-2" />
                            Sign out
                        </button>
                    </div>
                </div>
            {/if}
        </nav>

        <!-- MAIN CONTENT -->
        <main class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8 py-6">
            {@render children()}
        </main>
    </div>
{:else}
    {@render children()}
{/if}