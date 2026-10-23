// Tracks in flight rpc calls for the global progress bar
class NetworkStore {
	private count = 0;
	// Plain mirror of visible, start and done run inside effects
	private shown = false;
	visible = $state(false);
	private showTimer: ReturnType<typeof setTimeout> | undefined;
	private hideTimer: ReturnType<typeof setTimeout> | undefined;

	start() {
		this.count++;
		clearTimeout(this.hideTimer);
		if (this.count === 1 && !this.shown) {
			clearTimeout(this.showTimer);
			// Fast requests never flash the bar
			this.showTimer = setTimeout(() => {
				if (this.count > 0) this.show(true);
			}, 150);
		}
	}

	done() {
		if (this.count > 0) this.count--;
		if (this.count > 0) return;
		clearTimeout(this.showTimer);
		if (this.shown) {
			// Lingers briefly so the bar never blinks
			this.hideTimer = setTimeout(() => {
				if (this.count === 0) this.show(false);
			}, 250);
		}
	}

	private show(v: boolean) {
		this.shown = v;
		this.visible = v;
	}
}

export const networkStore = new NetworkStore();
