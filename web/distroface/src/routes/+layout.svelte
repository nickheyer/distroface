<script lang="ts">
	import '../app.css';
	import { ModeWatcher } from 'mode-watcher';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import type { Pathname } from '$app/types';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Toaster } from '$lib/components/ui/sonner';
	import LoadingBar from '$lib/components/loading-bar.svelte';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuLabel,
		DropdownMenuSeparator,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import {
		Sheet,
		SheetContent,
		SheetHeader,
		SheetTitle,
		SheetDescription
	} from '$lib/components/ui/sheet';
	import {
		Sun,
		Moon,
		LogIn,
		LogOut,
		Package,
		GitBranch,
		Settings,
		Building,
		User,
		Menu,
		Shield,
		Archive,
		ExternalLink,
		BookOpen
	} from '$lib/icons';
	import { toggleMode, mode } from 'mode-watcher';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import CliSetupPrompt from '$lib/components/cli-setup-prompt.svelte';
	import NotificationBell from '$lib/components/git/notification-bell.svelte';
	import RefHovercard from '$lib/components/git/ref-hovercard.svelte';
	import SideNav from '$lib/components/side-nav.svelte';

	let { children } = $props();
	let initialized = $state(false);
	let mobileMenuOpen = $state(false);

	const isLoginPage = $derived(page.url.pathname === '/login');
	// Mailed link pages render bare, no sign in redirect
	const isAuthPage = $derived(
		isLoginPage ||
			/^\/(forgot-password|reset-password|verify-email)(\/|$)/.test(page.url.pathname) ||
			(page.url.pathname === '/settings/notifications' && page.url.searchParams.has('token'))
	);
	const isChangePasswordPage = $derived(page.url.pathname === '/change-password');

	function getUserInitials(user: typeof authStore.user): string {
		if (!user) return '?';
		const name = user.displayName || user.username;
		return name
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	function isActive(path: string): boolean {
		const current = page.url.pathname;
		if (path === '/') return current === '/' || current.startsWith('/images');
		return current.startsWith(path);
	}

	onMount(async () => {
		await authStore.init();
		configStore.init();
		await portalStore.init();
		initialized = true;

		if (!isAuthPage && authStore.firstUserSetup) {
			goto(resolve('/login'));
			return;
		}

		if (
			!isAuthPage &&
			!authStore.isAuthenticated &&
			!authStore.anonymousAccessEnabled &&
			authStore.authStatusLoaded
		) {
			goto(resolve('/login'));
		}
	});

	async function handleLogout() {
		await authStore.logout();
		goto(resolve('/login'));
	}

	type NavLink = { href: Pathname; label: string; icon: typeof Package };

	const navLinks: NavLink[] = $derived([
		{ href: '/', label: 'Images', icon: Package },
		{ href: '/artifacts', label: 'Artifacts', icon: Archive },
		...(configStore.gitEnabled
			? [{ href: '/git', label: 'Git', icon: GitBranch } satisfies NavLink]
			: []),
		...(authStore.isAuthenticated && !portalStore.isPortal
			? [{ href: '/orgs', label: 'Organizations', icon: Building } satisfies NavLink]
			: []),
		...(authStore.canAccessAdmin && !portalStore.isPortal
			? [{ href: '/admin', label: 'Admin', icon: Shield } satisfies NavLink]
			: []),
		{ href: '/docs/api', label: 'API', icon: BookOpen }
	]);

	const brandName = $derived(portalStore.isPortal ? portalStore.displayName : 'Distroface');

	const mobileNavItems = $derived(
		navLinks.map((l) => ({ href: resolve(l.href), label: l.label, icon: l.icon }))
	);

	// Org and instance management live on the primary UI only
	$effect(() => {
		const path = page.url.pathname;
		if (
			initialized &&
			portalStore.isPortal &&
			(path.startsWith('/orgs') || path.startsWith('/admin'))
		) {
			goto(resolve('/'));
		}
	});

	// Pending forced rotation traps the session on the change password page
	$effect(() => {
		if (initialized && authStore.user?.mustChangePassword && !isChangePasswordPage) {
			goto(resolve('/change-password'));
		}
	});
</script>

<svelte:head>
	<title>{brandName}</title>
</svelte:head>

<ModeWatcher />
<LoadingBar />
<Toaster position="bottom-center" expand={true} richColors />
<RefHovercard />

{#if isAuthPage || isChangePasswordPage}
	{@render children?.()}
{:else if !initialized}
	<div class="flex h-screen flex-col items-center justify-center gap-4">
		<img src="/splash-icon.png" alt="Distroface" class="size-12 rounded-xl" />
		<div class="size-4 animate-spin rounded-full border-2 border-primary/25 border-t-primary"></div>
	</div>
{:else}
	<div class="flex min-h-screen flex-col">
		<header
			class="sticky top-0 z-40 border-b bg-background/90 backdrop-blur supports-backdrop-filter:bg-background/75"
		>
			<div class="mx-auto flex h-13 max-w-7xl items-center gap-5 px-4 sm:px-6">
				<a
					href={resolve('/')}
					class="flex shrink-0 items-center gap-2.5 rounded-md transition-opacity outline-none hover:opacity-75 focus-visible:ring-2 focus-visible:ring-ring/50"
				>
					<img src="/Df-dark-dark-grey.png" alt={brandName} class="mr-2 h-7 w-auto dark:hidden" />
					<img src="/Df-light-grey.png" alt={brandName} class="mr-2 hidden h-7 w-auto dark:block" />
					{#if portalStore.isPortal}
						<span class="hidden text-sm font-semibold tracking-tight sm:inline">{brandName}</span>
						<span
							class="rounded-full border px-1.5 py-px text-2xs font-medium text-muted-foreground"
							>Portal</span
						>
					{/if}
				</a>

				<span class="hidden h-5 w-px bg-border md:block" aria-hidden="true"></span>

				<nav class="hidden h-full items-stretch gap-1 md:flex" aria-label="Primary">
					{#each navLinks as link (link.href)}
						{@const active = isActive(link.href)}
						<a
							href={resolve(link.href)}
							aria-current={active ? 'page' : undefined}
							class="group relative flex items-center px-1 text-sm font-medium outline-none {active
								? 'text-foreground'
								: 'text-muted-foreground hover:text-foreground'}"
						>
							<span
								class="flex items-center gap-1.5 rounded-md px-2 py-1 transition-colors group-hover:bg-muted group-focus-visible:ring-2 group-focus-visible:ring-ring/50"
							>
								<link.icon class="size-4 opacity-80" />
								{link.label}
							</span>
							{#if active}
								<span
									class="absolute inset-x-1 -bottom-px h-0.5 rounded-full bg-primary"
									aria-hidden="true"
								></span>
							{/if}
						</a>
					{/each}
				</nav>

				<div class="flex-1"></div>

				<div class="flex items-center gap-1">
					<span class="mr-2 hidden font-mono text-2xs text-muted-foreground lg:inline"
						>{__APP_VERSION__}</span
					>

					{#if portalStore.isPortal && portalStore.primaryOrigin && !portalStore.hidePrimaryLink}
						<!-- eslint-disable svelte/no-navigation-without-resolve -->
						<Button
							variant="ghost-muted"
							size="sm"
							href={portalStore.primaryOrigin}
							title="Open the primary Distroface UI"
						>
							<ExternalLink />
							<span class="hidden sm:inline">Exit portal</span>
						</Button>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					{/if}

					<Button variant="ghost-muted" size="icon" onclick={toggleMode} aria-label="Toggle theme">
						{#if mode.current === 'light'}
							<Moon />
						{:else}
							<Sun />
						{/if}
					</Button>

					{#if authStore.isAuthenticated && configStore.gitEnabled}
						<NotificationBell />
					{/if}

					{#if authStore.isAuthenticated && authStore.user}
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<button
										{...props}
										class="ml-1 flex items-center rounded-full ring-offset-2 ring-offset-background outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
										aria-label="Account menu"
									>
										<Avatar class="size-7 ring-1 ring-border">
											<AvatarFallback>{getUserInitials(authStore.user)}</AvatarFallback>
										</Avatar>
									</button>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" class="w-56">
								<DropdownMenuLabel class="font-normal">
									<div class="flex flex-col">
										<span class="text-sm font-medium text-foreground"
											>{authStore.user.displayName || authStore.user.username}</span
										>
										{#if authStore.user.email}
											<span class="truncate text-xs text-muted-foreground"
												>{authStore.user.email}</span
											>
										{/if}
									</div>
								</DropdownMenuLabel>
								<DropdownMenuSeparator />
								<DropdownMenuItem onclick={() => goto(resolve(`/${authStore.user?.username}`))}>
									<User />
									Profile
								</DropdownMenuItem>
								<DropdownMenuItem onclick={() => goto(resolve('/settings'))}>
									<Settings />
									Settings
								</DropdownMenuItem>
								<DropdownMenuSeparator />
								<DropdownMenuItem onclick={handleLogout}>
									<LogOut />
									Sign out
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					{:else if !authStore.loading}
						<Button
							variant="outline"
							size="sm"
							class="ml-1"
							onclick={() => goto(resolve('/login'))}
						>
							<LogIn />
							Sign in
						</Button>
					{/if}

					<Button
						variant="ghost-muted"
						size="icon"
						class="md:hidden"
						onclick={() => (mobileMenuOpen = true)}
						aria-label="Open navigation"
					>
						<Menu />
					</Button>
				</div>
			</div>
		</header>

		<main class="flex-1">
			{#key page.url.pathname}
				<div class="page-enter mx-auto max-w-7xl px-4 py-6 sm:px-6 sm:py-8">
					{@render children?.()}
				</div>
			{/key}
		</main>
	</div>

	<CliSetupPrompt />

	<Sheet bind:open={mobileMenuOpen}>
		<SheetContent side="right" class="w-72">
			<SheetHeader>
				<SheetTitle>Navigation</SheetTitle>
				<SheetDescription class="sr-only">Navigation menu</SheetDescription>
			</SheetHeader>
			<div class="p-3">
				<SideNav
					items={mobileNavItems}
					isActive={(href) => isActive(href)}
					onNavigate={() => (mobileMenuOpen = false)}
					class="md:w-auto"
				/>
			</div>
		</SheetContent>
	</Sheet>
{/if}
