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
	private gen = 0;

	get live() {
		return this.running;
	}

	// Idempotent, first caller opens the stream
	ensure() {
		if (this.running || typeof window === 'undefined') return;
		this.running = true;
		void this.loop(++this.gen);
	}

	stop() {
		this.running = false;
		this.abort?.abort();
	}

	// Reopens the stream so the server filters for the new identity
	restart() {
		if (!this.running) return;
		this.stop();
		this.ensure();
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

	// A superseded loop leaves the state to its replacement
	private async loop(gen: number) {
		while (this.running && gen === this.gen) {
			const abort = new AbortController();
			this.abort = abort;
			try {
				const stream = rpcClient.mirror.watchSyncs(
					{},
					{ ...silentCallOptions, signal: abort.signal }
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
			if (gen !== this.gen) break;
			this.active = {};
			this.activeEvents = {};
			this.messages = {};
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

// Dev reloads of this module must not strand the previous stream
if (import.meta.hot) {
	import.meta.hot.dispose((data) => {
		data.live = mirrorSyncStore.live;
		mirrorSyncStore.stop();
	});
	if (import.meta.hot.data.live) mirrorSyncStore.ensure();
}
