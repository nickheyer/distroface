// Tracks in flight rpc calls for the global progress bar
class NetworkStore {
	private count = 0;
	visible = $state(false);
	private showTimer: ReturnType<typeof setTimeout> | undefined;
	private hideTimer: ReturnType<typeof setTimeout> | undefined;

	start() {
		this.count++;
		clearTimeout(this.hideTimer);
		if (this.count === 1 && !this.visible) {
			clearTimeout(this.showTimer);
			// Fast requests never flash the bar
			this.showTimer = setTimeout(() => {
				if (this.count > 0) this.visible = true;
			}, 150);
		}
	}

	done() {
		if (this.count > 0) this.count--;
		if (this.count > 0) return;
		clearTimeout(this.showTimer);
		if (this.visible) {
			// Lingers briefly so the bar never blinks
			this.hideTimer = setTimeout(() => {
				if (this.count === 0) this.visible = false;
			}, 250);
		}
	}
}

export const networkStore = new NetworkStore();
