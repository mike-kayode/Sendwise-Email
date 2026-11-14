"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { AuthFrame } from "@/components/marketing/site";

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
    <AuthFrame>
      <div className="wrap split auth-columns">
        <aside className="dark auth-pitch">
          <div>
            <span className="eyebrow eyebrow-dark">Get started</span>
            <h1 className="display">Create your workspace and send your first email.</h1>
            <p className="lead">You will add a domain, verify it with DNS, and get an API key or SMTP credentials.</p>
          </div>
          <ol className="auth-steps">
            <li><span className="step-n step-accent">1</span><span>Create your account</span></li>
            <li><span className="step-n step-accent">2</span><span>Verify your email address</span></li>
            <li><span className="step-n step-accent">3</span><span>Add and verify your sending domain</span></li>
          </ol>
        </aside>
        <section className="card auth-form">
          <h2 className="title auth-form-title">Create account</h2>
          <p className="small auth-form-description">Already have an account? <Link href="/login">Sign in</Link></p>
          <form onSubmit={form.handleSubmit(onSubmit)} className="form-stack">
            <div>
              <label className="label" htmlFor="register-organization">Organization name</label>
              <input {...form.register("organization_name")} className="field" id="register-organization" placeholder="Workspace name" autoComplete="organization" disabled={form.formState.isSubmitting} />
              {form.formState.errors.organization_name && <p className="form-error">{form.formState.errors.organization_name.message}</p>}
            </div>
            <div>
              <label className="label" htmlFor="register-name">Your name</label>
              <input {...form.register("name")} className="field" id="register-name" placeholder="Your name" autoComplete="name" disabled={form.formState.isSubmitting} />
              {form.formState.errors.name && <p className="form-error">{form.formState.errors.name.message}</p>}
            </div>
            <div>
              <label className="label" htmlFor="register-email">Work email</label>
              <input {...form.register("email")} className="field" id="register-email" placeholder="you@example.com" type="email" autoComplete="email" disabled={form.formState.isSubmitting} />
              {form.formState.errors.email && <p className="form-error">{form.formState.errors.email.message}</p>}
            </div>
            <div>
              <label className="label" htmlFor="register-password">Password</label>
              <input {...form.register("password")} className="field" id="register-password" type="password" placeholder="••••••••••" autoComplete="new-password" aria-describedby="register-password-hint" disabled={form.formState.isSubmitting} />
              <p className="small auth-password-hint" id="register-password-hint">Use at least 10 characters.</p>
              {form.formState.errors.password && <p className="form-error">{form.formState.errors.password.message}</p>}
            </div>
            <p className="small auth-terms">
              By creating an account, you agree to the <Link href="/legal/terms">Terms</Link> and <Link href="/legal/privacy">Privacy policy</Link>.
            </p>
            <button type="submit" className="btn btn-ink auth-submit" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? "Creating workspace..." : "Create account"}
            </button>
          </form>
        </section>
      </div>
    </AuthFrame>
  );
}
