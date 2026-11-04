"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { AppShell } from "@/components/app-shell";
import { useAuth } from "@/hooks/use-auth";

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading, token, me } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isLoading) return;
    if (token && !isAuthenticated()) {
      // token present but no session; try to restore
      void me().catch(() => router.replace("/login"));
    }
    if (!token) {
      router.replace("/login");
    }
  }, [isLoading, token, isAuthenticated, me, router]);

  if (isLoading) {
    return null;
  }

  return <AppShell>{children}</AppShell>;
}

