import { create } from "zustand";
import { persist, createJSONStorage } from "zustand/middleware";
import { getAccessToken, setAccessToken, removeAccessToken } from "@/lib/auth";

export type User = {
  id: string;
  name: string;
  email: string;
};

export type Organization = {
  id: string;
  name: string;
  slug: string;
  role: string;
};

type AuthState = {
  token: string | null;
  user: User | null;
  organization: Organization | null;
  isLoading: boolean;
  setSession: (token: string, user: User, organization: Organization) => void;
  clearSession: () => void;
  setLoading: (loading: boolean) => void;
  isAuthenticated: () => boolean;
};

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      token: getAccessToken(),
      user: null,
      organization: null,
      isLoading: false,
      setSession: (token, user, organization) => {
        setAccessToken(token);
        set({ token, user, organization, isLoading: false });
      },
      clearSession: () => {
        removeAccessToken();
        set({ token: null, user: null, organization: null, isLoading: false });
      },
      setLoading: (loading) => set({ isLoading: loading }),
      isAuthenticated: () => !!get().token,
    }),
    {
      name: "auth-storage",
      storage: createJSONStorage(() => localStorage),
      partialize: (state) => ({ token: state.token, user: state.user, organization: state.organization }),
    },
  ),
);

if (typeof window !== "undefined") {
  window.addEventListener("sendwise:access-token", (event) => {
    const token = (event as CustomEvent<string | null>).detail;
    if (token) {
      useAuthStore.setState({ token });
    } else {
      useAuthStore.setState({ token: null, user: null, organization: null, isLoading: false });
    }
  });
}