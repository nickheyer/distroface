import { rpcClient } from '$lib/api/rpc-client';
import type { User } from '$lib/proto/distroface/v1/types_pb';

const SESSION_KEY = 'distroface_session';

class AuthStore {
	user = $state<User | null>(null);
	token = $state<string | null>(null);
	loading = $state(true);
	isAuthenticated = $derived(this.user !== null);

	constructor() {
		this.token = typeof window !== 'undefined' ? localStorage.getItem(SESSION_KEY) : null;
	}

	async init() {
		if (!this.token) {
			this.loading = false;
			return;
		}

		try {
			const resp = await rpcClient.auth.getCurrentUser({});
			this.user = resp.user ?? null;
		} catch {
			this.clearSession();
		} finally {
			this.loading = false;
		}
	}

	async login(identifier: string, password: string) {
		const resp = await rpcClient.auth.login({ identifier, password });
		this.setSession(resp.sessionToken, resp.user ?? null);
	}

	async register(username: string, email: string, password: string) {
		const resp = await rpcClient.auth.register({ username, email, password });
		this.setSession(resp.sessionToken, resp.user ?? null);
	}

	async logout() {
		try {
			await rpcClient.auth.logout({});
		} catch {
			// ignore errors during logout
		} finally {
			this.clearSession();
		}
	}

	private setSession(token: string, user: User | null) {
		this.token = token;
		this.user = user;
		localStorage.setItem(SESSION_KEY, token);
	}

	private clearSession() {
		this.token = null;
		this.user = null;
		localStorage.removeItem(SESSION_KEY);
	}
}

export const authStore = new AuthStore();
