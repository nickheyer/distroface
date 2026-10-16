import type { ReleaseImage } from '$lib/proto/distroface/v1/release_pb';
import { configStore } from '$lib/stores/config.svelte';
import { portalStore } from '$lib/stores/portal.svelte';

const SESSION_KEY = 'distroface_session';

// Pull command for a release image, built from the portal host when the server left it empty
export function pullLine(img: ReleaseImage): string {
	return img.pullCommand || `docker pull ${configStore.publicHostname}/${portalStore.imageRef(img.namespace, img.name)}:${img.tag}`;
}

// Scoped cookie carries auth for a direct asset download
export function primeDownload() {
	const token = localStorage.getItem(SESSION_KEY);
	if (!token) return;
	const secure = location.protocol === 'https:' ? '; Secure' : '';
	document.cookie = `${SESSION_KEY}=${token}; Path=/api/v1/artifacts; Max-Age=60; SameSite=Strict${secure}`;
}
