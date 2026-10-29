import type { ReleaseImage } from '$lib/proto/distroface/v1/release_pb';
import { configStore } from '$lib/stores/config.svelte';
import { portalStore } from '$lib/stores/portal.svelte';

const SESSION_KEY = 'distroface_session';

// Pull command for a release image, built from the portal host when the server left it empty
export function pullLine(img: ReleaseImage): string {
	return (
		img.pullCommand ||
		`docker pull ${configStore.publicHostname}/${portalStore.imageRef(img.namespace, img.name)}:${img.tag}`
	);
}

// Short lived cookie scoped to the repository so a direct download carries the session
export function primeDownload(namespace: string, name: string) {
	const token = localStorage.getItem(SESSION_KEY);
	if (!token) return;
	const secure = location.protocol === 'https:' ? '; Secure' : '';
	const path = `/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/`;
	document.cookie = `${SESSION_KEY}=${token}; Path=${path}; Max-Age=60; SameSite=Strict${secure}`;
}

// Where a new asset of a release streams to
export function assetUploadUrl(
	namespace: string,
	name: string,
	tag: string,
	file: string,
	label = ''
): string {
	const q = new URLSearchParams({ name: file });
	if (label) q.set('label', label);
	return `/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/-/releases/${encodeURIComponent(tag)}/assets?${q}`;
}
