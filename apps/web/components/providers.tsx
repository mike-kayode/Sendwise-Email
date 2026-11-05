"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "@/components/ui/toaster";
import { useEffect } from "react";
import { useAuth } from "@/hooks/use-auth";

let queryClient: QueryClient;

export function makeQueryClient() {
  if (typeof window === "undefined") {
    return new QueryClient({
      defaultOptions: {
        queries: {
          staleTime: 30 * 1000,
          refetchOnWindowFocus: false,
        },
      },
    });
  }
  if (!queryClient) {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          staleTime: 30 * 1000,
          refetchOnWindowFocus: false,
        },
      },
    });
  }
  return queryClient;
}

export function Providers({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, token, me, isLoading } = useAuth();
  const client = makeQueryClient();

  useEffect(() => {
    if (token && !isLoading) {
      void me().catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, isLoading]);

  return (
    <QueryClientProvider client={client}>
      <Toaster position="top-right" richColors closeButton />
      {children}
    </QueryClientProvider>
  );
}
