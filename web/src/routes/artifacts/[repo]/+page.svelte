<script lang="ts">
  import { artifacts } from "$lib/stores/artifacts.svelte";
  import { page } from "$app/state";
  import ArtifactBrowser from "$lib/components/artifacts/ArtifactBrowser.svelte";
  import { Loader2 } from "lucide-svelte";
  import type { ArtifactRepository } from "$lib/types/artifacts.svelte";

  // GET REPO FROM URL
  const repoName = $derived(page.params.repo);
  let repository = $state<ArtifactRepository>();
  let loading = $state(true);
  let error = $state('');

  async function loadRepository() {
      loading = true;
      error = '';
      try {
          await artifacts.fetchRepositories();
          repository = artifacts.repositories.find(r => r.name === repoName);
          if (!repository) {
              error = "Repository not found";
          }
      } catch (err) {
          error = err instanceof Error ? err.message : "Failed to load repository";
      } finally {
          loading = false;
      }
  }

  $effect(() => {
      loadRepository();
  });
</script>

{#if loading}
  <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
  </div>
{:else if error}
  <div class="rounded-lg bg-red-50 p-4">
      <div class="flex">
          <div class="ml-3">
              <h3 class="text-sm font-medium text-red-800">Error</h3>
              <div class="mt-2 text-sm text-red-700">
                  <p>{error}</p>
              </div>
          </div>
      </div>
  </div>
{:else if repository}
  <ArtifactBrowser initialRepo={ repository } />
{/if}
