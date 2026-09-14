import type { DataProvider } from "@refinedev/core";

import { http } from "@/lib/api";

/**
 * Maps refine's data actions onto the existing REST API:
 *   getList  GET    /api/{resource}?meta.params   (server filtering is
 *   getOne   GET    /api/{resource}/{id}          available for runs/checks)
 *   create   POST   /api/{resource}
 *   update   PUT    /api/{resource}/{id}          (partial body passthrough)
 *   deleteOne DELETE /api/{resource}/{id}
 *
 * Errors are rethrown as plain Errors carrying the FastAPI `detail` message
 * so refine notifications read naturally in Chinese/English alike.
 */

export function toMessage(e: unknown): Error {
  if (e instanceof Error) {
    const axiosErr = e as Error & {
      response?: { data?: { detail?: unknown }; status?: number };
    };
    const msg = new Error(errMessageWith(axiosErr));
    (msg as Error & { cause?: unknown }).cause = e;
    return msg;
  }
  return new Error(String(e));
}

function errMessageWith(e: {
  response?: { data?: { detail?: unknown }; status?: number };
}): string {
  const detail = e.response?.data?.detail;
  if (typeof detail === "string") return detail;
  if (Array.isArray(detail) && detail[0]?.msg) return String(detail[0].msg);
  return e.response ? `请求失败（HTTP ${e.response.status}）` : "网络错误";
}

export const dataProvider: DataProvider = {
  getApiUrl: () => "/api",

  getList: async ({ resource, meta }) => {
    const params = (meta?.params as Record<string, unknown> | undefined) ?? {};
    try {
      const { data } = await http.get(`/api/${resource}`, { params });
      const rows = Array.isArray(data) ? data : [];
      return { data: rows, total: rows.length };
    } catch (e) {
      throw toMessage(e);
    }
  },

  getOne: async ({ resource, id }) => {
    try {
      const { data } = await http.get(`/api/${resource}/${id}`);
      return { data };
    } catch (e) {
      throw toMessage(e);
    }
  },

  create: async ({ resource, variables }) => {
    try {
      const { data } = await http.post(`/api/${resource}`, variables);
      return { data };
    } catch (e) {
      throw toMessage(e);
    }
  },

  update: async ({ resource, id, variables }) => {
    try {
      const { data } = await http.put(`/api/${resource}/${id}`, variables);
      return { data };
    } catch (e) {
      throw toMessage(e);
    }
  },

  deleteOne: async ({ resource, id }) => {
    try {
      await http.delete(`/api/${resource}/${id}`);
      return { data: { id } } as never;
    } catch (e) {
      throw toMessage(e);
    }
  },

  custom: async ({ url, method, payload }) => {
    try {
      const { data } = await http.request({
        url,
        method,
        data: payload,
      });
      return { data };
    } catch (e) {
      throw toMessage(e);
    }
  },
};
