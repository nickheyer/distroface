import { sessionToken } from '$lib/rpc';
import { saveBlob } from '$lib/net';

function authHeaders(): HeadersInit {
	const token = sessionToken();
	return token ? { Authorization: `Bearer ${token}` } : {};
}

// The _ns marker keeps org repos namespaced on the plain http api
export function artifactFileUrl(namespace: string, repo: string, version: string, path: string): string {
	const parts = [namespace, repo, version, ...path.split('/')].map(encodeURIComponent);
	return `/api/v1/artifacts/_ns/${parts.join('/')}`;
}

export async function downloadArtifact(namespace: string, repo: string, version: string, path: string, filename: string) {
	const resp = await fetch(artifactFileUrl(namespace, repo, version, path), { headers: authHeaders() });
	if (!resp.ok) throw new Error(`Download failed (${resp.status})`);
	saveBlob(await resp.blob(), filename);
}

const CHUNK = 8 * 1024 * 1024;

// Streams a file to an upload session in fixed chunks
export async function uploadChunks(uploadUrl: string, file: File, onProgress: (sent: number) => void) {
	for (let off = 0; off < file.size; off += CHUNK) {
		const resp = await fetch(uploadUrl, {
			method: 'PATCH',
			headers: authHeaders(),
			body: file.slice(off, Math.min(off + CHUNK, file.size))
		});
		if (!resp.ok) throw new Error(`Upload failed (${resp.status})`);
		onProgress(Math.min(off + CHUNK, file.size));
	}
	if (file.size === 0) {
		const resp = await fetch(uploadUrl, { method: 'PATCH', headers: authHeaders(), body: new Blob([]) });
		if (!resp.ok) throw new Error(`Upload failed (${resp.status})`);
		onProgress(0);
	}
}
