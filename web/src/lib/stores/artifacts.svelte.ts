import { api, auth } from './auth.svelte';
import type { Artifact, ArtifactRepository } from '$lib/types/artifacts.svelte';
import { v4 as uuidv4 } from 'uuid';


const state = $state<{
  repositories: ArtifactRepository[];
  artifacts: { [repoId: number]: Artifact[] };
  loading: boolean;
  error: string | null;
  uploadProgress: { [uploadId: string]: number };
  currentRepo: ArtifactRepository | null;
  searchTerm: string;
}>({
  repositories: [],
  artifacts: {},
  loading: false,
  error: null,
  uploadProgress: {},
  currentRepo: null,
  searchTerm: ''
});

// COMPUTED
const filteredRepositories = $derived(() => {
  const searchLower = state.searchTerm.toLowerCase();
  return state.repositories.filter((repo) =>
      repo.name.toLowerCase().includes(searchLower) ||
      repo.description.toLowerCase().includes(searchLower)
  );
});

async function fetchRepositories() {
    state.loading = true;
    state.error = null;

    try {
        const response = await api.get('/api/v1/artifacts/repos');
        const data = await response.json();
        state.repositories = data || [];
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to fetch repositories';
        throw err;
    } finally {
        state.loading = false;
    }
}

async function createRepository(name: string, description: string, isPrivate: boolean) {
    try {
        await api.post('/api/v1/artifacts/repos', {
            name,
            description,
            private: isPrivate
        });
        await fetchRepositories();
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to create repository';
        throw err;
    }
}

async function deleteRepository(name: string) {
    try {
        await api.delete(`/api/v1/artifacts/repos/${name}`);
        await fetchRepositories();
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to delete repository';
        throw err;
    }
}

async function uploadArtifact(repo: string, file: File, version: string, path: string) {
  const uploadId = uuidv4();
  state.uploadProgress[uploadId] = 0;

  try {
      // INIT UPLOAD
      const initResponse = await api.post(`/api/v1/artifacts/${repo}/upload`, {});
      if (!initResponse.ok) throw new Error('Failed to initialize upload');
      
      const location = initResponse.headers.get('Location');
      const uploadEndpoint = `/api/v1${location?.split('/api/v1')[1]}`;
      if (!uploadEndpoint) throw new Error('Invalid upload location');

      // UPLOADING 5MB CHUNKS
      const chunkSize = 5 * 1024 * 1024;
      const totalChunks = Math.ceil(file.size / chunkSize);
      let uploadedChunks = 0;

      for (let start = 0; start < file.size; start += chunkSize) {
          const chunk = file.slice(start, start + chunkSize);
          const response = await api.patch(uploadEndpoint, chunk);
          if (!response.ok) throw new Error('Failed to upload chunk');
          
          uploadedChunks++;
          state.uploadProgress[uploadId] = (uploadedChunks / totalChunks) * 100;
      }

      // COMPLETE UPLOAD WITH NEW UUID IN PATH
      const artifactId = uuidv4();
      const completePath = path.includes(artifactId) ? path : `${artifactId}/${path}`;
      
      const completeResponse = await api.put(
          `${uploadEndpoint}?version=${encodeURIComponent(version)}&path=${encodeURIComponent(completePath)}`,
          null
      );
      
      if (!completeResponse.ok) throw new Error('Failed to complete upload');

      delete state.uploadProgress[uploadId];
      await fetchArtifacts(repo);
  } catch (err) {
      delete state.uploadProgress[uploadId];
      throw err;
  }
}

async function fetchArtifacts(repoName: string) {
  state.loading = true;
  state.error = null;

  try {
      const response = await api.get(`/api/v1/artifacts/${repoName}/versions`);
      const data: Record<string, Artifact[]> = await response.json();
      const repo = state.repositories.find(r => r.name === repoName);
      if (repo) {
          // FORCE NEW ID IF NONE EXISTS
          const processedArtifacts = Object.values(data).flat().map(artifact => ({
              ...artifact,
              id: artifact.id || uuidv4()
          }));
          state.artifacts[repo.id] = processedArtifacts;
      }
  } catch (err) {
      state.error = err instanceof Error ? err.message : 'Failed to fetch artifacts';
      throw err;
  } finally {
      state.loading = false;
  }
}

async function deleteArtifact(repo: string, version: string, path: string) {
    try {
        await api.delete(`/api/v1/artifacts/${repo}/${version}/${path}`);
        await fetchArtifacts(repo);
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to delete artifact';
        throw err;
    }
}

async function updateMetadata(repo: string, artifactId: string, metadata: Record<string, any>) {
    try {
        await api.put(`/api/v1/artifacts/${repo}/${artifactId}/metadata`, metadata);
        await fetchArtifacts(repo);
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to update metadata';
        throw err;
    }
}

function formatSize(bytes: number): string {
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let size = bytes;
    let unitIndex = 0;
    
    while (size >= 1024 && unitIndex < units.length - 1) {
        size /= 1024;
        unitIndex++;
    }
    
    return `${size.toFixed(1)} ${units[unitIndex]}`;
}

export const artifacts = {
    // STATE
    get repositories() { return state.repositories },
    get artifacts() { return state.artifacts },
    get loading() { return state.loading },
    get error() { return state.error },
    get currentRepo() { return state.currentRepo },
    set currentRepo(repo: ArtifactRepository | null) { state.currentRepo = repo },
    get searchTerm() { return state.searchTerm },
    set searchTerm(term: string) { state.searchTerm = term },
    get uploadProgress() { return state.uploadProgress },
    get filtered() { return filteredRepositories() },

    // ACTIONS
    fetchRepositories,
    createRepository,
    deleteRepository,
    uploadArtifact,
    fetchArtifacts,
    deleteArtifact,
    updateMetadata,
    formatSize
};
