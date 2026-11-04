"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldLabel,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useAuth } from "@/hooks/use-auth";
import { useToast } from "@/components/ui/use-toast";
import { PageHeader } from "@/components/page-header";
import { ApiError } from "@/lib/api-errors";
import { loginErrorMessage } from "@/lib/login-utils";

const loginSchema = z.object({
  email: z.string().email("Enter a valid email address."),
  password: z.string().min(1, "Password is required."),
});

type LoginInput = z.infer<typeof loginSchema>;

export default function LoginPage() {
  const router = useRouter();
  const { login } = useAuth();
  const { toast } = useToast();
  const [callbackUrl, setCallbackUrl] = useState("/dashboard");

  const form = useForm<LoginInput>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: "", password: "" },
  });

  // Read callbackUrl from query string only (never credentials).
  // Credentials must never be passed in the URL.
  if (typeof window !== "undefined") {
    const callbackUrlParam = new URLSearchParams(window.location.search).get("callbackUrl");
    if (callbackUrlParam?.startsWith("/") && !callbackUrlParam.startsWith("//")) {
      setCallbackUrl(callbackUrlParam);
    }
  }

  async function onSubmit(values: LoginInput) {
    try {
      await login(values.email, values.password);
      router.push(callbackUrl);
    } catch (err: unknown) {
      const isNotVerified = err instanceof ApiError && err.code === "email_not_verified";
      toast({
        title: isNotVerified ? "Verify your email" : "Sign in failed",
        description: loginErrorMessage(err),
        variant: "destructive",
      });
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#f6f8f7] px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Sign in to Sendwise</CardTitle>
          <CardDescription>
            Enter your workspace credentials below.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <PageHeader title="Welcome back" description="Sign in to your workspace." />
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <Field>
                <FieldLabel>Email address</FieldLabel>
                <Input
                  {...form.register("email")}
                  placeholder="you@example.com"
                  type="email"
                  autoComplete="username"
                  disabled={form.formState.isSubmitting}
                />
                {form.formState.errors.email && (
                  <p className="text-sm text-rose-600">{form.formState.errors.email.message}</p>
                )}
              </Field>
              <Field>
                <FieldLabel>Password</FieldLabel>
                <Input
                  type="password"
                  {...form.register("password")}
                  placeholder="••••••••"
                  autoComplete="current-password"
                  disabled={form.formState.isSubmitting}
                />
                {form.formState.errors.password && (
                  <p className="text-sm text-rose-600">{form.formState.errors.password.message}</p>
                )}
              </Field>
              <div className="flex items-center justify-between">
                <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
                  {form.formState.isSubmitting ? "Signing in..." : "Sign in"}
                </Button>
              </div>
              <p className="text-sm text-center text-slate-500">
                Don't have an account?{" "}
                <Link href="/register" className="font-medium text-emerald-800 hover:text-emerald-900">
                  Create workspace
                </Link>
              </p>
              <p className="text-sm text-center text-slate-500">
                Forgot your password?{" "}
                <Link href="/forgot-password" className="font-medium text-emerald-800 hover:text-emerald-900">
                  Reset password
                </Link>
              </p>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
