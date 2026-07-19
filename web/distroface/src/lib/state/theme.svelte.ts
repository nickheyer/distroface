const KEY = 'distroface_theme';

// Document theme, seeded before paint by app.html
class Theme {
	mode = $state<'light' | 'dark'>('light');

	init() {
		const t = document.documentElement.dataset.theme;
		this.mode = t === 'dark' ? 'dark' : 'light';
		// Follow the system until the reader has chosen
		const followsSystem = !localStorage.getItem(KEY);
		if (followsSystem) {
			const media = window.matchMedia('(prefers-color-scheme: dark)');
			media.addEventListener('change', (e) => {
				if (!localStorage.getItem(KEY)) this.apply(e.matches ? 'dark' : 'light', false);
			});
		}
	}

	toggle() {
		this.apply(this.mode === 'dark' ? 'light' : 'dark', true);
	}

	private apply(mode: 'light' | 'dark', persist: boolean) {
		this.mode = mode;
		document.documentElement.dataset.theme = mode;
		if (persist) localStorage.setItem(KEY, mode);
	}
}

export const theme = new Theme();
