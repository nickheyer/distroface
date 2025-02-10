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
  repoSearchTerm: string;
  fileSearchTerm: string;
  requiredProperties: string[],
  indexedProperties: string[],
  properties: Record<string,string>|null
  settingsError: string | null
}>({
  repositories: [],
  artifacts: {},
  loading: false,
  error: null,
  uploadProgress: {},
  currentRepo: null,
  repoSearchTerm: '',
  fileSearchTerm: '',
  requiredProperties: [],
  indexedProperties: [],
  properties: {},
  settingsError: null

});

// COMPUTED
const filteredRepositories = $derived(() => {
  const searchLower = state.repoSearchTerm.toLowerCase();
  return state.repositories.filter((repo) =>
      repo.name.toLowerCase().includes(searchLower) ||
      repo.description.toLowerCase().includes(searchLower)
  );
});

const filteredArtifacts = $derived(() => {
  const searchLower = state.fileSearchTerm.toLowerCase();
  if (!state.currentRepo?.id) {
    console.log('No repo id set: ', state.currentRepo);
    return {};
  }

  if (searchLower === '') {
    return state.artifacts;
  }

  const tmpArtifacts = Object.assign({}, state.artifacts);
  tmpArtifacts[state.currentRepo.id] = state.artifacts[state.currentRepo.id].filter((artifact) =>
      artifact.name.toLowerCase().includes(searchLower)
  );

  return tmpArtifacts;
});

async function fetchRepositories() {
    state.loading = true;
    state.error = null;

    try {
        const response = await api.get('/api/v1/artifacts/repos');
        const data = await response.json();
        state.repositories = data || [];
        for (const r of state.repositories) {
          await fetchArtifacts(r.name);
        }
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

async function uploadArtifact(repo: string, file: File, version: string, path: string, addProps: Record<string,string>|null) {
  const uploadId = uuidv4();
  state.uploadProgress[uploadId] = 0;

  try {
    // INIT UPLOAD
    const initResponse = await api.post(`/api/v1/artifacts/${encodeURIComponent(repo)}/upload`, {});
    if (!initResponse.ok) throw new Error('Failed to initialize upload');
    
    const uploadEndpoint = initResponse.headers.get('Location');
    if (!uploadEndpoint) throw new Error('Invalid upload location');

    // CHUNK SIZE INCREASED FOR LARGE FILES
    const chunkSize = 50 * 1024 * 1024; // 50MB chunks
    const totalChunks = Math.ceil(file.size / chunkSize);
    let uploadedChunks = 0;

    for (let start = 0; start < file.size; start += chunkSize) {
      const chunk = file.slice(start, Math.min(start + chunkSize, file.size));
      const response = await api.patch(uploadEndpoint, chunk, true);
      
      if (!response.ok) throw new Error('Failed to upload chunk');
      
      uploadedChunks++;
      state.uploadProgress[uploadId] = Math.floor((uploadedChunks / totalChunks) * 100);
    }

    // COMPLETE UPLOAD
    const props = { ...state.properties, ...addProps };
    const completeUrl = `${uploadEndpoint}?version=${encodeURIComponent(version)}&path=${encodeURIComponent(path)}`;
    const completeResponse = await api.put(
      completeUrl,
      props
    );
    
    if (!completeResponse.ok) throw new Error('Failed to complete upload');

    delete state.uploadProgress[uploadId];
    
  } catch (err) {
    delete state.uploadProgress[uploadId];
    throw err;
  }
}

async function fetchArtifacts(repoName: string) {
  state.loading = true;
  state.error = null;

  try {
      const response = await api.get(`/api/v1/artifacts/${encodeURIComponent(repoName)}/versions`);
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

async function fetchArtifactSettings() {
  try {
    const response = await api.get('/api/v1/settings/artifacts');
    if (response.ok) {
      const settings = await response.json();
      state.requiredProperties = settings.properties?.required || [];
      state.indexedProperties = settings.properties?.indexed || [];
      state.properties = state.requiredProperties.reduce<Record<string,string>>((acc, prop) => {
        acc[prop] = '';
        return acc;
      }, {});
    }
  } catch (err) {
    state.settingsError = 'Failed to load artifact settings';
  }
}

async function deleteArtifact(repo: string, version: string, path: string) {
  try {
    await api.delete(`/api/v1/artifacts/${encodeURIComponent(repo)}/${encodeURIComponent(version)}/${encodeURIComponent(path)}`);
    await fetchArtifacts(repo);
  } catch (err) {
    throw new Error(err instanceof Error ? err.message : 'Failed to delete artifact');
  }
}

async function updateMetadata(repo: string, artifactId: string, metadata: Record<string, any>) {
    try {
        await api.put(`/api/v1/artifacts/${encodeURIComponent(repo)}/${artifactId}/metadata`, metadata);
        await fetchArtifacts(repo);
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to update metadata';
        throw err;
    }
}

async function updateProperties(repo: string, artifactId: string, props: Record<string, any>) {
    try {
        await api.put(`/api/v1/artifacts/${encodeURIComponent(repo)}/${artifactId}/properties`, props);
        await fetchArtifacts(repo);
    } catch (err) {
        state.error = err instanceof Error ? err.message : 'Failed to update properties';
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

function sumArtifacts(artifacts: Artifact[]): string {
    const sumOfArtifacts = artifacts.reduce((acc, cur) => acc + cur.size, 0);
    return formatSize(sumOfArtifacts);
}

export const artifacts = {
    // STATE
    get repositories() { return state.repositories },
    get artifacts() { return state.artifacts },
    get loading() { return state.loading },
    get error() { return state.error },
    get currentRepo() { return state.currentRepo },
    set currentRepo(repo: ArtifactRepository | null) { state.currentRepo = repo },
    get repoSearchTerm() { return state.repoSearchTerm },
    set repoSearchTerm(term: string) { state.repoSearchTerm = term },
    get fileSearchTerm() { return state.fileSearchTerm },
    set fileSearchTerm(term: string) { state.fileSearchTerm = term },
    get uploadProgress() { return state.uploadProgress },
    get properties() { return state.properties || {} },
    get indexedProperties() { return state.indexedProperties || [] },
    get requiredProperties() { return state.requiredProperties || [] },
    get filteredRepos() { return filteredRepositories() },
    get filteredArtifacts() { return filteredArtifacts() },

    // ACTIONS
    fetchRepositories,
    createRepository,
    deleteRepository,
    uploadArtifact,
    fetchArtifacts,
    deleteArtifact,
    updateMetadata,
    formatSize,
    sumArtifacts,
    updateProperties,
    fetchArtifactSettings
};
