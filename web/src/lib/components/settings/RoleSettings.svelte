<script lang="ts">
  import { Save, Plus, AlertCircle } from "lucide-svelte";
  import { auth, api } from "$lib/stores/auth.svelte";
  import type { Role } from "$lib/stores/auth.svelte";

  let roles = $state<Role[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let editingRole = $state<Role | null>(null);

  // CURRENTLY AVAILABLE, SHOULD DYNAMICALLY QUERY THESE THOUGH
  const availableActions = ['VIEW', 'CREATE', 'UPDATE', 'DELETE', 'PUSH', 'PULL', 'ADMIN', 'LOGIN', 'MIGRATE', 'UPLOAD', 'DOWNLOAD'];
  const availableResources = ['WEBUI', 'IMAGE', 'TAG', 'USER', 'GROUP', 'SYSTEM', 'TASK', 'ARTIFACT', 'REPO'];

  async function fetchRoles() {
    try {
      loading = true;
      const response = await api.get("/api/v1/roles");
      roles = await response.json();
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to fetch roles";
    } finally {
      loading = false;
    }
  }

  async function updateRole(role: Role|null) {
    if (!role) return;
    try {
      loading = true;
      await api.put(`/api/v1/roles/${role.name}`, {
        description: role.description,
        permissions: role.permissions
      });
      await fetchRoles();
      editingRole = null;
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to update role";
    } finally {
      loading = false;
    }
  }

  function togglePermission(role: Role, action: string, resource: string) {
    if (!role) return;

    const exists = role.permissions.some(p => p.action === action && p.resource === resource);
    if (exists) {
      role.permissions = role.permissions.filter(p => !(p.action === action && p.resource === resource));
    } else {
      role.permissions = [...role.permissions, { action, resource }];
    }
  }

  $effect(() => {
    if (auth.isAuthenticated) {
      fetchRoles();
    }
  });
</script>

<div class="space-y-6">
  {#if error}
    <div class="rounded-md bg-red-50 p-4">
      <div class="flex">
        <AlertCircle class="h-5 w-5 text-red-400" />
        <div class="ml-3">
          <p class="text-sm font-medium text-red-800">{error}</p>
        </div>
      </div>
    </div>
  {/if}

  {#if loading}
    <div class="flex justify-center py-12">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
    </div>
  {:else}
    {#each roles as role}
      <div class="bg-white shadow-sm rounded-lg p-6">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-lg font-medium text-gray-900">{role.name}</h3>
            {#if editingRole?.name === role.name}
              <input
                type="text"
                bind:value={editingRole.description}
                class="mt-1 block w-full px-3 py-2 rounded-md border border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 text-sm"
              />
            {:else}
              <p class="mt-1 text-sm text-gray-500">{role.description}</p>
            {/if}
          </div>
          <div>
            {#if editingRole?.name === role.name}
              <div class="flex space-x-3">
                <button
                  onclick={() => editingRole = null}
                  class="inline-flex items-center px-3 py-2 border border-gray-300 shadow-sm text-sm font-medium rounded-md text-gray-700 bg-white hover:bg-gray-50"
                >
                  Cancel
                </button>
                <button
                  onclick={() => updateRole(editingRole)}
                  class="inline-flex items-center px-3 py-2 border border-transparent text-sm font-medium rounded-md text-white bg-blue-600 hover:bg-blue-700"
                >
                  <Save class="h-4 w-4 mr-2" />
                  Save Changes
                </button>
              </div>
            {:else}
              <button
                onclick={() => editingRole = {...role}}
                class="inline-flex items-center px-3 py-2 border border-gray-300 shadow-sm text-sm font-medium rounded-md text-gray-700 bg-white hover:bg-gray-50"
              >
                Edit
              </button>
            {/if}
          </div>
        </div>

        {#if editingRole?.name === role.name}
          <div class="mt-6">
            <h4 class="text-sm font-medium text-gray-900">Permissions</h4>
            <div class="mt-4 border rounded-lg overflow-hidden">
              <table class="min-w-full divide-y divide-gray-200">
                <thead class="bg-gray-50">
                  <tr>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase">Resource</th>
                    {#each availableActions as action}
                      <th class="px-6 py-3 text-center text-xs font-medium text-gray-500 uppercase">
                        {action}
                      </th>
                    {/each}
                  </tr>
                </thead>
                <tbody class="bg-white divide-y divide-gray-200">
                  {#each availableResources as resource}
                    <tr>
                      <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-gray-900">
                        {resource}
                      </td>
                      {#each availableActions as action}
                        <td class="px-6 py-4 whitespace-nowrap text-center">
                          <input
                            type="checkbox"
                            checked={editingRole && editingRole.permissions.some(p => p.action === action && p.resource === resource)}
                            onchange={() => editingRole && togglePermission(editingRole, action, resource)}
                            class="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                          />
                        </td>
                      {/each}
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </div>
        {:else}
          <div class="mt-4">
            <h4 class="text-sm font-medium text-gray-900">Current Permissions</h4>
            <div class="mt-2 flex flex-wrap gap-2">
              {#each role.permissions as permission}
                <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800">
                  {permission.action}:{permission.resource}
                </span>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>
