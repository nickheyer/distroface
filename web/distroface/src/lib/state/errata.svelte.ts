let seq = 0;

const FAULT_MS = 10000;
const PLAIN_MS = 5000;
const NAV_GRACE_MS = 1000;

export interface Slip {
	id: number;
	text: string;
	kind: 'fault' | 'plain';
	at: number;
}

// Notice banner pinned under the masthead, replaces toasts
class Errata {
	slips = $state<Slip[]>([]);

	report(text: string) {
		this.push(text, 'fault');
	}

	remark(text: string) {
		this.push(text, 'plain');
	}

	dismiss(id: number) {
		this.slips = this.slips.filter((s) => s.id !== id);
	}

	// Drops stale slips on navigation, keeps ones raised just before it
	sweep() {
		const cutoff = Date.now() - NAV_GRACE_MS;
		this.slips = this.slips.filter((s) => s.at > cutoff);
	}

	private push(text: string, kind: Slip['kind']) {
		const id = ++seq;
		// Collapse duplicate consecutive reports
		const last = this.slips[this.slips.length - 1];
		if (last && last.text === text) return;
		this.slips = [...this.slips.slice(-2), { id, text, kind, at: Date.now() }];
		setTimeout(() => this.dismiss(id), kind === 'plain' ? PLAIN_MS : FAULT_MS);
	}
}

export const errata = new Errata();
