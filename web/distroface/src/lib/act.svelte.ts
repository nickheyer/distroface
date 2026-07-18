import { ConnectError } from '@connectrpc/connect';

// Human readable message from a failed rpc
export function errText(err: unknown): string {
	if (err instanceof ConnectError) return err.rawMessage || err.message;
	return 'Request failed';
}

// Inline action state, feedback lives at the control instead of toasts
export class Act {
	busy = $state(false);
	error = $state('');
	saved = $state(false);
	#timer: ReturnType<typeof setTimeout> | undefined;

	// True on success, error text is retained until the next run
	async run(fn: () => Promise<unknown>): Promise<boolean> {
		this.busy = true;
		this.error = '';
		this.saved = false;
		try {
			await fn();
			this.saved = true;
			clearTimeout(this.#timer);
			this.#timer = setTimeout(() => (this.saved = false), 2500);
			return true;
		} catch (err) {
			this.error = errText(err);
			return false;
		} finally {
			this.busy = false;
		}
	}

	// Pill label for the field tag slot
	get tag(): string | undefined {
		return this.saved ? 'Saved' : undefined;
	}
}
