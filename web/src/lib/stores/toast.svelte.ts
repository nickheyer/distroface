import { writable } from 'svelte/store';

export type ToastType = 'success' | 'error' | 'info';

interface Toast {
  id: string;
  message: string;
  type: ToastType;
}

export const toasts = writable<Toast[]>([]);

export function showToast(message: string, type: ToastType = 'info') {
  const id = crypto.randomUUID();
  toasts.update((existing) => [...existing, { id, message, type }]);

  // AUTO REMOVE AFTER 5 SECONDS
  setTimeout(() => {
    toasts.update((all) => all.filter((t) => t.id !== id));
  }, 5000);
}

export function removeToast(id: string) {
  toasts.update((all) => all.filter((t) => t.id !== id));
}

