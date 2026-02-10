<script lang="ts">
	import '../app.css';
	import { ModeWatcher } from 'mode-watcher';
	import { page } from '$app/state';
	import {
		SidebarProvider,
		SidebarInset,
		Sidebar,
		SidebarContent,
		SidebarGroup,
		SidebarGroupLabel,
		SidebarGroupContent,
		SidebarMenu,
		SidebarMenuItem,
		SidebarMenuButton,
		SidebarHeader,
		SidebarFooter,
		SidebarTrigger
	} from '$lib/components/ui/sidebar';
	import { Separator } from '$lib/components/ui/separator';
	import { Button } from '$lib/components/ui/button';
	import { Toaster } from '$lib/components/ui/sonner';

	import { Home, Settings, Sun, Moon } from '@lucide/svelte';
	import { toggleMode, mode } from 'mode-watcher';

	let { children } = $props();
</script>

<svelte:head>
	<title>Distroface</title>
</svelte:head>

<ModeWatcher />
<Toaster position="bottom-center" expand={true} richColors />

<div>
	<SidebarProvider>
		<Sidebar collapsible="icon">
			<SidebarHeader class="my-2">
				<div class="m-auto flex items-center gap-2">
					<span class="text-lg font-bold group-data-[collapsible=icon]:hidden">Distroface</span>
				</div>
			</SidebarHeader>

			<SidebarContent>
				<SidebarGroup>
					<SidebarGroupLabel class="group-data-[collapsible=icon]:opacity-0">Navigation</SidebarGroupLabel>
					<SidebarGroupContent>
						<SidebarMenu>
							<SidebarMenuItem>
								<SidebarMenuButton isActive={page.url.pathname === '/'}>
									{#snippet child({ props })}
										<a href="/" {...props}>
											<Home class="h-4 w-4" />
											<span class="group-data-[collapsible=icon]:hidden">Home</span>
										</a>
									{/snippet}
								</SidebarMenuButton>
							</SidebarMenuItem>
						</SidebarMenu>
					</SidebarGroupContent>
				</SidebarGroup>
			</SidebarContent>

			<SidebarFooter>
				<Separator orientation="horizontal" class="mb-2" />
				<div class="ml-auto flex items-center gap-2">
					<span class="text-muted-foreground text-xs group-data-[collapsible=icon]:hidden">{__APP_VERSION__}</span>
					<Button variant="ghost" size="icon" class="h-7 w-7 group-data-[collapsible=icon]:hidden" onclick={toggleMode}>
						{#if mode.current === 'light'}
							<Moon class="h-4 w-4 text-muted-foreground" />
						{:else}
							<Sun class="h-4 w-4 text-muted-foreground" />
						{/if}
					</Button>
					<SidebarTrigger />
				</div>
			</SidebarFooter>
		</Sidebar>

		<SidebarInset class="flex h-screen flex-col">
			<main class="flex-1">
				{@render children?.()}
			</main>
		</SidebarInset>
	</SidebarProvider>
</div>
