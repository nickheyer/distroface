import { auth } from './auth.svelte';
import type { ImageRepository, RegistryStats } from '$lib/types/registry.svelte';

export interface ImageTag {
  name: string;
  size: number;
  digest: string;
  created: string;
  owner?: string;
  isOwned?: boolean;
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
          // SORT BY UPDATED TIME (NEWEST FIRST), THEN PRIVATE STATUS, THEN NAME
          const timeA = new Date(a.updated_at).getTime();
          const timeB = new Date(b.updated_at).getTime();
          
          if (timeA !== timeB) {
              return timeB - timeA;
          }
          
          // THEN BY PRIVATE STATUS
          if (a.private !== b.private) {
              return a.private ? 1 : -1;
          }
          
          // FINALLY BY NAME
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
        
        const data = await response.json();
        let repoData = Array.isArray(data) ? data : data.images || [];
        const username = auth.user?.username;
        const repoMap = new Map<string, any>();
        
        repoData.forEach((img: any) => {
            if (!img.tags || !Array.isArray(img.tags)) {
                console.error('Missing or invalid tags property:', img);
                return;
            }
            
            // "id": "sha256:757d680068d77be46fd1ea20fb21db16f150468c5e7079a08a2e4705aec096ac",
            // "name": "iss-warden",
            // "tags": [
            //     {
            //         "name": "test",
            //         "size": 3993626,
            //         "digest": "sha256:757d680068d77be46fd1ea20fb21db16f150468c5e7079a08a2e4705aec096ac",
            //         "created": "2025-04-01T22:05:46-07:00"
            //     }
            // ],
            // "updated_at": "2025-04-01T22:05:46-07:00",
            // "owner": "admin",
            // "size": 3993626,
            // "private": false

            const tagObjects = img.tags.map((tag:any) => {
                if (tag.created && new Date(tag.created).toString() === 'Invalid Date') {
                    console.warn('Invalid date format detected:', tag.created);
                    tag.created = new Date().toISOString();
                }
                
                return tag;
            });
            
            if (!repoMap.has(img.name)) {
                repoMap.set(img.name, {
                    id: img.image_id,
                    name: img.name,
                    tags: tagObjects,
                    updated_at: img.updated_at,
                    owner: img.owner,
                    private: img.private,
                    size: img.size || 0,
                    isOwned: img.owner === username
                });
            } else {
                const repo = repoMap.get(img.name);
                repo.tags = [...repo.tags, ...tagObjects];
                if (new Date(img.updated_at) > new Date(repo.updated_at)) {
                    repo.updated_at = img.updated_at;
                }
                
                repo.size += (img.size || 0);
            }
        });
        
        state.repositories = Array.from(repoMap.values());
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
    if (bytes === undefined || bytes === null || isNaN(bytes) || bytes < 0) {
        return '0 B';
    }
    
    const numBytes = Number(bytes);
    if (numBytes === 0) {
        return '0 B';
    }

    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let size = numBytes;
    let unitIndex = 0;

    while (size >= 1024 && unitIndex < units.length - 1) {
        size /= 1024;
        unitIndex++;
    }

    try {
        return `${size.toFixed(1)} ${units[unitIndex]}`;
    } catch (e) {
        console.error("Error formatting size:", e, "bytes:", bytes);
        return '0 B';
    }
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
