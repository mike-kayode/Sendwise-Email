"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
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
import { Mail } from "lucide-react";
import { PageHeader } from "@/components/page-header";

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
    <div className="flex min-h-screen items-center justify-center bg-[#f6f8f7] px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Reset your password</CardTitle>
          <CardDescription>
            Enter your email address and we’ll send you a reset link.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <PageHeader title="Forgot password" description="Reset your Sendwise account password." />
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <Field>
                <FieldLabel>Email address</FieldLabel>
                <Input
                  {...form.register("email")}
                  placeholder="you@example.com"
                  type="email"
                  autoComplete="email"
                  disabled={form.formState.isSubmitting}
                />
                {form.formState.errors.email && (
                  <p className="text-sm text-rose-600">{form.formState.errors.email.message}</p>
                )}
              </Field>
              <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
                {form.formState.isSubmitting ? "Sending..." : "Send reset link"}
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