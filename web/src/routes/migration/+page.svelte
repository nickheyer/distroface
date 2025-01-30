<script lang="ts">
  import Tag from '$lib/components/Tag.svelte';
  import { auth, api } from "$lib/stores/auth.svelte";
  import {
    AlertCircle,
    ArrowRight,
    Loader2,
    Server,
    Search,
    Tag as TagIcon,
    Package,
  } from "lucide-svelte";

  type RegistryImage = {
    name: string;
    tags: string[];
  };

  // FORM STATE
  let sourceRegistry = $state("");
  let username = $state("");
  let password = $state("");
  let searchTerm = $state("");

  // LOADING STATES
  let loading = $state(false);
  let fetchingImages = $state(false);
  let migrationInProgress = $state(false);

  // DATA STATES
  let error = $state<string | null>(null);
  let images = $state<RegistryImage[]>([]);
  let selectedImages = $state<{ [key: string]: string[] }>({});
  let taskId = $state<string | null>(null);
  let taskStatus = $state<any | null>(null);

  // FETCH AVAILABLE IMAGES AND TAGS
  async function fetchRegistryContents() {
    if (!sourceRegistry) return;

    fetchingImages = true;
    error = null;
    images = [];
    selectedImages = {};

    try {
      // USE PROXY ENDPOINT FOR CATALOG
      const response = await api.get(
        // WE QUERY PARAM THE USER/PASS FOR THE REMOTE, BUT THIS USER AUTH TOKEN STILL ADDED TO HEADERS
        `/api/v1/registry/proxy/catalog?registry=${encodeURIComponent(sourceRegistry)}&username=${encodeURIComponent(username)}&password=${encodeURIComponent(password)}`
      );

      const data = await response.json();
      const repositories = data.repositories || [];

      // FETCH TAGS FOR EACH REPOSITORY
      const imagePromises = repositories.map(async (repo: string) => {
        // USE PROXY ENDPOINT FOR TAGS
        const tagResponse = await api.get(
          `/api/v1/registry/proxy/tags?registry=${encodeURIComponent(sourceRegistry)}&repository=${encodeURIComponent(repo)}&username=${encodeURIComponent(username)}&password=${encodeURIComponent(password)}`
        );

        if (tagResponse.ok) {
          const tagData = await tagResponse.json();
          return {
            name: repo,
            tags: tagData.tags || [],
          };
        }
        return null;
      });

      const results = await Promise.all(imagePromises);
      images = results.filter((img): img is RegistryImage => img !== null);
    } catch (err) {
      error =
        err instanceof Error
          ? err.message
          : "Failed to fetch registry contents";
      images = [];
    } finally {
      fetchingImages = false;
    }
  }

  // TOGGLE IMAGE SELECTION
  function toggleImageTag(imageName: string, tag: string) {
    if (!selectedImages[imageName]) {
      selectedImages[imageName] = [tag];
    } else {
      const index = selectedImages[imageName].indexOf(tag);
      if (index === -1) {
        selectedImages[imageName] = [...selectedImages[imageName], tag];
      } else {
        selectedImages[imageName] = selectedImages[imageName].filter(
          (t) => t !== tag
        );
        if (selectedImages[imageName].length === 0) {
          delete selectedImages[imageName];
        }
      }
    }
    selectedImages = selectedImages; // TRIGGER REACTIVITY
  }

  const filteredImages = $derived(
    images.filter((img) =>
      img.name.toLowerCase().includes(searchTerm.toLowerCase())
    )
  );

  const hasSelectedImages = $derived(Object.keys(selectedImages).length > 0);

  async function handleSubmit(e: Event) {
    e.preventDefault();
    error = null;
    migrationInProgress = true;

    try {
      // CONVERT SELECTIONS TO IMAGE LIST
      const imageList = Object.entries(selectedImages).flatMap(([name, tags]) =>
        tags.map((tag) => `${name}:${tag}`)
      );

      const response = await api.post("/api/v1/registry/migrate", {
        source_registry: sourceRegistry,
        images: imageList,
        username,
        password,
      });

      const data = await response.json();
      taskId = data.task_id;
      pollStatus(data.task_id);
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to start migration";
      migrationInProgress = false;
    }
  }

  async function pollStatus(id: string) {
    try {
      const response = await api.get(
        `/api/v1/registry/migrate/status?task_id=${id}`
      );
      const data = await response.json();

      taskStatus = data;

      if (data.status === "running" || data.status === "pending") {
        setTimeout(() => pollStatus(id), 2000);
      } else {
        migrationInProgress = false;
      }
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to get status";
      migrationInProgress = false;
    }
  }

  function resetMigration() {
    migrationInProgress = false;
    taskId = null;
    taskStatus = null;
    selectedImages = {};
  }

  $effect(() => {
    // WATCH FOR COMPLETED STATUS
    if (taskStatus?.status === "completed" || taskStatus?.status === "failed") {
      migrationInProgress = false;
    }
  });
</script>

<div class="space-y-6">
  <div class="sm:flex sm:items-center sm:justify-between">
    <div>
      <h1 class="text-2xl font-semibold text-gray-900">Image Migration</h1>
      <p class="mt-2 text-sm text-gray-700">
        Migrate images from another registry
      </p>
    </div>
  </div>

  <div class="bg-white shadow-sm rounded-lg p-6">
    {#if error}
      <div class="mb-4 p-4 rounded-md bg-red-50 text-red-700 flex items-center">
        <AlertCircle class="h-5 w-5 mr-2" />
        {error}
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label
          for="source-registry-block"
          class="block text-sm font-medium text-gray-700"
        >
          Source Registry
        </label>
        <div id="source-registry-block" class="mt-1 flex rounded-md shadow-sm">
          <span
            class="inline-flex items-center px-3 rounded-l-md border border-r-0 border-gray-300 bg-gray-50 text-gray-500 sm:text-sm"
          >
            <Server class="h-4 w-4" />
          </span>
          <input
            type="text"
            bind:value={sourceRegistry}
            placeholder="registry.example.com"
            class="flex-1 min-w-0 block w-full px-3 py-2 rounded-none rounded-r-md border border-gray-300 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
          />
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <label
            for="registry-username"
            class="block text-sm font-medium text-gray-700"
          >
            Username (optional)
          </label>
          <input
            id="registry-username"
            type="text"
            bind:value={username}
            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
          />
        </div>
        <div>
          <label
            for="registry-password"
            class="block text-sm font-medium text-gray-700"
          >
            Password (optional)
          </label>
          <input
            id="registry-password"
            type="password"
            bind:value={password}
            class="mt-1 block w-full border border-gray-300 rounded-md shadow-sm py-2 px-3 focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
          />
        </div>
      </div>

      <div class="flex justify-end">
        <button
          type="button"
          onclick={fetchRegistryContents}
          disabled={!sourceRegistry || fetchingImages}
          class="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-sm text-white bg-blue-600 hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500 disabled:bg-gray-400"
        >
          {#if fetchingImages}
            <Loader2 class="animate-spin -ml-1 mr-2 h-4 w-4" />
            Fetching...
          {:else}
            <Search class="-ml-1 mr-2 h-4 w-4" />
            Fetch Images
          {/if}
        </button>
      </div>

      {#if images.length > 0}
      
        <div class="mt-4">


        <div class="flex justify-between items-center mb-4">
                <h3 class="text-lg font-medium text-gray-900">Available Images</h3>
                <div class="relative">
                    <input
                        type="text"
                        bind:value={searchTerm}
                        placeholder="Search images..."
                        class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-md focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
                    />
                    <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
                </div>
          </div>

          <div class="border border-gray-200 rounded-lg overflow-hidden">
            <div class="space-y-4 max-h-96 overflow-y-auto">
              {#each filteredImages as image}
                <div class="border rounded-lg p-4">
                  <div class="flex items-center mb-2">
                    <Package class="h-5 w-5 text-gray-400 mr-2" />
                    <span class="font-medium">{image.name}</span>
                  </div>
                  <div class="mt-2 flex flex-wrap gap-2">
                    {#each image.tags as tag}
                    <Tag
                      name={image.name}
                      tag={tag}
                      onClick={() => toggleImageTag(image.name, tag)}
                      selected={selectedImages[image.name]?.includes(tag)}
                    />
                    {/each}
                </div>
                </div>
              {/each}
            </div>
          </div>
        </div>

        <div class="flex justify-end gap-2">
          {#if taskStatus?.status === "completed"}
            <button
              type="button"
              onclick={resetMigration}
              class="inline-flex items-center px-4 py-2 border border-gray-300 text-sm font-medium rounded-md shadow-sm text-gray-700 bg-white hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500"
            >
              Start New Migration
            </button>
          {:else}
            <button
              type="submit"
              disabled={!hasSelectedImages || migrationInProgress}
              class="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-sm text-white bg-blue-600 hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500 disabled:bg-gray-400"
            >
              {#if migrationInProgress}
                <Loader2 class="animate-spin -ml-1 mr-2 h-4 w-4" />
                Migration in Progress...
              {:else}
                <ArrowRight class="-ml-1 mr-2 h-4 w-4" />
                Start Migration ({Object.values(selectedImages).flat().length} images)
              {/if}
            </button>
          {/if}
        </div>
      {/if}
    </form>

    {#if taskStatus}
      <div class="mt-6 border-t pt-4">
        <h3 class="text-sm font-medium text-gray-700 mb-2">Migration Status</h3>
        <div class="bg-gray-50 rounded-md p-4">
          <div class="flex items-center justify-between mb-2">
            <span class="text-sm font-medium text-gray-900">
              {taskStatus.status.charAt(0).toUpperCase() +
                taskStatus.status.slice(1)}
            </span>
            {#if taskStatus.progress > 0}
              <span class="text-sm text-gray-500">
                {Math.round(taskStatus.progress)}%
              </span>
            {/if}
          </div>
          <div class="w-full bg-gray-200 rounded-full h-2">
            <div
              class="bg-blue-600 h-2 rounded-full transition-all duration-500"
              style="width: {taskStatus.progress}%"
            ></div>
          </div>
          {#if taskStatus.error}
            <p class="mt-2 text-sm text-red-600">
              {taskStatus.error}
            </p>
          {/if}
        </div>
      </div>
    {/if}
  </div>
</div>
