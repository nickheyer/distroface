import { gitRepoStore } from './git-repo.svelte';
import { mirrorSyncStore } from './mirror-sync.svelte';
import { notificationStore } from './notification.svelte';

// Everything fetched under the previous identity goes
export function resetSessionCaches() {
	gitRepoStore.invalidate();
	notificationStore.reset();
	mirrorSyncStore.restart();
}
