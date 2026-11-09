"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldLabel } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { PageHeader } from "@/components/page-header";
import { CheckCircle2, XCircle } from "lucide-react";

type VerifyState = "loading" | "success" | "error" | "pending";

// The runtime may mount this page more than once per navigation, and each mount
// would otherwise POST the same one-time token again: the second attempt fails
// (token already used) and repaints the page as "Verification failed" even
// though the first attempt verified the address. Cache the in-flight request per
// token for the lifetime of the document so the token is submitted exactly once.
const verifyRequests = new Map<string, Promise<{ message: string }>>();

export default function VerifyEmailPage() {
  const router = useRouter();
  const { toast } = useToast();
  const [state, setState] = useState<VerifyState>("loading");
  const [message, setMessage] = useState("");
  const [email, setEmail] = useState("");
  const [resendState, setResendState] = useState<"idle" | "sending" | "sent">("idle");
  const verificationRequest = useRef<Promise<{ message: string }> | null>(null);
  const succeeded = useRef(false);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token");
    if (!token) {
      setEmail(params.get("email") ?? "");
      if (params.get("delivery") === "retrying") {
        setState("pending");
        setMessage("Your verification email is queued. If delivery fails, we'll keep retrying. You can also request another link below.");
      } else if (params.get("delivery") === "queue_failed") {
        setState("pending");
        setMessage("Your account was created, but the verification email could not be queued. Please use the button below to request another link.");
      } else {
        setState("pending");
        setMessage("Your verification email is queued. If delivery fails, we'll keep retrying. You can request another link below.");
      }
      return;
    }

    if (!verificationRequest.current) {
      let request = verifyRequests.get(token);
      if (!request) {
        request = apiFetch<{ message: string }>("/auth/verify", {
          method: "POST",
          body: JSON.stringify({ token }),
        });
        verifyRequests.set(token, request);
      }
      verificationRequest.current = request;
    }

    let active = true;
    verificationRequest.current
      .then((res) => {
        if (!active) return;
        succeeded.current = true;
        setState("success");
        setMessage(res.message ?? "Email verified.");
        toast({
          title: "Email verified",
          description: "Your email address is confirmed. You can now sign in.",
        });
        setTimeout(() => router.push("/login"), 1200);
      })
      .catch((err: any) => {
        // A duplicate response for a token that already verified must never
        // replace the success state.
        if (!active || succeeded.current) return;
        setState("error");
        setMessage(err?.message ?? "This verification link is invalid or has expired.");
      });

    return () => {
      active = false;
    };
  }, [router, toast]);

  async function resend(event: React.FormEvent) {
    event.preventDefault();
    setResendState("sending");
    try {
      await apiFetch<{ message: string }>("/auth/resend-verification", {
        method: "POST",
        body: JSON.stringify({ email }),
      });
      setResendState("sent");
      toast({
        title: "Check your inbox",
        description: "If that address needs verification, a new link is queued. If delivery fails, we'll keep retrying.",
      });
    } catch (err: any) {
      setResendState("idle");
      toast({
        title: "Could not queue link",
        description: err?.message ?? "Please try again in a moment. Delivery failures are retried automatically.",
        variant: "destructive",
      });
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#f6f8f7] px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Verify your email</CardTitle>
          <CardDescription>Confirm your address to activate your Sendwise workspace.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <PageHeader
            title={
              state === "loading"
                ? "Verifying…"
                : state === "success"
                  ? "Verified"
                  : state === "pending"
                    ? "Check your inbox"
                    : "Verification failed"
            }
            description={state === "loading" ? "Checking your verification link." : message}
          />
          {state === "success" && (
            <div className="flex items-center justify-center rounded-lg bg-emerald-50 p-6">
              <CheckCircle2 className="h-10 w-10 text-emerald-600" aria-hidden />
            </div>
          )}
          {(state === "error" || state === "pending") && (
            <>
              {state === "error" && (
                <div className="flex items-center justify-center rounded-lg bg-rose-50 p-6">
                  <XCircle className="h-10 w-10 text-rose-600" aria-hidden />
                </div>
              )}
              <form onSubmit={resend} className="space-y-3">
                <Field>
                  <FieldLabel>Email address</FieldLabel>
                  <Input
                    type="email"
                    autoComplete="email"
                    placeholder="you@example.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    disabled={resendState !== "idle"}
                    required
                  />
                </Field>
                <Button type="submit" className="w-full" disabled={resendState !== "idle"}>
                  {resendState === "sending"
                    ? "Sending…"
                    : resendState === "sent"
                      ? "Email requested"
                      : "Resend verification email"}
                </Button>
              </form>
            </>
          )}
          {state === "loading" && (
            <p className="text-center text-sm text-slate-500">This should only take a moment.</p>
          )}
          <p className="text-sm text-center text-slate-500">
            Already verified?{" "}
            <Link href="/login" className="font-medium text-emerald-800 hover:text-emerald-900">
              Sign in
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
