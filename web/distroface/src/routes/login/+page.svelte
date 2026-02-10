<script lang="ts">
	import { goto } from '$app/navigation';
	import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';

	let identifier = $state('');
	let password = $state('');
	let isSubmitting = $state(false);

	async function handleLogin(e: Event) {
		e.preventDefault();
		if (!identifier || !password) return;

		isSubmitting = true;
		try {
			await authStore.login(identifier, password);
			toast.success('Logged in successfully');
			goto('/');
		} catch (err: any) {
			toast.error(err.message || 'Login failed');
		} finally {
			isSubmitting = false;
		}
	}
</script>

<div class="flex min-h-screen items-center justify-center p-4">
	<Card class="w-full max-w-md">
		<CardHeader class="text-center">
			<CardTitle class="text-2xl">Sign in to Distroface</CardTitle>
		</CardHeader>
		<CardContent>
			<form onsubmit={handleLogin} class="space-y-4">
				<div class="space-y-2">
					<Label for="identifier">Username or Email</Label>
					<Input
						id="identifier"
						type="text"
						placeholder="username or email"
						bind:value={identifier}
						required
					/>
				</div>
				<div class="space-y-2">
					<Label for="password">Password</Label>
					<Input
						id="password"
						type="password"
						placeholder="password"
						bind:value={password}
						required
					/>
				</div>
				<Button type="submit" class="w-full" disabled={isSubmitting}>
					{isSubmitting ? 'Signing in...' : 'Sign in'}
				</Button>
			</form>
			<div class="mt-4 text-center text-sm text-muted-foreground">
				Don't have an account?
				<a href="/register" class="text-primary underline-offset-4 hover:underline">Register</a>
			</div>
		</CardContent>
	</Card>
</div>
