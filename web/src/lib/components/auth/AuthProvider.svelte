<script lang="ts">
    import { auth } from "$lib/stores/auth.svelte";

    let { children } = $props();

    $effect(() => {
        if (auth.token) {
            localStorage.setItem("auth_token", auth.token);
        }
    });
</script>

{@render children()}
