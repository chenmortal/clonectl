import { useEffect, useSyncExternalStore } from "react";

import { getSiteInfo } from "@/lib/api";

export const DEFAULT_SITE_TITLE = "clonectl";

// Module-level store shared by every useSiteTitle() consumer (login page,
// app shell). Kept outside react-query on purpose: the direct
// @tanstack/react-query import resolves to a different module instance than
// refine's bundled copy, which breaks QueryClient context.
let title = DEFAULT_SITE_TITLE;
let fetched = false;
let pending: Promise<void> | null = null;
const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((l) => l());
}

function load(force = false): Promise<void> {
  if (fetched && !force) return Promise.resolve();
  if (!pending) {
    pending = getSiteInfo()
      .then((d) => {
        title = d.site_title?.trim() || DEFAULT_SITE_TITLE;
        fetched = true;
        emit();
      })
      .catch(() => {
        fetched = true; // keep the default; retry on next refresh
        emit();
      })
      .finally(() => {
        pending = null;
      });
  }
  return pending;
}

/** Re-fetch the site title after the admin changes the setting. */
export function refreshSiteTitle(): void {
  load(true);
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  load();
  return () => listeners.delete(cb);
}

/**
 * Site title applied to the browser tab; the hook owns document.title and
 * every consumer re-renders when the setting changes.
 */
export function useSiteTitle(): string {
  useSyncExternalStore(subscribe, () => title);
  useEffect(() => {
    document.title = title;
  }, [title]);
  return title;
}
