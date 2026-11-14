"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { AuthFrame } from "@/components/marketing/site";

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
      <AuthFrame>
        <section className="wrap card auth-card">
          <h1 className="title auth-form-title">Invalid reset link</h1>
          <p className="small auth-form-description">This password reset link is invalid or missing.</p>
          <p className="small auth-status-note">Please request a new reset link or contact support.</p>
          <Link className="btn btn-ink auth-submit" href="/forgot-password">Request a new link</Link>
        </section>
      </AuthFrame>
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
    <AuthFrame>
      <section className="wrap card auth-card">
        <h1 className="title auth-form-title">Set new password</h1>
        <p className="small auth-form-description">Enter your new password below.</p>
        <form onSubmit={form.handleSubmit(onSubmit)} className="form-stack">
          <div>
            <label className="label" htmlFor="reset-password">New password</label>
            <input {...form.register("password")} id="reset-password" className="field" placeholder="••••••••••" type="password" autoComplete="new-password" disabled={form.formState.isSubmitting} />
            {form.formState.errors.password && <p className="form-error">{form.formState.errors.password.message}</p>}
          </div>
          <div>
            <label className="label" htmlFor="reset-confirm-password">Confirm password</label>
            <input {...form.register("confirmPassword")} id="reset-confirm-password" className="field" placeholder="••••••••••" type="password" autoComplete="new-password" disabled={form.formState.isSubmitting} />
            {form.formState.errors.confirmPassword && <p className="form-error">{form.formState.errors.confirmPassword.message}</p>}
          </div>
          <button type="submit" className="btn btn-ink auth-submit" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting ? "Resetting password..." : "Reset password"}
          </button>
          <p className="small auth-bottom-link">Remember your password? <Link href="/login">Sign in</Link></p>
        </form>
      </section>
    </AuthFrame>
  );
}