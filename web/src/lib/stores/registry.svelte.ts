import { auth } from './auth.svelte';
import type { ImageRepository, RegistryStats } from '$lib/types/registry.svelte';

export interface ImageTag {
  name: string;
  size: number;
  digest: string;
  created: string;
}

// STATE
const state = $state<{
    repositories: ImageRepository[];
    loading: boolean;
    error: string | null;
    searchTerm: string;
    selectedRepository: string | null;
}>({
    repositories: [],
    loading: false,
    error: null,
    searchTerm: '',
    selectedRepository: null
});

// COMPUTED
const filteredRepositories = $derived(() => {
  const searchLower = state.searchTerm.toLowerCase();
  return state.repositories
      .filter(repo => 
          repo.name.toLowerCase().includes(searchLower)
      )
      .sort((a, b) => {
          // SORT BY PRIVATE STATUS THEN NAME
          if (a.private !== b.private) {
              return a.private ? 1 : -1;
          }
          return a.name.localeCompare(b.name);
      });
});

// ACTIONS
async function fetchRepositories() {
    state.loading = true;
    state.error = null;
    
    try {
        const response = await fetch('/api/v1/repositories', {
            headers: {
                'Authorization': `Bearer ${auth.token}`
            }
        });

        if (!response.ok) throw new Error('Failed to fetch repositories');
        
        state.repositories = await response.json();
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to load repositories';
    } finally {
        state.loading = false;
    }
}

async function updateVisibility(repository: ImageRepository, newPrivateState: boolean) {
    try {
      const response = await fetch('/api/v1/repositories/visibility', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${auth.token}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          id: repository.id,
          private: newPrivateState
        })
      });
  
      if (!response.ok) {
        throw new Error('Failed to update visibility');
      }
  
      // GET UPDATED STATE REFRESH
      await fetchRepositories();
    } catch (err) {
      throw new Error(err instanceof Error ? err.message : 'Failed to update visibility');
    }
  }
  

async function deleteTag(repository: string, tag: string) {
    try {
        state.loading = true;
        const response = await fetch(`/api/v1/repositories/${repository}/tags/${tag}`, {
            method: 'DELETE',
            headers: {
                'Authorization': `Bearer ${auth.token}`
            }
        });

        if (!response.ok) {
            throw new Error('Failed to delete tag');
        }
        
        state.error = null;
        // REFRESH REPOS AFTER DELETE
        await fetchRepositories();
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to delete tag';
        throw err;
    } finally {
        state.loading = false;
    }
}

function formatSize(bytes: number | undefined | null): string {
    if (bytes == null || bytes === undefined) {
        return '0 B';
    }

    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let size = bytes;
    let unitIndex = 0;

    while (size >= 1024 && unitIndex < units.length - 1) {
        size /= 1024;
        unitIndex++;
    }

    return `${size.toFixed(1)} ${units[unitIndex]}`;
}

export const registry = {
    // STATE
    get repositories() { return state.repositories },
    get loading() { return state.loading },
    get error() { return state.error },
    get searchTerm() { return state.searchTerm },
    set searchTerm(value: string) { state.searchTerm = value },
    get filtered() { return filteredRepositories() },

    // ACTIONS
    fetchRepositories,
    deleteTag,
    formatSize,
    updateVisibility,
};
