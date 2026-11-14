"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/hooks/use-auth";

export function AuthenticatedHomeRedirect() {
  const { token, isLoading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!isLoading && token) router.replace("/dashboard");
  }, [isLoading, token, router]);

  return null;
}
