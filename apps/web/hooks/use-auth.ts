import { useCallback } from "react";
import { useAuthStore } from "@/stores/auth";
import { apiFetch, getAccessToken } from "@/lib/auth";
import { loginRequest } from "@/lib/login-utils";
import type { User, Organization } from "@/stores/auth";

let meInFlight: Promise<{ user: User; organization: Organization }> | null = null;

export function useAuth() {
  const token = useAuthStore((s) => s.token);
  const user = useAuthStore((s) => s.user);
  const organization = useAuthStore((s) => s.organization);
  const isLoading = useAuthStore((s) => s.isLoading);
  const setSession = useAuthStore((s) => s.setSession);
  const clearSession = useAuthStore((s) => s.clearSession);
  const setLoading = useAuthStore((s) => s.setLoading);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);

  const login = async (email: string, password: string) => {
    const res = await apiFetch<{
      access_token: string;
      user: User;
      organization: Organization;
    }>("/auth/login", {
      ...loginRequest(email, password),
    });
    setSession(res.access_token, res.user, res.organization);
    return res;
  };

  const register = async (body: {
    email: string;
    password: string;
    name: string;
    organization_name: string;
  }) => {
    // Registration does not create a session: the user must verify their
    // email address before signing in.
    const res = await apiFetch<{ message: string; email: string }>("/auth/register", {
      method: "POST",
      body: JSON.stringify(body),
    });
    return res;
  };

  const logout = async () => {
    try {
      await apiFetch("/auth/logout", {
        method: "POST",
        body: JSON.stringify({}),
      });
    } finally {
      clearSession();
      if (typeof window !== "undefined") {
        window.location.href = "/login";
      }
    }
  };

  // Stable identity + single-flight: effects depend on `me`, so a new function
  // every render re-ran them forever (each run updated the store and re-rendered),
  // hammering /auth/me until the gateway rate limiter returned 429.
  const me = useCallback(async () => {
    if (!meInFlight) {
      meInFlight = (async () => {
        const res = await apiFetch<{ user: User; organization: Organization }>("/auth/me");
        const latestToken = getAccessToken();
        if (!latestToken) throw new Error("Authentication session is missing.");
        useAuthStore.getState().setSession(latestToken, res.user, res.organization);
        return res;
      })().finally(() => {
        meInFlight = null;
      });
    }
    return meInFlight;
  }, []);

  return {
    token,
    user,
    organization,
    isLoading,
    isAuthenticated,
    login,
    register,
    logout,
    me,
    setLoading,
  };
}