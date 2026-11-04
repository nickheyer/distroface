import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import type { Permission, User } from '$lib/proto/distroface/v1/types_pb';

const SESSION_KEY = 'distroface_session';

// Thirty days, the server validates the token on every use anyway
const COOKIE_MAX_AGE = 60 * 60 * 24 * 30;

// Browser loads of raw files, archives, and avatars carry no bearer header
function writeSessionCookie(token: string | null) {
	if (typeof document === 'undefined') return;
	const secure = location.protocol === 'https:' ? '; Secure' : '';
	if (token) {
		document.cookie = `${SESSION_KEY}=${token}; Path=/; Max-Age=${COOKIE_MAX_AGE}; SameSite=Strict${secure}`;
	} else {
		document.cookie = `${SESSION_KEY}=; Path=/; Max-Age=0; SameSite=Strict${secure}`;
	}
}

function checkPermission(
	permissions: Permission[],
	resource: string,
	action: string,
	objectId?: string
): boolean {
	return permissions.some(
		(p) =>
			(p.resource === resource || p.resource === '*') &&
			(p.action === action || p.action === '*' || p.action === 'manage') &&
			(!objectId || p.objectId === objectId || p.objectId === '*' || p.objectId === '')
	);
}

class AuthStore {
	user = $state<User | null>(null);
	token = $state<string | null>(null);
	permissions = $state<Permission[]>([]);
	loading = $state(true);

	localAuthEnabled = $state(true);
	oidcEnabled = $state(false);
	firstUserSetup = $state(false);
	allowRegistration = $state(false);
	passwordResetEnabled = $state(false);
	anonymousAccessEnabled = $state(false);
	authStatusLoaded = $state(false);

	isAuthenticated = $derived(this.user !== null);
	isAdmin = $derived(
		this.permissions.some((p) => p.resource === '*' && (p.action === '*' || p.action === 'manage'))
	);

	// Admin panel access - can see at least one admin section
	canAccessAdmin = $derived(
		checkPermission(this.permissions, 'settings', 'read') ||
			checkPermission(this.permissions, 'users', 'read') ||
			checkPermission(this.permissions, 'roles', 'read')
	);

	// Settings access (personal)
	canAccessSettings = $derived(this.isAuthenticated);

	// Users
	canReadUsers = $derived(checkPermission(this.permissions, 'users', 'read'));
	canCreateUsers = $derived(checkPermission(this.permissions, 'users', 'create'));
	canUpdateUsers = $derived(checkPermission(this.permissions, 'users', 'update'));
	canDeleteUsers = $derived(checkPermission(this.permissions, 'users', 'delete'));

	// Roles
	canReadRoles = $derived(checkPermission(this.permissions, 'roles', 'read'));
	canCreateRoles = $derived(checkPermission(this.permissions, 'roles', 'create'));
	canUpdateRoles = $derived(checkPermission(this.permissions, 'roles', 'update'));
	canDeleteRoles = $derived(checkPermission(this.permissions, 'roles', 'delete'));

	// Settings (includes invites - both map to settings resource)
	canReadSettings = $derived(checkPermission(this.permissions, 'settings', 'read'));
	canCreateSettings = $derived(checkPermission(this.permissions, 'settings', 'create'));
	canUpdateSettings = $derived(checkPermission(this.permissions, 'settings', 'update'));
	canDeleteSettings = $derived(checkPermission(this.permissions, 'settings', 'delete'));
	// Mirrors backend requireSystemAdmin (settings manage on *)
	canManageSettings = $derived(checkPermission(this.permissions, 'settings', 'manage', '*'));

	// Repositories
	canUpdateRepos = $derived(checkPermission(this.permissions, 'repositories', 'update'));
	canDeleteRepos = $derived(checkPermission(this.permissions, 'repositories', 'delete'));

	// Organizations
	canCreateOrgs = $derived(checkPermission(this.permissions, 'organizations', 'create'));

	// Tokens
	canReadTokens = $derived(checkPermission(this.permissions, 'tokens', 'read'));
	canCreateTokens = $derived(checkPermission(this.permissions, 'tokens', 'create'));
	canDeleteTokens = $derived(checkPermission(this.permissions, 'tokens', 'delete'));

	// Webhooks
	canReadWebhooks = $derived(checkPermission(this.permissions, 'webhooks', 'read'));
	canCreateWebhooks = $derived(checkPermission(this.permissions, 'webhooks', 'create'));

	constructor() {
		this.token = typeof window !== 'undefined' ? localStorage.getItem(SESSION_KEY) : null;
	}

	async checkAuthStatus() {
		try {
			const resp = await rpcClient.auth.getAuthStatus({}, silentCallOptions);
			this.localAuthEnabled = resp.localEnabled;
			this.oidcEnabled = resp.oidcEnabled;
			this.firstUserSetup = resp.firstUserSetup;
			this.allowRegistration = resp.registrationEnabled;
			this.passwordResetEnabled = resp.passwordResetEnabled;
			this.anonymousAccessEnabled = resp.anonymousAccess;
			this.authStatusLoaded = true;
		} catch {
			this.authStatusLoaded = true;
		}
	}

	async validateSession() {
		if (!this.token) {
			this.loading = false;
			return;
		}

		try {
			const resp = await rpcClient.auth.getCurrentUser({}, silentCallOptions);
			this.user = resp.user ?? null;
			this.permissions = resp.permissions;
			writeSessionCookie(this.token);
		} catch {
			this.clearSession();
		} finally {
			this.loading = false;
		}
	}

	async init() {
		await this.checkAuthStatus();
		await this.validateSession();
	}

	setToken(token: string) {
		this.token = token;
		localStorage.setItem(SESSION_KEY, token);
		writeSessionCookie(token);
	}

	async login(identifier: string, password: string) {
		const resp = await rpcClient.auth.login({ identifier, password });
		this.setSession(resp.sessionToken, resp.user ?? null, resp.permissions);
	}

	async register(
		username: string,
		email: string,
		password: string,
		inviteCode?: string,
		invitePin?: string
	) {
		const resp = await rpcClient.auth.register({
			username,
			email,
			password,
			inviteCode,
			invitePin
		});
		this.setSession(resp.sessionToken, resp.user ?? null, resp.permissions);
	}

	async logout() {
		try {
			await rpcClient.auth.logout({}, silentCallOptions);
		} catch {
			// ignore errors during logout
		} finally {
			this.clearSession();
		}
	}

	hasPermission(resource: string, action: string, objectId?: string): boolean {
		return checkPermission(this.permissions, resource, action, objectId);
	}

	private setSession(token: string, user: User | null, permissions: Permission[]) {
		this.token = token;
		this.user = user;
		this.permissions = permissions;
		localStorage.setItem(SESSION_KEY, token);
		writeSessionCookie(token);
	}

	private clearSession() {
		this.token = null;
		this.user = null;
		this.permissions = [];
		localStorage.removeItem(SESSION_KEY);
		writeSessionCookie(null);
	}

	// Swaps in a freshly returned user row, settings pages call it after edits
	applyUser(user: User | undefined) {
		if (user) this.user = user;
	}
}

export const authStore = new AuthStore();
