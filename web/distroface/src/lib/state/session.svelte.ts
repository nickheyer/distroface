import { rpc, hush, SESSION_KEY } from '$lib/rpc';
import type { Permission, User } from '$lib/proto/distroface/v1/types_pb';

function granted(perms: Permission[], resource: string, action: string, objectId?: string): boolean {
	return perms.some(
		(p) =>
			(p.resource === resource || p.resource === '*') &&
			(p.action === action || p.action === '*' || p.action === 'manage') &&
			(!objectId || p.objectId === objectId || p.objectId === '*' || p.objectId === '')
	);
}

// The signed-in identity and its grants
class Session {
	user = $state<User | null>(null);
	token = $state<string | null>(null);
	permissions = $state<Permission[]>([]);
	ready = $state(false);

	localEnabled = $state(true);
	oidcEnabled = $state(false);
	firstUserSetup = $state(false);
	allowRegistration = $state(false);
	anonymousAccess = $state(false);

	signedIn = $derived(this.user !== null);
	isAdmin = $derived(
		this.permissions.some((p) => p.resource === '*' && (p.action === '*' || p.action === 'manage'))
	);
	canReadUsers = $derived(granted(this.permissions, 'users', 'read'));
	canReadRoles = $derived(granted(this.permissions, 'roles', 'read'));
	canReadSettings = $derived(granted(this.permissions, 'settings', 'read'));
	// Mirrors backend requireSystemAdmin
	canManageSettings = $derived(granted(this.permissions, 'settings', 'manage', '*'));
	canCreateOrgs = $derived(granted(this.permissions, 'organizations', 'create'));
	adminGate = $derived(this.canReadUsers || this.canReadRoles || this.canReadSettings);

	constructor() {
		this.token = typeof window !== 'undefined' ? localStorage.getItem(SESSION_KEY) : null;
	}

	async init() {
		try {
			const s = await rpc.auth.getAuthStatus({}, hush);
			this.localEnabled = s.localEnabled;
			this.oidcEnabled = s.oidcEnabled;
			this.firstUserSetup = s.firstUserSetup;
			this.allowRegistration = s.registrationEnabled;
			this.anonymousAccess = s.anonymousAccess;
		} catch {
			// Status endpoint down, leave defaults
		}
		if (this.token) {
			try {
				const r = await rpc.auth.getCurrentUser({}, hush);
				this.user = r.user ?? null;
				this.permissions = r.permissions;
			} catch {
				this.clear();
			}
		}
		this.ready = true;
	}

	async refresh() {
		const r = await rpc.auth.getCurrentUser({}, hush);
		this.user = r.user ?? null;
		this.permissions = r.permissions;
	}

	can(resource: string, action: string, objectId?: string): boolean {
		return granted(this.permissions, resource, action, objectId);
	}

	async login(identifier: string, password: string) {
		const r = await rpc.auth.login({ identifier, password });
		this.adopt(r.sessionToken, r.user ?? null, r.permissions);
	}

	async register(username: string, email: string, password: string, inviteCode?: string, invitePin?: string) {
		const r = await rpc.auth.register({ username, email, password, inviteCode, invitePin });
		this.adopt(r.sessionToken, r.user ?? null, r.permissions);
	}

	// OIDC hands back a bare token in the URL fragment
	async adoptToken(token: string) {
		this.token = token;
		localStorage.setItem(SESSION_KEY, token);
		const r = await rpc.auth.getCurrentUser({}, hush);
		this.user = r.user ?? null;
		this.permissions = r.permissions;
	}

	async logout() {
		try {
			await rpc.auth.logout({}, hush);
		} catch {
			// Session may already be dead
		}
		this.clear();
	}

	private adopt(token: string, user: User | null, permissions: Permission[]) {
		this.token = token;
		this.user = user;
		this.permissions = permissions;
		localStorage.setItem(SESSION_KEY, token);
	}

	private clear() {
		this.token = null;
		this.user = null;
		this.permissions = [];
		localStorage.removeItem(SESSION_KEY);
	}
}

export const session = new Session();
