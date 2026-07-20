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
		Settings,
		Building2,
		User,
		Menu,
		Shield,
		Archive,
		ExternalLink,
		BookOpen
	} from '@lucide/svelte';
	import { toggleMode, mode } from 'mode-watcher';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';

	let { children } = $props();
	let initialized = $state(false);
	let mobileMenuOpen = $state(false);

	const isLoginPage = $derived(page.url.pathname === '/login');
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
		if (path === '/') return page.url.pathname === '/';
		return page.url.pathname.startsWith(path);
	}

	onMount(async () => {
		await authStore.init();
		configStore.init();
		await portalStore.init();
		initialized = true;

		if (!isLoginPage && authStore.firstUserSetup) {
			goto(resolve('/login'));
			return;
		}

		if (
			!isLoginPage &&
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
		...(authStore.isAuthenticated && !portalStore.isPortal
			? [{ href: '/orgs', label: 'Organizations', icon: Building2 } satisfies NavLink]
			: []),
		...(authStore.canAccessAdmin && !portalStore.isPortal
			? [{ href: '/admin', label: 'Admin', icon: Shield } satisfies NavLink]
			: []),
		{ href: '/docs/api', label: 'API', icon: BookOpen }
	]);

	const brandName = $derived(portalStore.isPortal ? portalStore.displayName : 'Distroface');

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

{#if isLoginPage || isChangePasswordPage}
	{@render children?.()}
{:else if !initialized}
	<div class="flex h-screen flex-col items-center justify-center gap-3">
		<img src="/splash-icon.png" alt="Distroface" class="h-14 w-14 rounded-2xl" />
		<div class="h-5 w-5 border-2 border-primary/30 border-t-primary rounded-full animate-spin"></div>
	</div>
{:else}
	<div class="min-h-screen flex flex-col">
		<header class="sticky top-0 z-50 border-b border-border/50 bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/80">
			<div class="mx-auto flex h-14 max-w-7xl items-center gap-6 px-4 sm:px-6">
				<a href={resolve('/')} class="flex items-center gap-2.5 shrink-0">
					<img src="/adaptive-icon.png" alt={brandName} class="h-7 w-7 rounded-lg" />
					<span class="font-bold text-lg tracking-tight hidden sm:inline">{brandName}</span>
					{#if portalStore.isPortal}
						<span class="rounded-full border border-border px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Portal</span>
					{/if}
				</a>

				<nav class="hidden md:flex items-center gap-0.5">
					{#each navLinks as link (link.href)}
						<a
							href={resolve(link.href)}
							class="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors {isActive(link.href)
								? 'bg-accent text-accent-foreground'
								: 'text-muted-foreground hover:text-foreground hover:bg-accent/50'}"
						>
							<link.icon class="h-4 w-4" />
							{link.label}
						</a>
					{/each}
				</nav>

				<div class="flex-1"></div>

				<div class="flex items-center gap-1.5">
					<span class="text-muted-foreground/60 text-[11px] hidden lg:inline mr-2">{__APP_VERSION__}</span>

					{#if portalStore.isPortal && portalStore.primaryOrigin && !portalStore.hidePrimaryLink}
						<!-- eslint-disable svelte/no-navigation-without-resolve -->
						<a
							href={portalStore.primaryOrigin}
							class="flex items-center gap-1.5 h-8 px-2.5 rounded-md text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
							title="Open the primary Distroface UI"
						>
							<ExternalLink class="h-3.5 w-3.5" />
							<span class="hidden sm:inline">Exit portal</span>
						</a>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					{/if}

					<Button variant="ghost" size="icon" class="h-8 w-8" onclick={toggleMode}>
						{#if mode.current === 'light'}
							<Moon class="h-4 w-4 text-muted-foreground" />
						{:else}
							<Sun class="h-4 w-4 text-muted-foreground" />
						{/if}
					</Button>

					{#if authStore.isAuthenticated && authStore.user}
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<button
										{...props}
										class="flex items-center gap-2 rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-ring ml-1"
									>
										<Avatar class="h-8 w-8">
											<AvatarFallback class="text-xs bg-primary/10 text-primary font-medium">
												{getUserInitials(authStore.user)}
											</AvatarFallback>
										</Avatar>
									</button>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" class="w-56">
								<DropdownMenuLabel>
									<div class="flex flex-col">
										<span class="font-medium">{authStore.user.displayName || authStore.user.username}</span>
										{#if authStore.user.email}
											<span class="text-xs font-normal text-muted-foreground">{authStore.user.email}</span>
										{/if}
									</div>
								</DropdownMenuLabel>
								<DropdownMenuSeparator />
								<DropdownMenuItem onclick={() => goto(resolve(`/${authStore.user?.username}`))}>
									<User class="h-4 w-4 mr-2" />
									Profile
								</DropdownMenuItem>
								<DropdownMenuItem onclick={() => goto(resolve('/settings'))}>
									<Settings class="h-4 w-4 mr-2" />
									Settings
								</DropdownMenuItem>
								<DropdownMenuSeparator />
								<DropdownMenuItem onclick={handleLogout}>
									<LogOut class="h-4 w-4 mr-2" />
									Sign out
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					{:else if !authStore.loading}
						<Button variant="outline" size="sm" class="ml-1" onclick={() => goto(resolve('/login'))}>
							<LogIn class="h-4 w-4 mr-1.5" />
							Sign in
						</Button>
					{/if}

					<Button
						variant="ghost"
						size="icon"
						class="h-8 w-8 md:hidden"
						onclick={() => (mobileMenuOpen = true)}
					>
						<Menu class="h-4 w-4" />
					</Button>
				</div>
			</div>
		</header>

		<main class="flex-1">
			{#key page.url.pathname}
				<div class="mx-auto max-w-7xl px-4 sm:px-6 py-6 page-enter">
					{@render children?.()}
				</div>
			{/key}
		</main>
	</div>

	<Sheet bind:open={mobileMenuOpen}>
		<SheetContent side="right" class="w-72">
			<SheetHeader>
				<SheetTitle>Navigation</SheetTitle>
				<SheetDescription class="sr-only">Navigation menu</SheetDescription>
			</SheetHeader>
			<nav class="flex flex-col gap-1 mt-4">
				{#each navLinks as link (link.href)}
					<a
						href={resolve(link.href)}
						class="flex items-center gap-2.5 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors {isActive(link.href)
							? 'bg-accent text-accent-foreground'
							: 'text-muted-foreground hover:text-foreground hover:bg-accent/50'}"
						onclick={() => (mobileMenuOpen = false)}
					>
						<link.icon class="h-4 w-4" />
						{link.label}
					</a>
				{/each}
			</nav>
		</SheetContent>
	</Sheet>
{/if}
