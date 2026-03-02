<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Separator } from '$lib/components/ui/separator';
	import { Label } from '$lib/components/ui/label';
	import { authStore } from '$lib/stores/auth.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Shield, Globe, ArrowRight, Check, Loader2 } from '@lucide/svelte';
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
		authStore.firstUserSetup ||
			authStore.allowRegistration ||
			(inviteValid && inviteChecked)
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
		const token = urlParams.get('token');
		if (token) {
			processingOidc = true;
			authStore.setToken(token);
			try {
				await authStore.validateSession();
				if (authStore.isAuthenticated) {
					toast.success('Signed in successfully');
					goto('/');
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
			goto('/');
		} catch {
			// error interceptor handles the toast
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
				authStore.firstUserSetup
					? 'Admin account created'
					: 'Account created successfully'
			);
			goto('/');
		} catch {
			// error interceptor handles the toast
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

<div class="flex min-h-screen items-center justify-center p-4 bg-muted/30">
	<div class="w-full max-w-105">
		<!-- Logo & heading -->
		<div class="text-center mb-8">
			<img
				src="/adaptive-icon.png"
				alt="Distroface"
				class="mx-auto h-12 w-12 rounded-xl mb-5"
			/>
			{#if processingOidc}
				<h1 class="text-xl font-semibold tracking-tight">Signing you in</h1>
				<p class="text-sm text-muted-foreground mt-1.5">Completing authentication...</p>
			{:else}
				<h1 class="text-xl font-semibold tracking-tight">{heading}</h1>
				<p class="text-sm text-muted-foreground mt-1.5">{subheading}</p>
			{/if}
		</div>

		{#if processingOidc}
			<!-- OIDC processing state -->
			<Card class="border-border/60">
				<CardContent class="flex flex-col items-center justify-center py-12">
					<Loader2 class="h-6 w-6 text-primary animate-spin mb-3" />
					<p class="text-sm text-muted-foreground">Please wait...</p>
				</CardContent>
			</Card>
		{:else}
			<Card class="border-border/60">
				<CardContent class="p-6">
					<!-- SSO button (shown at top when available alongside local auth) -->
					{#if oidcAvailable}
						<Button
							variant={oidcOnly ? 'default' : 'outline'}
							class="w-full h-10"
							onclick={handleOidcLogin}
							disabled={processingOidc}
						>
							<Globe class="h-4 w-4 mr-2" />
							Continue with SSO
						</Button>

						{#if !oidcOnly}
							<div class="relative my-5">
								<Separator />
								<span class="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-3 text-xs text-muted-foreground">
									or
								</span>
							</div>
						{/if}
					{/if}

					<!-- First-user setup -->
					{#if authStore.firstUserSetup}
						<div class="flex items-start gap-2.5 p-3 rounded-lg bg-primary/8 border border-primary/15 mb-5">
							<Shield class="h-4 w-4 text-primary mt-0.5 shrink-0" />
							<div class="text-sm">
								<p class="font-medium text-primary">Administrator account</p>
								<p class="text-muted-foreground mt-0.5 text-[13px] leading-snug">This account will have full system access.</p>
							</div>
						</div>
						{@render registerFormContent()}

					<!-- Login view -->
					{:else if view === 'login' && canShowLogin}
						<form onsubmit={handleLogin} class="space-y-4" novalidate>
							<div class="space-y-1.5">
								<Label for="login-id" class="text-sm font-medium">Username or email</Label>
								<Input
									id="login-id"
									type="text"
									placeholder="Enter your username or email"
									autocomplete="username"
									bind:value={identifier}
									aria-invalid={loginTouched.identifier && !!loginErrors.identifier}
									onblur={() => { loginTouched.identifier = true; validateLogin(); }}
								/>
								{#if loginTouched.identifier && loginErrors.identifier}
									<p class="text-[13px] text-destructive">{loginErrors.identifier}</p>
								{/if}
							</div>

							<div class="space-y-1.5">
								<Label for="login-pw" class="text-sm font-medium">Password</Label>
								<PasswordInput
									id="login-pw"
									placeholder="Enter your password"
									autocomplete="current-password"
									bind:value={password}
									error={loginTouched.password && !!loginErrors.password}
									onblur={() => { loginTouched.password = true; validateLogin(); }}
								/>
								{#if loginTouched.password && loginErrors.password}
									<p class="text-[13px] text-destructive">{loginErrors.password}</p>
								{/if}
							</div>

							<Button type="submit" class="w-full h-10 mt-1" disabled={isSubmitting}>
								{#if isSubmitting}
									<Loader2 class="h-4 w-4 mr-2 animate-spin" />
									Signing in...
								{:else}
									Sign in
									<ArrowRight class="h-4 w-4 ml-1.5" />
								{/if}
							</Button>
						</form>

					<!-- Register view -->
					{:else if view === 'register' && canShowRegister}
						{@render registerFormContent()}

					<!-- Fallback: nothing available locally, no OIDC -->
					{:else if !oidcOnly}
						<div class="text-center py-6 text-sm text-muted-foreground">
							<p>Registration is currently closed.</p>
							<p class="mt-1">Contact an administrator for access.</p>
						</div>
					{/if}
				</CardContent>
			</Card>

			<!-- View toggle & guest access -->
			<div class="mt-4 space-y-2">
				{#if canShowLogin && canShowRegister && !authStore.firstUserSetup}
					<p class="text-center text-sm text-muted-foreground">
						{#if view === 'login'}
							Don't have an account?
							<button
								class="font-medium text-primary hover:text-primary/80 transition-colors"
								onclick={() => switchView('register')}
							>
								Create one
							</button>
						{:else}
							Already have an account?
							<button
								class="font-medium text-primary hover:text-primary/80 transition-colors"
								onclick={() => switchView('login')}
							>
								Sign in
							</button>
						{/if}
					</p>
				{/if}

				{#if authStore.anonymousAccessEnabled && !authStore.firstUserSetup}
					<p class="text-center text-sm">
						<button
							class="text-muted-foreground hover:text-foreground transition-colors"
							onclick={() => goto('/')}
						>
							Continue as guest
						</button>
					</p>
				{/if}
			</div>
		{/if}

		<p class="text-center text-[11px] text-muted-foreground/50 mt-6">
			Distroface &middot; Container Image Registry
		</p>
	</div>
</div>

{#snippet registerFormContent()}
	<form onsubmit={handleRegister} class="space-y-4" novalidate>
		{#if inviteValid && inviteRequiresPin}
			<div class="space-y-1.5">
				<Label for="reg-pin" class="text-sm font-medium">
					Invite PIN
					<span class="text-destructive ml-0.5">*</span>
				</Label>
				<Input
					id="reg-pin"
					type="text"
					inputmode="numeric"
					placeholder="Enter your invite PIN"
					bind:value={invitePin}
					aria-invalid={regTouched.pin && !!regErrors.pin}
					onblur={() => { regTouched.pin = true; validateRegistration(); }}
				/>
				{#if regTouched.pin && regErrors.pin}
					<p class="text-[13px] text-destructive">{regErrors.pin}</p>
				{/if}
			</div>
		{/if}

		<div class="space-y-1.5">
			<Label for="reg-user" class="text-sm font-medium">
				Username
				<span class="text-destructive ml-0.5">*</span>
			</Label>
			<Input
				id="reg-user"
				type="text"
				placeholder="Choose a username"
				autocomplete="username"
				bind:value={regUsername}
				aria-invalid={regTouched.username && !!regErrors.username}
				onblur={() => { regTouched.username = true; validateRegistration(); }}
			/>
			{#if regTouched.username && regErrors.username}
				<p class="text-[13px] text-destructive">{regErrors.username}</p>
			{:else}
				<p class="text-[12px] text-muted-foreground">Lowercase letters, numbers, hyphens, dots. 3-40 characters.</p>
			{/if}
		</div>

		<div class="space-y-1.5">
			<Label for="reg-email" class="text-sm font-medium">
				Email
				<span class="text-destructive ml-0.5">*</span>
			</Label>
			<Input
				id="reg-email"
				type="email"
				placeholder="you@example.com"
				autocomplete="email"
				bind:value={regEmail}
				aria-invalid={regTouched.email && !!regErrors.email}
				onblur={() => { regTouched.email = true; validateRegistration(); }}
			/>
			{#if regTouched.email && regErrors.email}
				<p class="text-[13px] text-destructive">{regErrors.email}</p>
			{/if}
		</div>

		<div class="space-y-1.5">
			<Label for="reg-pw" class="text-sm font-medium">
				Password
				<span class="text-destructive ml-0.5">*</span>
			</Label>
			<PasswordInput
				id="reg-pw"
				placeholder="Create a password"
				autocomplete="new-password"
				bind:value={regPassword}
				error={regTouched.password && !!regErrors.password}
				onblur={() => { regTouched.password = true; validateRegistration(); }}
			/>
			{#if regTouched.password && regErrors.password}
				<p class="text-[13px] text-destructive">{regErrors.password}</p>
			{:else}
				<PasswordStrength password={regPassword} />
			{/if}
		</div>

		<div class="space-y-1.5">
			<Label for="reg-confirm" class="text-sm font-medium">
				Confirm password
				<span class="text-destructive ml-0.5">*</span>
			</Label>
			<PasswordInput
				id="reg-confirm"
				placeholder="Confirm your password"
				autocomplete="new-password"
				bind:value={regConfirmPassword}
				error={regTouched.confirm && !!regErrors.confirm}
				onblur={() => { regTouched.confirm = true; validateRegistration(); }}
			/>
			{#if regTouched.confirm && regErrors.confirm}
				<p class="text-[13px] text-destructive">{regErrors.confirm}</p>
			{:else if confirmMatch}
				<p class="text-[13px] text-success flex items-center gap-1">
					<Check class="h-3 w-3" />
					Passwords match
				</p>
			{/if}
		</div>

		<Button type="submit" class="w-full h-10 mt-1" disabled={isSubmitting}>
			{#if isSubmitting}
				<Loader2 class="h-4 w-4 mr-2 animate-spin" />
				Creating account...
			{:else if authStore.firstUserSetup}
				Create admin account
				<ArrowRight class="h-4 w-4 ml-1.5" />
			{:else}
				Create account
				<ArrowRight class="h-4 w-4 ml-1.5" />
			{/if}
		</Button>
	</form>
{/snippet}
