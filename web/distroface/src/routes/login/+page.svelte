<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import FormField from '$lib/components/form-field.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Shield, Globe, ArrowRight, Loader2 } from '@lucide/svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import PasswordStrength from '$lib/components/password-strength.svelte';

	type View = 'login' | 'register';

	let view = $state<View>('login');
	let identifier = $state('');
	let password = $state('');
	let regUsername = $state('');
	let regEmail = $state('');
	let regPassword = $state('');
	let regConfirmPassword = $state('');
	let isSubmitting = $state(false);
	let processingOidc = $state(false);
	let loginErrors = $state<Record<string, string>>({});
	let regErrors = $state<Record<string, string>>({});
	let loginTouched = $state<Record<string, boolean>>({});
	let regTouched = $state<Record<string, boolean>>({});

	let inviteCode = $state<string | null>(null);
	let invitePin = $state('');
	let inviteValid = $state(false);
	let inviteRequiresPin = $state(false);
	let inviteChecked = $state(false);

	const canShowLogin = $derived(authStore.localAuthEnabled && !authStore.firstUserSetup);
	const canShowRegister = $derived(
		authStore.firstUserSetup || authStore.allowRegistration || (inviteValid && inviteChecked)
	);
	const oidcAvailable = $derived(authStore.oidcEnabled && !authStore.firstUserSetup);
	const oidcOnly = $derived(oidcAvailable && !canShowLogin && !canShowRegister);

	const confirmMatch = $derived(
		regConfirmPassword.length > 0 && regPassword === regConfirmPassword
	);

	// Page title / subtitle
	const heading = $derived(
		authStore.firstUserSetup
			? 'Set up Distroface'
			: view === 'register'
				? 'Create an account'
				: 'Welcome back'
	);
	const subheading = $derived(
		authStore.firstUserSetup
			? 'Create the administrator account to get started.'
			: inviteValid
				? "You've been invited to join. Create your account below."
				: view === 'register'
					? 'Enter your details to create your account.'
					: oidcOnly
						? 'Sign in with your identity provider to continue.'
						: 'Sign in to your account to continue.'
	);

	onMount(async () => {
		const urlParams = new URLSearchParams(page.url.search);

		// Handle OIDC callback token
		const hashParams = new URLSearchParams(window.location.hash.slice(1));
		const token = hashParams.get('token') ?? urlParams.get('token');
		if (token) {
			// Scrub token from address bar right away
			history.replaceState(null, '', window.location.pathname);
			processingOidc = true;
			authStore.setToken(token);
			try {
				await authStore.validateSession();
				if (authStore.isAuthenticated) {
					toast.success('Signed in successfully');
					goto(resolve('/'));
					return;
				}
			} catch {
				toast.error('SSO authentication failed');
			} finally {
				processingOidc = false;
			}
		}

		// Handle invite code
		const invite = urlParams.get('invite');
		if (invite) {
			inviteCode = invite;
			try {
				const resp = await rpcClient.auth.validateInvite({ code: invite });
				inviteValid = resp.valid;
				inviteRequiresPin = resp.requiresPin;
				if (resp.valid) {
					view = 'register';
				} else {
					toast.error('This invite link is invalid or has expired.');
				}
			} catch {
				toast.error('Unable to validate invite code.');
			} finally {
				inviteChecked = true;
			}
		}

		// Set initial view based on available auth methods
		if (authStore.firstUserSetup || (!canShowLogin && canShowRegister)) {
			view = 'register';
		}
	});

	function validateLogin(): boolean {
		loginErrors = {};
		if (!identifier.trim()) loginErrors.identifier = 'Username or email is required.';
		if (!password) loginErrors.password = 'Password is required.';
		return Object.keys(loginErrors).length === 0;
	}

	function validateRegistration(): boolean {
		regErrors = {};
		if (!regUsername.trim()) {
			regErrors.username = 'Username is required.';
		} else if (regUsername.length < 3) {
			regErrors.username = 'Must be at least 3 characters.';
		} else if (!/^[a-z0-9][a-z0-9._-]*[a-z0-9]$/.test(regUsername) && regUsername.length > 1) {
			regErrors.username = 'Lowercase letters, numbers, hyphens, and dots only.';
		}
		if (!regEmail.trim()) {
			regErrors.email = 'Email is required.';
		} else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(regEmail)) {
			regErrors.email = 'Enter a valid email address.';
		}
		if (!regPassword) {
			regErrors.password = 'Password is required.';
		} else if (regPassword.length < 8) {
			regErrors.password = 'Must be at least 8 characters.';
		}
		if (!regConfirmPassword) {
			regErrors.confirm = 'Please confirm your password.';
		} else if (regPassword !== regConfirmPassword) {
			regErrors.confirm = 'Passwords do not match.';
		}
		if (inviteRequiresPin && !invitePin.trim()) {
			regErrors.pin = 'Invite PIN is required.';
		}
		return Object.keys(regErrors).length === 0;
	}

	async function handleLogin(e: Event) {
		e.preventDefault();
		loginTouched = { identifier: true, password: true };
		if (!validateLogin()) return;

		isSubmitting = true;
		try {
			await authStore.login(identifier, password);
			toast.success('Signed in successfully');
			goto(resolve('/'));
		} catch {
			// Error interceptor handles the toast
		} finally {
			isSubmitting = false;
		}
	}

	async function handleRegister(e: Event) {
		e.preventDefault();
		regTouched = { username: true, email: true, password: true, confirm: true, pin: true };
		if (!validateRegistration()) return;

		isSubmitting = true;
		try {
			await authStore.register(
				regUsername,
				regEmail,
				regPassword,
				inviteCode ?? undefined,
				inviteRequiresPin ? invitePin : undefined
			);
			toast.success(
				authStore.firstUserSetup ? 'Admin account created' : 'Account created successfully'
			);
			goto(resolve('/'));
		} catch {
			// Error interceptor handles the toast
		} finally {
			isSubmitting = false;
		}
	}

	async function handleOidcLogin() {
		processingOidc = true;
		try {
			const resp = await rpcClient.auth.getOIDCLoginURL({});
			window.location.href = resp.redirectUrl;
		} catch {
			processingOidc = false;
		}
	}

	function switchView(target: View) {
		view = target;
		loginErrors = {};
		regErrors = {};
		loginTouched = {};
		regTouched = {};
	}
</script>

<div class="flex min-h-screen items-center justify-center bg-muted/40 p-4">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center">
			{#if portalStore.isPortal}
				<div class="mb-5 inline-flex items-center gap-3">
					<img src="/adaptive-icon.png" alt={portalStore.displayName} class="size-9 rounded-lg" />
					<div class="h-8 w-px bg-border"></div>
					<div class="text-left leading-tight">
						<div class="flex items-center gap-2">
							<span class="text-sm font-semibold tracking-tight">{portalStore.displayName}</span>
							<span
								class="rounded-full border px-1.5 py-px text-2xs font-medium text-muted-foreground"
								>Portal</span
							>
						</div>
						<p class="mt-0.5 text-xs text-muted-foreground">Managed by {portalStore.displayName}</p>
					</div>
				</div>
			{:else}
				<img src="/adaptive-icon.png" alt="Distroface" class="mx-auto mb-5 size-10 rounded-lg" />
			{/if}
			{#if processingOidc}
				<h1 class="text-xl font-semibold tracking-tight">Signing you in</h1>
				<p class="mt-1 text-sm text-muted-foreground">Completing authentication</p>
			{:else}
				<h1 class="text-xl font-semibold tracking-tight">{heading}</h1>
				<p class="mt-1 text-sm text-muted-foreground">{subheading}</p>
			{/if}
		</div>

		<div class="rounded-lg border bg-card p-6 shadow-sm">
			{#if processingOidc}
				<div class="flex flex-col items-center justify-center py-10">
					<Loader2 class="mb-3 size-5 animate-spin text-muted-foreground" />
					<p class="text-sm text-muted-foreground">Please wait</p>
				</div>
			{:else}
				{#if oidcAvailable}
					<Button
						variant={oidcOnly ? 'default' : 'outline'}
						size="lg"
						class="w-full"
						onclick={handleOidcLogin}
						disabled={processingOidc}
					>
						<Globe />
						Continue with SSO
					</Button>
					{#if !oidcOnly}
						<div class="relative my-5">
							<div class="h-px w-full bg-border"></div>
							<span
								class="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground"
								>or</span
							>
						</div>
					{/if}
				{/if}

				{#if authStore.firstUserSetup}
					<div
						class="mb-5 flex items-start gap-2.5 rounded-md border border-primary/20 bg-primary/5 px-3 py-2.5"
					>
						<Shield class="mt-0.5 size-4 shrink-0 text-primary" />
						<div class="text-sm">
							<p class="font-medium">Administrator account</p>
							<p class="mt-0.5 text-xs text-muted-foreground">
								This account will have full system access.
							</p>
						</div>
					</div>
					{@render registerFormContent()}
				{:else if view === 'login' && canShowLogin}
					<form onsubmit={handleLogin} class="space-y-4" novalidate>
						<FormField
							label="Username or email"
							id="login-id"
							error={loginTouched.identifier ? loginErrors.identifier : ''}
						>
							<Input
								id="login-id"
								type="text"
								placeholder="Username or email"
								autocomplete="username"
								bind:value={identifier}
								aria-invalid={loginTouched.identifier && !!loginErrors.identifier}
								onblur={() => {
									loginTouched.identifier = true;
									validateLogin();
								}}
							/>
						</FormField>
						<div class="space-y-1.5">
							<div class="flex items-center justify-between">
								<label for="login-pw" class="text-sm leading-5 font-medium">Password</label>
								{#if authStore.passwordResetEnabled}
									<a
										href={resolve('/forgot-password')}
										class="text-xs text-muted-foreground transition-colors hover:text-primary"
									>
										Forgot password?
									</a>
								{/if}
							</div>
							<PasswordInput
								id="login-pw"
								placeholder="Password"
								autocomplete="current-password"
								bind:value={password}
								error={loginTouched.password && !!loginErrors.password}
								onblur={() => {
									loginTouched.password = true;
									validateLogin();
								}}
							/>
							{#if loginTouched.password && loginErrors.password}
								<p class="text-xs text-destructive">{loginErrors.password}</p>
							{/if}
						</div>
						<Button type="submit" size="lg" class="mt-2 w-full" disabled={isSubmitting}>
							{#if isSubmitting}
								<Loader2 class="animate-spin" />
								Signing in
							{:else}
								Sign in
								<ArrowRight />
							{/if}
						</Button>
					</form>
				{:else if view === 'register' && canShowRegister}
					{@render registerFormContent()}
				{:else if !oidcOnly}
					<div class="py-6 text-center text-sm text-muted-foreground">
						<p>Registration is currently closed.</p>
						<p class="mt-1">Contact an administrator for access.</p>
					</div>
				{/if}
			{/if}
		</div>

		<div class="mt-4 space-y-2 text-center text-sm">
			{#if canShowLogin && canShowRegister && !authStore.firstUserSetup}
				<p class="text-muted-foreground">
					{#if view === 'login'}
						Don't have an account?
						<button
							class="font-medium text-primary transition-colors hover:underline"
							onclick={() => switchView('register')}>Create one</button
						>
					{:else}
						Already have an account?
						<button
							class="font-medium text-primary transition-colors hover:underline"
							onclick={() => switchView('login')}>Sign in</button
						>
					{/if}
				</p>
			{/if}
			{#if authStore.anonymousAccessEnabled && !authStore.firstUserSetup}
				<p>
					<button
						class="text-muted-foreground transition-colors hover:text-foreground"
						onclick={() => goto(resolve('/'))}
					>
						Continue as guest
					</button>
				</p>
			{/if}
		</div>

		<p class="mt-8 text-center text-2xs text-muted-foreground/70">
			{#if portalStore.isPortal}
				Portal managed by {portalStore.displayName} &middot; Powered by Distroface
			{:else}
				Distroface &middot; Container image registry
			{/if}
		</p>
	</div>
</div>

{#snippet registerFormContent()}
	<form onsubmit={handleRegister} class="space-y-4" novalidate>
		{#if inviteValid && inviteRequiresPin}
			<FormField
				label="Invite PIN"
				id="reg-pin"
				required
				error={regTouched.pin ? regErrors.pin : ''}
			>
				<Input
					id="reg-pin"
					type="text"
					inputmode="numeric"
					placeholder="Invite PIN"
					bind:value={invitePin}
					aria-invalid={regTouched.pin && !!regErrors.pin}
					onblur={() => {
						regTouched.pin = true;
						validateRegistration();
					}}
				/>
			</FormField>
		{/if}
		<FormField
			label="Username"
			id="reg-user"
			required
			help="Lowercase letters, numbers, hyphens, dots. 3 to 40 characters."
			error={regTouched.username ? regErrors.username : ''}
		>
			<Input
				id="reg-user"
				type="text"
				placeholder="Choose a username"
				autocomplete="username"
				bind:value={regUsername}
				aria-invalid={regTouched.username && !!regErrors.username}
				onblur={() => {
					regTouched.username = true;
					validateRegistration();
				}}
			/>
		</FormField>
		<FormField
			label="Email"
			id="reg-email"
			required
			error={regTouched.email ? regErrors.email : ''}
		>
			<Input
				id="reg-email"
				type="email"
				placeholder="you@example.com"
				autocomplete="email"
				bind:value={regEmail}
				aria-invalid={regTouched.email && !!regErrors.email}
				onblur={() => {
					regTouched.email = true;
					validateRegistration();
				}}
			/>
		</FormField>
		<FormField
			label="Password"
			id="reg-pw"
			required
			error={regTouched.password ? regErrors.password : ''}
		>
			<PasswordInput
				id="reg-pw"
				placeholder="Create a password"
				autocomplete="new-password"
				bind:value={regPassword}
				error={regTouched.password && !!regErrors.password}
				onblur={() => {
					regTouched.password = true;
					validateRegistration();
				}}
			/>
			{#if !(regTouched.password && regErrors.password)}
				<PasswordStrength password={regPassword} />
			{/if}
		</FormField>
		<FormField
			label="Confirm password"
			id="reg-confirm"
			required
			error={regTouched.confirm ? regErrors.confirm : ''}
			help={!regTouched.confirm || !regErrors.confirm
				? confirmMatch
					? 'Passwords match'
					: ''
				: ''}
		>
			<PasswordInput
				id="reg-confirm"
				placeholder="Confirm your password"
				autocomplete="new-password"
				bind:value={regConfirmPassword}
				error={regTouched.confirm && !!regErrors.confirm}
				onblur={() => {
					regTouched.confirm = true;
					validateRegistration();
				}}
			/>
		</FormField>
		<Button type="submit" size="lg" class="mt-2 w-full" disabled={isSubmitting}>
			{#if isSubmitting}
				<Loader2 class="animate-spin" />
				Creating account
			{:else if authStore.firstUserSetup}
				Create admin account
				<ArrowRight />
			{:else}
				Create account
				<ArrowRight />
			{/if}
		</Button>
	</form>
{/snippet}
