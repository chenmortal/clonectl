import type { AuthProvider } from "@refinedev/core";

import { errMessage, getAuthToken, http, setAuthToken } from "@/lib/api";

export interface Identity {
  id: number;
  username: string;
  role: "admin" | "edit" | "view";
}

export const authProvider: AuthProvider = {
  login: async ({ username, password }) => {
    try {
      const { data } = await http.post("/api/auth/login", {
        username,
        password,
      });
      setAuthToken(data.access_token);
      return { success: true, redirectTo: "/" };
    } catch (e) {
      return {
        success: false,
        error: new Error(
          errMessage(e) === "Request failed with status code 401"
            ? "用户名或密码错误"
            : errMessage(e),
        ),
      };
    }
  },

  logout: async () => {
    setAuthToken(null);
    return { success: true, redirectTo: "/login" };
  },

  check: async () => {
    if (!getAuthToken()) {
      return { authenticated: false, redirectTo: "/login" };
    }
    try {
      await http.get("/api/auth/me");
      return { authenticated: true };
    } catch {
      setAuthToken(null);
      return { authenticated: false, redirectTo: "/login" };
    }
  },

  onError: async (error) => {
    const status = (error as { status?: number; response?: { status?: number } })
      .response?.status ??
      (error as { status?: number }).status;
    if (status === 401) {
      setAuthToken(null);
      return { logout: true, redirectTo: "/login" };
    }
    return {};
  },

  getIdentity: async () => {
    const { data } = await http.get<Identity>("/api/auth/me");
    return data;
  },

  getPermissions: async () => {
    const { data } = await http.get<Identity>("/api/auth/me");
    return data.role;
  },
};
