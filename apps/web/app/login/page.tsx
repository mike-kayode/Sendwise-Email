"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { useAuth } from "@/hooks/use-auth";
import { useToast } from "@/components/ui/use-toast";
import { ApiError } from "@/lib/api-errors";
import { loginErrorMessage } from "@/lib/login-utils";
import { AuthFrame } from "@/components/marketing/site";

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
    <AuthFrame>
      <div className="wrap split auth-columns">
        <aside className="dark auth-pitch">
          <figure className="auth-image">
            <img
              src="https://images.unsplash.com/photo-1552664730-d307ca884978?auto=format&fit=crop&w=1200&q=80"
              alt="Team collaborating on an email marketing dashboard"
              width={1200}
              height={800}
            />
          </figure>
        </aside>
        <section className="card auth-form">
          <h1 className="title auth-form-title">Sign in</h1>
          <p className="small auth-form-description">New to Sendwise? <Link href="/register">Create an account</Link></p>
          <form onSubmit={form.handleSubmit(onSubmit)} className="form-stack">
            <div>
              <label className="label" htmlFor="login-email">Email</label>
              <input
                {...form.register("email")}
                className="field"
                id="login-email"
                placeholder="you@example.com"
                type="email"
                autoComplete="email"
                disabled={form.formState.isSubmitting}
              />
              {form.formState.errors.email && <p className="form-error">{form.formState.errors.email.message}</p>}
            </div>
            <div>
              <div className="auth-password-label">
                <label className="label" htmlFor="login-password">Password</label>
                <Link className="small" href="/forgot-password">Forgot password?</Link>
              </div>
              <input
                type="password"
                {...form.register("password")}
                className="field"
                id="login-password"
                placeholder="••••••••"
                autoComplete="current-password"
                disabled={form.formState.isSubmitting}
              />
              {form.formState.errors.password && <p className="form-error">{form.formState.errors.password.message}</p>}
            </div>
            <button type="submit" className="btn btn-ink auth-submit" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? "Signing in..." : "Sign in"}
            </button>
            <p className="small auth-bottom-link">Don't have an account? <Link href="/register">Create workspace</Link></p>
          </form>
        </section>
      </div>
    </AuthFrame>
  );
}
