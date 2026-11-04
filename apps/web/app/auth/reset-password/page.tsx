"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
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
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { PageHeader } from "@/components/page-header";

const resetPasswordSchema = z.object({
  password: z.string().min(10, "Password must be at least 10 characters."),
  confirmPassword: z.string(),
}).refine((data) => data.password === data.confirmPassword, {
  message: "Passwords do not match",
  path: ["confirmPassword"],
});

type ResetPasswordInput = z.infer<typeof resetPasswordSchema>;

export default function ResetPasswordPage() {
  const router = useRouter();
  const { toast } = useToast();
  const [token, setToken] = useState<string | null>(null);

  useEffect(() => {
    setToken(new URLSearchParams(window.location.search).get("token"));
  }, []);

  const form = useForm<ResetPasswordInput>({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { password: "", confirmPassword: "" },
  });

  if (!token) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[#f6f8f7] px-4">
        <Card className="w-full max-w-md">
          <CardHeader>
            <CardTitle>Invalid reset link</CardTitle>
            <CardDescription>This password reset link is invalid or missing.</CardDescription>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-slate-500">
              Please request a new reset link or contact support.
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  async function onSubmit(values: ResetPasswordInput) {
    try {
      await apiFetch<{ message: string }>("/auth/reset-password", {
        method: "POST",
        body: JSON.stringify({ token, password: values.password }),
      });
      toast({
        title: "Password reset",
        description: "Your password has been reset successfully. You can now sign in.",
      });
      router.push("/login");
    } catch (err: any) {
      toast({
        title: "Reset failed",
        description: err?.message ?? "Could not reset your password. Please try again.",
        variant: "destructive",
      });
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#f6f8f7] px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Set new password</CardTitle>
          <CardDescription>
            Enter your new password below.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <PageHeader title="Reset password" description="Create a new password for your Sendwise account." />
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <Field>
                <FieldLabel>New password</FieldLabel>
                <Input
                  {...form.register("password")}
                  placeholder="••••••••••"
                  type="password"
                  autoComplete="new-password"
                  disabled={form.formState.isSubmitting}
                />
                {form.formState.errors.password && (
                  <p className="text-sm text-rose-600">{form.formState.errors.password.message}</p>
                )}
              </Field>
              <Field>
                <FieldLabel>Confirm password</FieldLabel>
                <Input
                  {...form.register("confirmPassword")}
                  placeholder="••••••••••"
                  type="password"
                  autoComplete="new-password"
                  disabled={form.formState.isSubmitting}
                />
                {form.formState.errors.confirmPassword && (
                  <p className="text-sm text-rose-600">{form.formState.errors.confirmPassword.message}</p>
                )}
              </Field>
              <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
                {form.formState.isSubmitting ? "Resetting password..." : "Reset password"}
              </Button>
              <p className="text-sm text-center text-slate-500">
                Remember your password?{" "}
                <Link href="/login" className="font-medium text-emerald-800 hover:text-emerald-900">
                  Sign in
                </Link>
              </p>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}