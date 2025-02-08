<script lang="ts">
  import { Save, Plus, Users, AlertCircle } from "lucide-svelte";
  import { auth, api } from "$lib/stores/auth.svelte";
  import { groups } from "$lib/stores/groups.svelte";
  import type { Group } from "$lib/stores/groups.svelte";

  let editingGroup = $state<Group | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let roles = $state<string[]>([]);

  async function fetchRoles() {
    try {
      const response = await api.get("/api/v1/roles");
      const data = await response.json();
      roles = data.map((role: any) => role.name);
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to fetch roles";
    }
  }

  async function updateGroup(group: Group|null) {
    if (!group) return;
    try {
      loading = true;
      await api.put(`/api/v1/groups/${group.name}`, {
        description: group.description,
        roles: group.roles,
        scope: group.scope || 'system:all'
      });
      await groups.fetchGroups();
      editingGroup = null;
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to update group";
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (auth.isAuthenticated) {
      Promise.all([
        groups.fetchGroups(),
        fetchRoles()
      ]).catch(console.error);
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
    {#each groups.all as group}
      <div class="bg-white shadow-sm rounded-lg p-6">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-lg font-medium text-gray-900 flex items-center">
              <Users class="h-5 w-5 mr-2 text-gray-400" />
              {group.name}
            </h3>
            {#if editingGroup?.name === group.name}
              <input
                type="text"
                bind:value={editingGroup.description}
                class="mt-1 block w-full rounded-md border-gray-300 shadow-sm focus:border-blue-500 focus:ring-blue-500 sm:text-sm"
              />
            {:else}
              <p class="mt-1 text-sm text-gray-500">{group.description}</p>
            {/if}
          </div>
          <div>
            {#if editingGroup?.name === group.name}
              <div class="flex space-x-3">
                <button
                  onclick={() => editingGroup = null}
                  class="inline-flex items-center px-3 py-2 border border-gray-300 shadow-sm text-sm font-medium rounded-md text-gray-700 bg-white hover:bg-gray-50"
                >
                  Cancel
                </button>
                <button
                  onclick={() => updateGroup(editingGroup)}
                  class="inline-flex items-center px-3 py-2 border border-transparent text-sm font-medium rounded-md text-white bg-blue-600 hover:bg-blue-700"
                >
                  <Save class="h-4 w-4 mr-2" />
                  Save Changes
                </button>
              </div>
            {:else}
              <button
                onclick={() => editingGroup = {...group}}
                class="inline-flex items-center px-3 py-2 border border-gray-300 shadow-sm text-sm font-medium rounded-md text-gray-700 bg-white hover:bg-gray-50"
              >
                Edit
              </button>
            {/if}
          </div>
        </div>

        {#if editingGroup?.name === group.name}
          <div class="mt-6">
            <h4 class="text-sm font-medium text-gray-900">Assigned Roles</h4>
            <div class="mt-4 space-y-2">
              {#each roles as role}
                <label class="flex items-center">
                  <input
                    type="checkbox"
                    checked={editingGroup.roles.includes(role)}
                    onchange={(e) => {
                      if (editingGroup) {
                        if (e.currentTarget.checked) {
                          editingGroup.roles = [...editingGroup.roles, role];
                        } else {
                          editingGroup.roles = editingGroup.roles.filter(r => r !== role);
                        }
                      }
                    }}
                    class="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                  />
                  <span class="ml-2 text-sm text-gray-900">{role}</span>
                </label>
              {/each}
            </div>
          </div>
        {:else}
          <div class="mt-4">
            <h4 class="text-sm font-medium text-gray-900">Assigned Roles</h4>
            <div class="mt-2 flex flex-wrap gap-2">
              {#each group.roles as role}
                <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800">
                  {role}
                </span>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>
