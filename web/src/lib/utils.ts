import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
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
