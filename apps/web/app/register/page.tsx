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
import { Mail, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";

const registerSchema = z
  .object({
    email: z.string().email("Enter a valid email address."),
    password: z.string().min(10, "Password must be at least 10 characters."),
    name: z.string().min(1, "Name is required."),
    organization_name: z.string().min(1, "Organization name is required."),
  });

type RegisterInput = z.infer<typeof registerSchema>;

export default function RegisterPage() {
  const router = useRouter();
  const { toast } = useToast();

  const form = useForm<RegisterInput>({
    resolver: zodResolver(registerSchema),
    defaultValues: { email: "", password: "", name: "", organization_name: "" },
  });

  async function onSubmit(values: RegisterInput) {
    try {
      const response = await apiFetch<{
        message: string;
        email: string;
        delivery_status?: string;
      }>("/auth/register", {
        method: "POST",
        body: JSON.stringify(values),
      });
      toast({
        title: "Account created",
        description: `A verification email is queued for ${response.email}. If delivery fails, we'll keep retrying.`,
      });
      router.push(
        `/auth/verify?email=${encodeURIComponent(response.email)}`,
      );
    } catch (err: any) {
      if (err?.code === "verification_email_unavailable") {
        router.push(
          `/auth/verify?email=${encodeURIComponent(form.getValues("email"))}&delivery=queue_failed`,
        );
        return;
      }
      toast({
        title: "Registration failed",
        description: err?.message ?? "Could not create your workspace. Please try again.",
        variant: "destructive",
      });
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#f6f8f7] px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Create your workspace</CardTitle>
          <CardDescription>
            Join Sendwise and start sending email that reaches inboxes.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <PageHeader title="Create account" description="Set up your first workspace." />
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <Field>
                <FieldLabel>
                  <Users className="mr-2 h-4 w-4 shrink-0 text-slate-400" /> Name
                </FieldLabel>
                <Input {...form.register("name")} placeholder="Your name" disabled={form.formState.isSubmitting} />
                {form.formState.errors.name && (
                  <p className="text-sm text-rose-600">{form.formState.errors.name.message}</p>
                )}
              </Field>
              <Field>
                <FieldLabel>
                  <Mail className="mr-2 h-4 w-4 shrink-0 text-slate-400" /> Email address
                </FieldLabel>
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
              <Field>
                <FieldLabel>Organization name</FieldLabel>
                <Input
                  {...form.register("organization_name")}
                  placeholder="Workspace name"
                  disabled={form.formState.isSubmitting}
                />
                {form.formState.errors.organization_name && (
                  <p className="text-sm text-rose-600">{form.formState.errors.organization_name.message}</p>
                )}
              </Field>
              <Field>
                <FieldLabel>Password</FieldLabel>
                <Input
                  type="password"
                  {...form.register("password")}
                  placeholder="••••••••••"
                  autoComplete="new-password"
                  disabled={form.formState.isSubmitting}
                />
                <p className="text-xs text-slate-500">
                  At least 10 characters.
                </p>
                {form.formState.errors.password && (
                  <p className="text-sm text-rose-600">{form.formState.errors.password.message}</p>
                )}
              </Field>
              <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
                {form.formState.isSubmitting ? "Creating workspace..." : "Create workspace"}
              </Button>
              <p className="text-sm text-center text-slate-500">
                Already have an account?{" "}
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
