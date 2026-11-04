import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import type { Notification, NotificationEvent } from '$lib/proto/distroface/v1/notification_pb';
import { ConnectError, Code } from '@connectrpc/connect';

const RETRY_MS = 5000;

// Unread count kept live by the server stream
class NotificationStore {
	unread = $state(0);
	latest = $state<Notification | null>(null);
	seq = $state(0);
	private running = false;
	private abort: AbortController | null = null;

	// Idempotent, first caller opens the stream
	ensure() {
		if (this.running || typeof window === 'undefined') return;
		this.running = true;
		void this.refresh();
		void this.loop();
	}

	stop() {
		this.running = false;
		this.abort?.abort();
		this.abort = null;
	}

	async refresh() {
		try {
			const resp = await rpcClient.notification.getUnreadCount({}, silentCallOptions);
			this.unread = Number(resp.unreadCount);
		} catch {
			// Stream or next call corrects it
		}
	}

	// Pages apply the count a mutation returned
	set(count: number | bigint) {
		this.unread = Number(count);
	}

	private async loop() {
		while (this.running) {
			this.abort = new AbortController();
			try {
				const stream = rpcClient.notification.watchNotifications(
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
			if (!this.running) break;
			await new Promise((r) => setTimeout(r, RETRY_MS));
		}
	}

	private apply(ev: NotificationEvent) {
		this.unread = Number(ev.unreadCount);
		if (ev.notification) {
			this.latest = ev.notification;
			this.seq++;
		}
	}
}

export const notificationStore = new NotificationStore();
