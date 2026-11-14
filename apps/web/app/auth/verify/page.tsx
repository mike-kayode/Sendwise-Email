"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { AuthFrame } from "@/components/marketing/site";

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
    <AuthFrame>
      <section className="wrap card auth-card" aria-live="polite">
        <span className={`auth-status-icon auth-state-${state}`} aria-hidden="true">
          {state === "success" ? "✓" : state === "error" ? "!" : state === "loading" ? "…" : "✉"}
        </span>
        <h1 className="title auth-form-title">
          {state === "loading" ? "Verifying…" : state === "success" ? "Verified" : state === "pending" ? "Check your inbox" : "Verification failed"}
        </h1>
        <p className="small auth-form-description">
          {state === "loading" ? "Checking your verification link." : message}
        </p>
        {(state === "error" || state === "pending") && (
          <form onSubmit={resend} className="form-stack">
            <div>
              <label className="label" htmlFor="verify-email">Email address</label>
              <input
                id="verify-email"
                className="field"
                type="email"
                autoComplete="email"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                disabled={resendState !== "idle"}
                required
              />
            </div>
            <button type="submit" className="btn btn-ink auth-submit" disabled={resendState !== "idle"}>
              {resendState === "sending" ? "Sending…" : resendState === "sent" ? "Email requested" : "Resend verification email"}
            </button>
          </form>
        )}
        {state === "loading" && <p className="small auth-status-note">This should only take a moment.</p>}
        <p className="small auth-bottom-link">Already verified? <Link href="/login">Sign in</Link></p>
      </section>
    </AuthFrame>
  );
}
