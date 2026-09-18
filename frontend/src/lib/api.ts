import axios from "axios";

import type { LogTail, SiteInfo } from "@/lib/types";

/**
 * Thin axios wrapper shared by the refine data provider and custom calls.
 * Error shape: FastAPI bodies {"detail": string | [{loc,msg,type}]}.
 */

const TOKEN_KEY = "clonectl.token";

export const getAuthToken = (): string | null => localStorage.getItem(TOKEN_KEY);

export const setAuthToken = (token: string | null): void => {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token);
  } else {
    localStorage.removeItem(TOKEN_KEY);
  }
};

export const http = axios.create({ baseURL: "", timeout: 30000 });

http.interceptors.request.use((cfg) => {
  const token = getAuthToken();
  if (token) {
    cfg.headers = cfg.headers ?? {};
    cfg.headers.Authorization = `Bearer ${token}`;
  }
  return cfg;
});

http.interceptors.response.use(
  (r) => r,
  (err) => {
    if (axios.isAxiosError(err) && err.response?.status === 401) {
      setAuthToken(null);
      if (window.location.pathname !== "/login") {
        window.location.href = "/login";
      }
    }
    return Promise.reject(err);
  },
);

/** Extract a human message from a FastAPI-shaped error. */
export function errMessage(e: unknown): string {
  if (axios.isAxiosError(e)) {
    const detail = e.response?.data?.detail;
    if (typeof detail === "string") return detail;
    if (Array.isArray(detail) && detail[0]?.msg) return detail[0].msg;
    return e.message;
  }
  if (e instanceof Error) return e.message;
  return String(e);
}

export const healthz = () =>
  http
    .get<{ status: string; rclone_reachable: boolean }>("/healthz")
    .then((r) => r.data);

/** Brand title (public); empty site_title → caller applies its default. */
export const getSiteInfo = () =>
  http.get<SiteInfo>("/api/site-info").then((r) => r.data);

/** Last `lines` lines of the rcd log (admin). */
export const getLogTail = (lines: number) =>
  http.get<LogTail>("/api/logs", { params: { lines } }).then((r) => r.data);

/** Download the full rcd log as a file (auth via the shared interceptor). */
export async function downloadLog(): Promise<void> {
  const r = await http.get<Blob>("/api/logs/download", {
    responseType: "blob",
  });
  const url = URL.createObjectURL(r.data);
  const a = document.createElement("a");
  a.href = url;
  a.download = "rcd.log";
  a.click();
  URL.revokeObjectURL(url);
}
