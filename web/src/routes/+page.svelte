<script lang="ts">
  import { auth } from "$lib/stores/auth.svelte";
  import { registry } from "$lib/stores/registry.svelte";
  import { formatDistance } from "date-fns";
  import { 
    Package, 
    Clock, 
    User, 
    Shield, 
    Settings,
    Activity,
    HardDrive
  } from "lucide-svelte";

  let stats = $state({
    totalSize: 0,
    totalImages: 0,
    lastUpdated: null as string | null
  });

  async function fetchStats() {
    try {
      const response = await fetch("/api/v1/repositories/public", {
        headers: {
          Authorization: `Bearer ${auth.token}`
        }
      });
      const data = await response.json();
      
      stats = {
        totalSize: data.total_size || 0,
        totalImages: data.total_images || 0,
        lastUpdated: data.images?.[0]?.updated_at || null
      };
    } catch (err) {
      console.error("Failed to fetch stats:", err);
    }
  }

  $effect(() => {
    if (auth.isAuthenticated) {
      fetchStats();
    }
  });

  function formatBytes(bytes: number) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }
</script>

{#if auth.user}
  <div class="space-y-6">
    <!-- Profile Header -->
    <div class="bg-white shadow rounded-lg p-6">
      <div class="sm:flex sm:items-center sm:justify-between">
        <div class="sm:flex sm:space-x-5">
          <div class="flex-shrink-0">
            <div class="relative">
              <div class="h-20 w-20 rounded-full bg-gradient-to-r from-blue-500 to-blue-600 flex items-center justify-center">
                <span class="text-3xl font-bold text-white">
                  {auth.user?.username?.[0]?.toUpperCase() ?? 'U'}
                </span>
              </div>
              <span class="absolute bottom-0 right-0 block h-4 w-4 rounded-full bg-green-400 ring-2 ring-white"></span>
            </div>
          </div>
          <div class="mt-4 sm:mt-0 text-center sm:text-left">
            <p class="text-xl font-bold text-gray-900 sm:flex sm:items-center">
              {auth.user?.username}
              {#if auth.hasRole('admins')}
                <span class="ml-2 inline-flex items-center rounded-md bg-blue-50 px-2 py-1 text-xs font-medium text-blue-700 ring-1 ring-inset ring-blue-600/20">
                  Administrator
                </span>
              {/if}
            </p>
            <p class="text-sm text-gray-500">Member Groups: {auth.user?.groups.join(', ')}</p>
          </div>
        </div>
        <div class="mt-5 flex justify-center sm:mt-0">
          <a
            href="/settings"
            class="flex items-center justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50"
          >
            <Settings class="h-5 w-5 mr-2" />
            Account Settings
          </a>
        </div>
      </div>
    </div>

    <!-- Stats Grid -->
    <div class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-4">
      <div class="bg-white overflow-hidden shadow rounded-lg">
        <div class="p-5">
          <div class="flex items-center">
            <div class="flex-shrink-0">
              <Package class="h-6 w-6 text-gray-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">Total Images</dt>
                <dd class="text-lg font-semibold text-gray-900">{stats.totalImages}</dd>
              </dl>
            </div>
          </div>
        </div>
      </div>

      <div class="bg-white overflow-hidden shadow rounded-lg">
        <div class="p-5">
          <div class="flex items-center">
            <div class="flex-shrink-0">
              <HardDrive class="h-6 w-6 text-gray-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">Total Storage</dt>
                <dd class="text-lg font-semibold text-gray-900">{formatBytes(stats.totalSize)}</dd>
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
                <dt class="text-sm font-medium text-gray-500 truncate">Access Level</dt>
                <dd class="text-lg font-semibold text-gray-900">
                  {auth.hasRole('admins') ? 'Admin' : auth.hasRole('developers') ? 'Developer' : 'Reader'}
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
              <Activity class="h-6 w-6 text-gray-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">Last Activity</dt>
                <dd class="text-lg font-semibold text-gray-900">
                  {stats.lastUpdated ? formatDistance(new Date(stats.lastUpdated), new Date(), { addSuffix: true }) : 'Never'}
                </dd>
              </dl>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Quick Actions -->
    <div class="bg-white shadow rounded-lg divide-y divide-gray-200">
      <div class="p-6">
        <h2 class="text-lg font-medium text-gray-900">Quick Actions</h2>
        <div class="mt-6 grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-3">
          <a href="/registry" class="group relative rounded-lg p-6 bg-gray-50 hover:bg-gray-100">
            <div>
              <span class="inline-flex rounded-lg p-3 bg-blue-50 text-blue-700 ring-4 ring-white">
                <Package class="h-6 w-6" />
              </span>
            </div>
            <div class="mt-4">
              <h3 class="text-lg font-medium text-gray-900">
                Browse Registry
              </h3>
              <p class="mt-2 text-sm text-gray-500">
                View and manage your container images and repositories.
              </p>
            </div>
          </a>

          <a href="/migration" class="group relative rounded-lg p-6 bg-gray-50 hover:bg-gray-100">
            <div>
              <span class="inline-flex rounded-lg p-3 bg-green-50 text-green-700 ring-4 ring-white">
                <Clock class="h-6 w-6" />
              </span>
            </div>
            <div class="mt-4">
              <h3 class="text-lg font-medium text-gray-900">
                Migrate Images
              </h3>
              <p class="mt-2 text-sm text-gray-500">
                Import container images from other registries.
              </p>
            </div>
          </a>

          {#if auth.hasRole('admins')}
            <a href="/users" class="group relative rounded-lg p-6 bg-gray-50 hover:bg-gray-100">
              <div>
                <span class="inline-flex rounded-lg p-3 bg-purple-50 text-purple-700 ring-4 ring-white">
                  <User class="h-6 w-6" />
                </span>
              </div>
              <div class="mt-4">
                <h3 class="text-lg font-medium text-gray-900">
                  Manage Users
                </h3>
                <p class="mt-2 text-sm text-gray-500">
                  Add, remove, and manage user access and permissions.
                </p>
              </div>
            </a>
          {/if}
        </div>
      </div>
    </div>
  </div>
  <div class="bg-white shadow sm:rounded-lg">
      <div class="px-4 py-5 sm:p-6">
          <h3 class="text-base font-semibold leading-6 text-gray-900">
              Welcome to DistroFace Registry
          </h3>
          <div class="mt-2 max-w-xl text-sm text-gray-500">
              <p>You are logged in as {auth.user.username}</p>
              <p class="mt-2">Your groups: {auth.user.groups.join(", ")}</p>
          </div>
      </div>
  </div>
{:else}
  <div class="text-center">
      Redirecting to login...
  </div>
{/if}
