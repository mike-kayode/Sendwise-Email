"use client";

import Image from "next/image";
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
          <div>
            <span className="eyebrow eyebrow-dark">Welcome back</span>
            <h1 className="display">Your campaigns and delivery logs are waiting.</h1>
            <p className="lead">Sign in to send, check domain status and see what happened to every message.</p>
          </div>
          <figure className="auth-image">
            <Image
              src="/images/email-performance-dashboard.jpg"
              alt="Laptop displaying performance charts in an analytics dashboard"
              width={1400}
              height={933}
              sizes="(max-width: 900px) 100vw, 48vw"
              priority
            />
          </figure>
          <ul className="checks">
            <li>Live DNS and domain verification status</li>
            <li>Campaign and transactional reports</li>
            <li>API keys, SMTP credentials and webhooks</li>
          </ul>
        </aside>
        <section className="card auth-form">
          <h2 className="title auth-form-title">Sign in</h2>
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
