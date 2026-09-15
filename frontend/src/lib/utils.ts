import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

import type { StorageSource } from "@/lib/types";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Join "{base}/{sub}" with sane slashes. Mirror of Go services.JoinDSPath. */
export function joinDSPath(base: string, sub: string): string {
  const b = base.replace(/\/+$/, "");
  const s = sub.replace(/^\/+/, "");
  if (!b || b === "/") return s;
  if (!s) return b;
  return `${b}/${s}`;
}

/**
 * The path actually handed to rclone for a data source: local storage
 * sources bake their FS prefix into the path (rclone's local backend has
 * no "root" config option), other backends keep the data source path
 * (bucket/prefix). Mirror of Go services.SidePath.
 */
export function effectiveDSPath(
  src: Pick<StorageSource, "type" | "path" | "extra"> | null | undefined,
  dsPath: string,
): string {
  if (src?.type === "local") {
    const fallbackRoot =
      typeof src.extra?.root === "string" && src.extra.root
        ? src.extra.root
        : "/";
    return joinDSPath(src.path?.trim() || fallbackRoot, dsPath);
  }
  return dsPath;
}

/** Full rclone path for a task side: effective DS path + task subpath. */
export function effectiveTaskPath(
  src: Pick<StorageSource, "type" | "path" | "extra"> | null | undefined,
  dsPath: string | null | undefined,
  subPath: string | null | undefined,
): string {
  return joinDSPath(effectiveDSPath(src, dsPath ?? ""), subPath ?? "");
}

/** Render a naive-UTC API timestamp ("2026-09-14T03:00:00") as "MM-DD HH:mm". */
export function fmtDateTime(v: string | null | undefined): string {
  if (!v || v.startsWith("0001-01-01")) return "-";
  const m = v.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/);
  if (!m) return v;
  return `${m[2]}-${m[3]} ${m[4]}:${m[5]}`;
}

/** Full timestamp for detail views. */
export function fmtFull(v: string | null | undefined): string {
  if (!v || v.startsWith("0001-01-01")) return "-";
  return v.replace("T", " ").replace("Z", "");
}

/** RFC3339Z timestamps from the scheduler API. */
export function fmtUtc(v: string | null | undefined): string {
  if (!v || v.startsWith("0001-01-01")) return "-";
  return v.replace("T", " ").replace("Z", "");
}

export function fmtBytes(n: number | undefined | null): string {
  if (n === undefined || n === null) return "-";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export function fmtDuration(seconds: number | undefined | null): string {
  if (seconds === undefined || seconds === null) return "-";
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const r = s % 60;
  if (m < 60) return `${m}m${r ? ` ${r}s` : ""}`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}
