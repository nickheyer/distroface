<script lang="ts">
    import { auth } from "$lib/stores/auth.svelte";
    import { groups } from "$lib/stores/groups.svelte";
    import { Users, Shield, UserPlus, Trash2, Settings, AlertCircle } from "lucide-svelte";
    import type { User } from "$lib/stores/auth.svelte";
    import { showToast } from "$lib/stores/toast.svelte";
    import Toast from "$lib/components/Toast.svelte";

    let users = $state<User[]>([]);
    let loading = $state(true);
    let error = $state<string | null>(null);
    let searchTerm = $state("");
    let showCreateModal = $state(false);

    // FORM STATES
    let newUsername = $state("");
    let newPassword = $state("");
    let selectedGroups = $state<string[]>([]);

    // DELETE STATE
    let deleteModalOpen = $state(false);
    let userToDelete = $state<string | null>(null);
    let deleteError = $state<string | null>(null);

    async function fetchUsers() {
        try {
            const response = await fetch("/api/v1/users", {
                headers: {
                    Authorization: `Bearer ${auth.token}`,
                },
            });

            if (!response.ok) {
                throw new Error("Failed to fetch users");
            }

            users = await response.json();
        } catch (e) {
            error = "Failed to load users";
            console.error(e);
        } finally {
            loading = false;
        }
    }

    async function createUser() {
        try {
            const response = await fetch("/api/v1/users", {
                method: "POST",
                headers: {
                    Authorization: `Bearer ${auth.token}`,
                    "Content-Type": "application/json",
                },
                body: JSON.stringify({
                    username: newUsername,
                    password: newPassword,
                    groups: selectedGroups,
                }),
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            showCreateModal = false;
            newUsername = "";
            newPassword = "";
            selectedGroups = [];
            await fetchUsers();
        } catch (e) {
            error = e instanceof Error ? e.message : "Failed to create user";
            console.error(e);
        }
    }

    async function updateUserGroups(username: string, groups: string[]) {
        try {
            const response = await fetch("/api/v1/users/groups", {
                method: "PUT",
                headers: {
                    Authorization: `Bearer ${auth.token}`,
                    "Content-Type": "application/json",
                },
                body: JSON.stringify({
                    username,
                    groups,
                }),
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            await fetchUsers();
        } catch (e) {
            error = e instanceof Error ? e.message : "Failed to update user groups";
            console.error(e);
        }
    }

    async function handleDeleteUser() {
        if (!userToDelete) return;
        
        try {
            const response = await fetch(`/api/v1/users/${userToDelete}`, {
                method: 'DELETE',
                headers: {
                    'Authorization': `Bearer ${auth.token}`
                }
            });

            if (!response.ok) {
                const errorText = await response.text();
                throw new Error(errorText);
            }

            // CLOSE MODAL AND REFRESH USERS
            deleteModalOpen = false;
            userToDelete = null;
            await fetchUsers();
            
        } catch (err) {
            deleteError = err instanceof Error ? err.message : 'Failed to delete user';
        }
    }

    $effect(() => {
        if (auth.token) {
            Promise.all([
                fetchUsers(),
                groups.fetchGroups()
            ]).catch(console.error);
        }
    });

    // FILTER USERS
    const filteredUsers = $derived(
        users.filter((user) =>
            user.username.toLowerCase().includes(searchTerm.toLowerCase()),
        ),
    );

    // COMPUTE METRICS
    const metrics = $derived({
        totalUsers: users.length,
        adminCount: users.filter((u) => u.groups.includes("admins")).length,
        developerCount: users.filter((u) => u.groups.includes("developers")).length,
        readerCount: users.filter((u) => u.groups.includes("readers")).length,
    });

    $effect(() => {
        if (error) {
            showToast(error, 'error');
            error = null;
        }
    })

    // EVERYTHING IS AUTHENTICATED ON SERVER, THIS KEEPS UI STABLE - EVENTUALLY WILL MAKE MORE GRANULAR
    const canCreateUser = $derived(auth.hasRole('admins'));
    const canUpdateUser = $derived(auth.hasRole('admins'));
    const canViewUsers = $derived(auth.hasRole('admins'));
</script>

<!-- CREATE USER MODAL -->
{#if showCreateModal}
    <div class="fixed inset-0 bg-gray-500 bg-opacity-75 flex items-center justify-center p-4 z-50">
        <div class="bg-white rounded-lg shadow-xl p-6 w-full max-w-md">
            <h3 class="text-lg font-medium mb-4">Create New User</h3>
            
            <form onsubmit={createUser} class="space-y-4">
                <!-- USERNAME FIELD -->
                <div>
                    <label for="username" class="block text-sm font-medium text-gray-700">
                        Username
                    </label>
                    <input
                        id="username"
                        type="text"
                        bind:value={newUsername}
                        required
                        class="mt-1 block w-full px-3 py-2 rounded-md border border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 text-sm"
                    />
                </div>

                <!-- PASSWORD FIELD -->
                <div>
                    <label for="password" class="block text-sm font-medium text-gray-700">
                        Password
                    </label>
                    <input
                        id="password"
                        type="password"
                        bind:value={newPassword}
                        required
                        class="mt-1 block w-full px-3 py-2 rounded-md border border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 text-sm"
                    />
                </div>

                <!-- GROUPS SELECT -->
                <div>
                    <label for="group-form-block" class="block text-sm font-medium text-gray-700 mb-2">
                        Groups
                    </label>
                    <div id="group-form-block" class="max-h-48 overflow-y-auto border border-gray-300 rounded-md divide-y divide-gray-200">
                        {#each groups.all as group}
                            <label class="flex items-center p-3 hover:bg-gray-50">
                                <input
                                    type="checkbox"
                                    value={group.name}
                                    checked={selectedGroups.includes(group.name)}
                                    onchange={(e) => {
                                        if (e.currentTarget.checked) {
                                            selectedGroups = [...selectedGroups, group.name];
                                        } else {
                                            selectedGroups = selectedGroups.filter(g => g !== group.name);
                                        }
                                    }}
                                    class="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                                />
                                <div class="ml-3">
                                    <span class="font-medium text-gray-900">{group.name}</span>
                                    <p class="text-gray-500 text-sm">{group.description}</p>
                                </div>
                            </label>
                        {/each}
                    </div>
                </div>

                <!-- ERR TO DISPLAY -->
                <Toast></Toast>

                <!-- ACTION BUTTONS -->
                <div class="flex justify-end space-x-2 pt-4">
                    <button
                        type="button"
                        onclick={() => showCreateModal = false}
                        class="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
                    >
                        Cancel
                    </button>
                    <button
                        type="submit"
                        class="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
                    >
                        Create User
                    </button>
                </div>
            </form>
        </div>
    </div>
{/if}

<div class="space-y-6">
    <!-- HEADER AND ACTIONS -->
    <div class="sm:flex sm:items-center sm:justify-between">
        <div>
            <h1 class="text-2xl font-semibold text-gray-900">
                User Management
            </h1>
            <p class="mt-2 text-sm text-gray-700">
                Manage users and their access levels
            </p>
        </div>
        {#if canCreateUser}
            <button
                onclick={() => (showCreateModal = true)}
                class="mt-4 sm:mt-0 inline-flex items-center justify-center rounded-md bg-registry-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-registry-500 focus-visible:outline focus-visible:outline-offset-2 focus-visible:outline-registry-600"
            >
                <UserPlus class="h-4 w-4 mr-2" />
                Add User
            </button>
        {/if}
    </div>

    <!-- METRICS CARDS -->
    <div class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-4">
        <div class="bg-white overflow-hidden shadow rounded-lg">
            <div class="p-5">
                <div class="flex items-center">
                    <div class="flex-shrink-0">
                        <Users class="h-6 w-6 text-gray-400" />
                    </div>
                    <div class="ml-5 w-0 flex-1">
                        <dl>
                            <dt
                                class="text-sm font-medium text-gray-500 truncate"
                            >
                                Total Users
                            </dt>
                            <dd class="text-lg font-semibold text-gray-900">
                                {metrics.totalUsers}
                            </dd>
                        </dl>
                    </div>
                </div>
            </div>
        </div>
        <div class="bg-white overflow-hidden shadow rounded-lg">
            <div class="p-5">
                <div class="flex items-center">
                    <div class="flex-shrink-0">
                        <Shield class="h-6 w-6 text-gray-400" />
                    </div>
                    <div class="ml-5 w-0 flex-1">
                        <dl>
                            <dt
                                class="text-sm font-medium text-gray-500 truncate"
                            >
                                Admins
                            </dt>
                            <dd class="text-lg font-semibold text-gray-900">
                                {metrics.adminCount}
                            </dd>
                        </dl>
                    </div>
                </div>
            </div>
        </div>
        <div class="bg-white overflow-hidden shadow rounded-lg">
            <div class="p-5">
                <div class="flex items-center">
                    <div class="flex-shrink-0">
                        <Settings class="h-6 w-6 text-gray-400" />
                    </div>
                    <div class="ml-5 w-0 flex-1">
                        <dl>
                            <dt
                                class="text-sm font-medium text-gray-500 truncate"
                            >
                                Developers
                            </dt>
                            <dd class="text-lg font-semibold text-gray-900">
                                {metrics.developerCount}
                            </dd>
                        </dl>
                    </div>
                </div>
            </div>
        </div>
        <div class="bg-white overflow-hidden shadow rounded-lg">
            <div class="p-5">
                <div class="flex items-center">
                    <div class="flex-shrink-0">
                        <Users class="h-6 w-6 text-gray-400" />
                    </div>
                    <div class="ml-5 w-0 flex-1">
                        <dl>
                            <dt
                                class="text-sm font-medium text-gray-500 truncate"
                            >
                                Readers
                            </dt>
                            <dd class="text-lg font-semibold text-gray-900">
                                {metrics.readerCount}
                            </dd>
                        </dl>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <!-- ERROR MESSAGE -->
    <Toast></Toast>

    <!-- USER LIST -->
    {#if canViewUsers}
        {#if loading}
            <div class="flex justify-center py-12">
                <div
                    class="animate-spin rounded-full h-8 w-8 border-b-2 border-registry-600"
                ></div>
            </div>
        {:else}
            <div class="bg-white shadow-sm rounded-lg">
                <div class="px-4 py-5 sm:p-6">
                    <div class="sm:flex sm:items-center">
                        <div class="sm:flex-auto">
                            <div class="mt-4 relative">
                                <input
                                    type="text"
                                    placeholder="Search users..."
                                    bind:value={searchTerm}
                                    class="block w-full px-3 py-2 rounded-md border border-gray-300 shadow-sm focus:border-registry-500 focus:ring-registry-500 text-sm"
                                />
                            </div>
                        </div>
                    </div>
                    <div class="mt-8 flow-root">
                        <div class="space-y-4">
                            <!-- USER TABLE -->
                            <div class="bg-white shadow rounded-lg">
                                <table class="min-w-full divide-y divide-gray-200">
                                    <thead class="bg-gray-50">
                                        <tr>
                                            <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                                Username
                                            </th>
                                            <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                                Groups
                                            </th>
                                            <th class="px-6 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider">
                                                Actions
                                            </th>
                                        </tr>
                                    </thead>
                                    <tbody class="bg-white divide-y divide-gray-200">
                                        {#each filteredUsers as user}
                                            <tr>
                                                <td class="px-6 py-4 whitespace-nowrap">
                                                    <div class="text-sm font-medium text-gray-900">
                                                        {user.username}
                                                    </div>
                                                </td>
                                                <td class="px-6 py-4">
                                                    <div class="flex flex-wrap gap-1">
                                                        {#each user.groups as group}
                                                            <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800">
                                                                {group}
                                                            </span>
                                                        {/each}
                                                    </div>
                                                </td>
                                                <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                                                    <div class="flex justify-end gap-2">
                                                        {#each groups.all as group}
                                                            <button
                                                                onclick={() => {
                                                                    const newGroups = user.groups.includes(group.name)
                                                                        ? user.groups.filter(g => g !== group.name)
                                                                        : [...user.groups, group.name];
                                                                    updateUserGroups(user.username, newGroups);
                                                                }}
                                                                class={`px-2 py-1 rounded-md text-xs font-medium 
                                                                    ${user.groups.includes(group.name)
                                                                        ? 'bg-blue-100 text-blue-700'
                                                                        : 'bg-gray-100 text-gray-600'} 
                                                                    hover:bg-blue-200`}
                                                            >
                                                                {group.name}
                                                            </button>
                                                        {/each}
                                                        <button
                                                            onclick={() => {
                                                                userToDelete = user.username;
                                                                deleteModalOpen = true;
                                                            }}
                                                            class="text-red-600 hover:text-red-900"
                                                        >
                                                            <Trash2 class="h-4 w-4" />
                                                        </button>
                                                    </div>

                                                </td>
                                            </tr>
                                        {/each}
                                    </tbody>
                                </table>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        {/if}
    {:else}
        <div class="text-center py-12">
            <p class="text-gray-500">You don't have permission to view users.</p>
        </div>
    {/if}
</div>

<!-- way too much tailwind -->
{#if deleteModalOpen && userToDelete}
    <div class="fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity z-50">
        <div class="fixed inset-0 z-10 overflow-y-auto">
            <div class="flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0">
                <div class="relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6">
                    <div class="sm:flex sm:items-start">
                        <div class="mx-auto flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full bg-red-100 sm:mx-0 sm:h-10 sm:w-10">
                            <AlertCircle class="h-6 w-6 text-red-600" />
                        </div>
                        <div class="mt-3 text-center sm:ml-4 sm:mt-0 sm:text-left">
                            <h3 class="text-base font-semibold leading-6 text-gray-900">
                                Delete User
                            </h3>
                            <div class="mt-2">
                                <p class="text-sm text-gray-500">
                                    Are you sure you want to delete user {userToDelete}? This action cannot be undone.
                                </p>
                            </div>
                        </div>
                    </div>

                    {#if deleteError}
                        <div class="mt-4 rounded-md bg-red-50 p-4">
                            <div class="flex">
                                <AlertCircle class="h-5 w-5 text-red-400" />
                                <div class="ml-3">
                                    <h3 class="text-sm font-medium text-red-800">Error</h3>
                                    <div class="mt-2 text-sm text-red-700">
                                        <p>{deleteError}</p>
                                    </div>
                                </div>
                            </div>
                        </div>
                    {/if}

                    <div class="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
                        <button
                            type="button"
                            onclick={handleDeleteUser}
                            class="inline-flex w-full justify-center rounded-md bg-red-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-red-500 sm:ml-3 sm:w-auto"
                        >
                            Delete User
                        </button>
                        <button
                            type="button"
                            onclick={() => {
                                deleteModalOpen = false;
                                userToDelete = null;
                                deleteError = null;
                            }}
                            class="mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                        >
                            Cancel
                        </button>
                    </div>
                </div>
            </div>
        </div>
    </div>
{/if}