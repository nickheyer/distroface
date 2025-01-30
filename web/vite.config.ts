import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";
import type { ProxyOptions } from "vite";

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    proxy: {
      "/v2": {
        target: "http://localhost:8668",
        changeOrigin: true,
        secure: false,
      },
      "/auth": {
        target: "http://localhost:8668", 
        changeOrigin: true,
        secure: false,
      },
      "/api": {
        target: "http://localhost:8668",
        changeOrigin: true,
        secure: false,
      }
    },
    allowedHosts: ["registry.localdomain", "registry.local", "localhost", "127.0.0.1", "0.0.0.0"]
  },
});
