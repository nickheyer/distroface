<script lang="ts">
    import { auth } from "$lib/stores/auth.svelte";
    import { groups } from "$lib/stores/groups.svelte";
    import type { Group } from "$lib/stores/groups.svelte";
    import { AlertCircle, Loader2 } from "lucide-svelte";

    interface Role {
        name: string;
        description: string;
        permissions: Array<{
            action: string;
            resource: string;
        }>;
    }

    // GROUP TYPE FOR EDITS
    interface EditingGroup extends Group {
        roles: string[];
    }

    // STATE
    let activeTab = $state<"roles" | "groups">("roles");
    let loading = $state(false);
    let error = $state<string | null>(null);
    let success = $state<string | null>(null);

    // ROLE MANAGEMENT
    let roles = $state<Role[]>([]);
    let selectedRole = $state<Role | null>(null);

    // GROUP MANAGEMENT
    let editingGroup = $state<EditingGroup | null>(null);

    async function fetchRoles() {
        try {
            const response = await fetch("/api/v1/roles", {
                headers: {
                    Authorization: `Bearer ${auth.token}`,
                },
            });

            if (!response.ok) throw new Error("Failed to fetch roles");
            roles = await response.json();
        } catch (err) {
            error = err instanceof Error ? err.message : "Failed to load roles";
        }
    }

    async function updateGroup(group: EditingGroup) {
        try {
            loading = true;
            const response = await fetch(`/api/v1/groups/${group.name}`, {
                method: "PUT",
                headers: {
                    Authorization: `Bearer ${auth.token}`,
                    "Content-Type": "application/json",
                },
                body: JSON.stringify(group),
            });

            if (!response.ok) {
                throw new Error("Failed to update group");
            }

            await groups.fetchGroups();
            success = "Group updated successfully";
            editingGroup = null;
        } catch (err) {
            error =
                err instanceof Error ? err.message : "Failed to update group";
        } finally {
            loading = false;
        }
    }

    function startEditing(group: Group) {
        // DEEP COPY
        editingGroup = {
            ...group,
            roles: [...group.roles],
        };
    }

    function updateEditingGroupRoles(roleName: string, checked: boolean) {
        if (!editingGroup) return;

        editingGroup = {
            ...editingGroup,
            roles: checked
                ? [...editingGroup.roles, roleName]
                : editingGroup.roles.filter((r) => r !== roleName),
        };
    }

    $effect(() => {
        if (auth.isAuthenticated && auth.hasRole("admins")) {
            Promise.all([
                fetchRoles(),
                groups.fetchGroups(),
            ]).catch((err) => {
                error =
                    err instanceof Error ? err.message : "Failed to load data";
            });
        }
    });
</script>

<div class="space-y-6">
    <!-- HEADER -->
    <div class="sm:flex sm:items-center sm:justify-between">
        <div>
            <h1 class="text-2xl font-semibold text-gray-900">Settings</h1>
            <p class="mt-2 text-sm text-gray-700">
                Manage system configuration, roles, and permissions
            </p>
        </div>
    </div>

    {#if !auth.hasRole("admins")}
        <div class="rounded-lg bg-red-50 p-4">
            <div class="flex">
                <AlertCircle class="h-5 w-5 text-red-400" />
                <div class="ml-3">
                    <h3 class="text-sm font-medium text-red-800">
                        Access Denied
                    </h3>
                    <p class="mt-1 text-sm text-red-700">
                        You need administrator privileges to access settings.
                    </p>
                </div>
            </div>
        </div>
    {:else}
        <!-- SUCCESS MESSAGE -->
        {#if success}
            <div class="rounded-lg bg-green-50 p-4">
                <div class="flex">
                    <div class="ml-3">
                        <p class="text-sm font-medium text-green-800">
                            {success}
                        </p>
                    </div>
                </div>
            </div>
        {/if}

        <!-- ERROR MESSAGE -->
        {#if error}
            <div class="rounded-lg bg-red-50 p-4">
                <div class="flex">
                    <AlertCircle class="h-5 w-5 text-red-400" />
                    <div class="ml-3">
                        <p class="text-sm font-medium text-red-800">{error}</p>
                    </div>
                </div>
            </div>
        {/if}

        <!-- TABS -->
        <div class="border-b border-gray-200">
            <nav class="-mb-px flex space-x-8" aria-label="Tabs">
                <button
                    class={`${
                        activeTab === "roles"
                            ? "border-registry-500 text-registry-600"
                            : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700"
                    } whitespace-nowrap border-b-2 py-4 px-1 text-sm font-medium`}
                    onclick={() => (activeTab = "roles")}
                >
                    Roles
                </button>
                <button
                    class={`${
                        activeTab === "groups"
                            ? "border-registry-500 text-registry-600"
                            : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700"
                    } whitespace-nowrap border-b-2 py-4 px-1 text-sm font-medium`}
                    onclick={() => (activeTab = "groups")}
                >
                    Groups
                </button>
            </nav>
        </div>

        <!-- CONTENT -->
        <div class="mt-6">
            {#if activeTab === "roles"}
                <div
                    class="bg-white shadow-sm rounded-lg divide-y divide-gray-200"
                >
                    {#each roles as role}
                        <div class="p-6">
                            <div class="flex items-center justify-between">
                                <div>
                                    <h3
                                        class="text-lg font-medium text-gray-900"
                                    >
                                        {role.name}
                                    </h3>
                                    <p class="mt-1 text-sm text-gray-500">
                                        {role.description}
                                    </p>
                                </div>
                            </div>
                            <div class="mt-4">
                                <h4 class="text-sm font-medium text-gray-900">
                                    Permissions
                                </h4>
                                <div class="mt-2 flex flex-wrap gap-2">
                                    {#each role.permissions as permission}
                                        <span
                                            class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800"
                                        >
                                            {permission.action}
                                            {permission.resource}
                                        </span>
                                    {/each}
                                </div>
                            </div>
                        </div>
                    {/each}
                </div>
            {:else if activeTab === "groups"}
                {#if groups.loading}
                    <div class="flex justify-center py-12">
                        <Loader2 class="h-8 w-8 animate-spin text-registry-600" />
                    </div>
                {:else if groups.error}
                    <div class="rounded-lg bg-red-50 p-4">
                        <div class="flex">
                            <AlertCircle class="h-5 w-5 text-red-400" />
                            <div class="ml-3">
                                <p class="text-sm font-medium text-red-800">{groups.error}</p>
                            </div>
                        </div>
                    </div>
                {:else}
                    <div class="bg-white shadow-sm rounded-lg divide-y divide-gray-200">
                        {#each groups.all as group}
                            <div class="p-6">
                                {#if editingGroup?.name === group.name}
                                    <form
                                        class="space-y-4"
                                        onsubmit={(e) => {
                                            e.preventDefault();
                                            if (editingGroup)
                                                updateGroup(editingGroup);
                                        }}
                                    >
                                        <div>
                                            <label
                                                for="group-description"
                                                class="block text-sm font-medium text-gray-700"
                                            >
                                                Description
                                            </label>
                                            <input
                                                id="group-description"
                                                type="text"
                                                class="mt-1 block w-full px-3 py-2 rounded-md border border-gray-300 shadow-sm focus:border-registry-500 focus:ring-registry-500 text-sm"
                                                bind:value={editingGroup.description}
                                            />
                                        </div>
                                        <div>
                                            <label
                                                for="roles-form-block"
                                                class="block text-sm font-medium text-gray-700"
                                            >
                                                Roles
                                            </label>
                                            <div
                                                id="roles-form-block"
                                                class="mt-2 border border-gray-300 rounded-md divide-y divide-gray-200"
                                            >
                                                {#each roles as role}
                                                    <label
                                                        class="flex items-center p-3 hover:bg-gray-50"
                                                    >
                                                        <input
                                                            type="checkbox"
                                                            class="h-4 w-4 rounded border-gray-300 text-registry-600 focus:ring-registry-500"
                                                            checked={editingGroup.roles.includes(
                                                                role.name,
                                                            )}
                                                            onchange={(e) => {
                                                                updateEditingGroupRoles(
                                                                    role.name,
                                                                    e.currentTarget
                                                                        .checked,
                                                                );
                                                            }}
                                                        />
                                                        <div class="ml-3">
                                                            <span
                                                                class="text-sm font-medium text-gray-900"
                                                                >{role.name}</span
                                                            >
                                                            <p
                                                                class="text-sm text-gray-500"
                                                            >
                                                                {role.description}
                                                            </p>
                                                        </div>
                                                    </label>
                                                {/each}
                                            </div>
                                        </div>
                                        <div class="flex justify-end space-x-3">
                                            <button
                                                type="button"
                                                class="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
                                                onclick={() =>
                                                    (editingGroup = null)}
                                            >
                                                Cancel
                                            </button>
                                            <button
                                                type="submit"
                                                class="px-4 py-2 text-sm font-medium text-white bg-registry-600 rounded-md hover:bg-registry-700"
                                                disabled={loading}
                                            >
                                                {#if loading}
                                                    Saving...
                                                {:else}
                                                    Save Changes
                                                {/if}
                                            </button>
                                        </div>
                                    </form>
                                {:else}
                                    <div class="flex items-center justify-between">
                                        <div>
                                            <h3
                                                class="text-lg font-medium text-gray-900"
                                            >
                                                {group.name}
                                            </h3>
                                            <p class="mt-1 text-sm text-gray-500">
                                                {group.description}
                                            </p>
                                        </div>
                                        <button
                                            onclick={() => startEditing(group)}
                                            class="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
                                        >
                                            Edit
                                        </button>
                                    </div>
                                    <div class="mt-4">
                                        <h4
                                            class="text-sm font-medium text-gray-900"
                                        >
                                            Roles
                                        </h4>
                                        <div class="mt-2 flex flex-wrap gap-2">
                                            {#each group.roles as role}
                                                <span
                                                    class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800"
                                                >
                                                    {role}
                                                </span>
                                            {/each}
                                        </div>
                                    </div>
                                {/if}
                            </div>
                        {/each}
                    </div>
                {/if}
            {/if}
        </div>
    {/if}
</div>
