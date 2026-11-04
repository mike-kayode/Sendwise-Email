"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/hooks/use-auth";

export default function HomePage() {
  const { isAuthenticated, isLoading, token, me } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isLoading) return;
    if (token && !isAuthenticated()) {
      void me().catch(() => router.replace("/login"));
      return;
    }
    if (!token) {
      router.replace("/login");
      return;
    }
    router.replace("/dashboard");
  }, [isLoading, token, isAuthenticated, me, router]);

  return null;
}

