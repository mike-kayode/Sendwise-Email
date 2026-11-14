"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { AuthFrame } from "@/components/marketing/site";

const forgotPasswordSchema = z.object({
  email: z.string().email("Enter a valid email address."),
});

type ForgotPasswordInput = z.infer<typeof forgotPasswordSchema>;

export default function ForgotPasswordPage() {
  const router = useRouter();
  const { toast } = useToast();

  const form = useForm<ForgotPasswordInput>({
    resolver: zodResolver(forgotPasswordSchema),
    defaultValues: { email: "" },
  });

  async function onSubmit(values: ForgotPasswordInput) {
    try {
      await apiFetch<{ message: string }>("/auth/forgot-password", {
        method: "POST",
        body: JSON.stringify({ email: values.email }),
      });
      toast({
        title: "Reset link sent",
        description: "If an account with this email exists, you will receive a password reset link shortly.",
      });
      router.push("/login");
    } catch (err: any) {
      toast({
        title: "Request failed",
        description: err?.message ?? "Could not process your request. Please try again.",
        variant: "destructive",
      });
    }
  }

  return (
    <AuthFrame>
      <section className="wrap card auth-card">
        <h1 className="title auth-form-title">Reset your password</h1>
        <p className="small auth-form-description">Enter your email address and we’ll send you a reset link.</p>
        <form onSubmit={form.handleSubmit(onSubmit)} className="form-stack">
          <div>
            <label className="label" htmlFor="forgot-email">Email address</label>
            <input {...form.register("email")} className="field" id="forgot-email" placeholder="you@example.com" type="email" autoComplete="email" disabled={form.formState.isSubmitting} />
            {form.formState.errors.email && <p className="form-error">{form.formState.errors.email.message}</p>}
          </div>
          <button type="submit" className="btn btn-ink auth-submit" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting ? "Sending..." : "Send reset link"}
          </button>
          <p className="small auth-bottom-link">Remember your password? <Link href="/login">Sign in</Link></p>
        </form>
      </section>
    </AuthFrame>
  );
}