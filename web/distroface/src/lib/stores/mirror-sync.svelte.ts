import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import { SyncPhase, type SyncEvent } from '$lib/proto/distroface/v1/mirror_pb';
import { ConnectError, Code } from '@connectrpc/connect';

const RETRY_MS = 5000;

// Live mirror sync state pushed from the server
class MirrorSyncStore {
	active = $state<Record<string, boolean>>({});
	// Last start frame per running sync, names the repository behind a key
	activeEvents = $state<Record<string, SyncEvent>>({});
	lastFinished = $state<SyncEvent | null>(null);
	finishedSeq = $state(0);
	private running = false;
	private abort: AbortController | null = null;

	// Idempotent, first caller opens the stream
	ensure() {
		if (this.running || typeof window === 'undefined') return;
		this.running = true;
		void this.loop();
	}

	stop() {
		this.running = false;
		this.abort?.abort();
	}

	syncing(
		kind: 'image' | 'artifact' | 'git' | 'git_push' | 'import',
		repoId: string | number | bigint
	): boolean {
		return !!this.active[`${kind}:${repoId}`];
	}

	// Latest progress line per active sync, imports report phases
	messages = $state<Record<string, string>>({});

	message(kind: 'git' | 'git_push' | 'import', repoId: string): string {
		return this.messages[`${kind}:${repoId}`] ?? '';
	}

	private async loop() {
		while (this.running) {
			this.abort = new AbortController();
			try {
				const stream = rpcClient.mirror.watchSyncs(
					{},
					{ ...silentCallOptions, signal: this.abort.signal }
				);
				for await (const ev of stream) {
					this.apply(ev);
				}
			} catch (err) {
				if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
					this.running = false;
					break;
				}
			}
			this.active = {};
			this.activeEvents = {};
			if (!this.running) break;
			await new Promise((r) => setTimeout(r, RETRY_MS));
		}
	}

	private apply(ev: SyncEvent) {
		const key = `${ev.kind}:${ev.repoId}`;
		if (ev.phase === SyncPhase.STARTED) {
			this.active[key] = true;
			this.activeEvents[key] = ev;
			if (ev.message) this.messages[key] = ev.message;
		} else if (ev.phase === SyncPhase.COMPLETED || ev.phase === SyncPhase.FAILED) {
			delete this.active[key];
			delete this.activeEvents[key];
			delete this.messages[key];
			this.lastFinished = ev;
			this.finishedSeq++;
		}
	}
}

export const mirrorSyncStore = new MirrorSyncStore();
