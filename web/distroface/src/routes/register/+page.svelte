<script lang="ts">
	import { goto } from '$app/navigation';
	import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';

	let username = $state('');
	let email = $state('');
	let password = $state('');
	let confirmPassword = $state('');
	let isSubmitting = $state(false);

	async function handleRegister(e: Event) {
		e.preventDefault();
		if (!username || !email || !password) return;

		if (password !== confirmPassword) {
			toast.error('Passwords do not match');
			return;
		}

		if (password.length < 8) {
			toast.error('Password must be at least 8 characters');
			return;
		}

		isSubmitting = true;
		try {
			await authStore.register(username, email, password);
			toast.success('Account created successfully');
			goto('/');
		} catch (err: any) {
			toast.error(err.message || 'Registration failed');
		} finally {
			isSubmitting = false;
		}
	}
</script>

<div class="flex min-h-screen items-center justify-center p-4">
	<Card class="w-full max-w-md">
		<CardHeader class="text-center">
			<CardTitle class="text-2xl">Create an account</CardTitle>
		</CardHeader>
		<CardContent>
			<form onsubmit={handleRegister} class="space-y-4">
				<div class="space-y-2">
					<Label for="username">Username</Label>
					<Input
						id="username"
						type="text"
						placeholder="username"
						bind:value={username}
						required
					/>
					<p class="text-xs text-muted-foreground">Lowercase letters, numbers, hyphens, dots. 3-40 characters.</p>
				</div>
				<div class="space-y-2">
					<Label for="email">Email</Label>
					<Input
						id="email"
						type="email"
						placeholder="you@example.com"
						bind:value={email}
						required
					/>
				</div>
				<div class="space-y-2">
					<Label for="password">Password</Label>
					<Input
						id="password"
						type="password"
						placeholder="at least 8 characters"
						bind:value={password}
						required
					/>
				</div>
				<div class="space-y-2">
					<Label for="confirmPassword">Confirm Password</Label>
					<Input
						id="confirmPassword"
						type="password"
						placeholder="confirm password"
						bind:value={confirmPassword}
						required
					/>
				</div>
				<Button type="submit" class="w-full" disabled={isSubmitting}>
					{isSubmitting ? 'Creating account...' : 'Create account'}
				</Button>
			</form>
			<div class="mt-4 text-center text-sm text-muted-foreground">
				Already have an account?
				<a href="/login" class="text-primary underline-offset-4 hover:underline">Sign in</a>
			</div>
		</CardContent>
	</Card>
</div>
