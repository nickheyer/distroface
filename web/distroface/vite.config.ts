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
	} catch (err) {
		console.log(err);
	}
	return 'dev';
}

export default defineConfig({
	plugins: [sveltekit()],
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
			},
			'/v2': {
				target: 'http://localhost:8080',
				changeOrigin: true
			},
			'/auth': {
				target: 'http://localhost:8080',
				changeOrigin: true
			},
			'/api': {
				target: 'http://localhost:8080',
				changeOrigin: true
			}
		}
	}
});
