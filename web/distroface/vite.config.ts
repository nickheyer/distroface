import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import { execSync } from 'child_process';

function getVersion() {
	if (process.env.APP_VERSION) {
		return process.env.APP_VERSION;
	}
	try {
		const version = execSync('git describe --tags --always').toString().trim();
		if (version) return version;
	} catch {}
	return 'dev';
}

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	define: {
		__APP_VERSION__: JSON.stringify(getVersion())
	},
	server: {
		proxy: {
			'/distroface.v1': {
				target: 'http://localhost:8080',
				changeOrigin: true
			},
			'/grpc.reflection': {
				target: 'http://localhost:8080',
				changeOrigin: true
			},
			'/connect': {
				target: 'http://localhost:8080',
				changeOrigin: true
			}
		}
	}
});
