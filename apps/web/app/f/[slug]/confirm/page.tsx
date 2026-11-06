"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { apiUrl } from "@/lib/api-url";

export default function FormConfirmationPage() {
  const { slug } = useParams<{ slug: string }>();
  const [message, setMessage] = useState("Confirming your subscription...");
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    const token = new URLSearchParams(window.location.search).get("token");
    if (!token) {
      setError("The confirmation link is incomplete.");
      return () => { active = false; };
    }
    fetch(`${apiUrl(`/api/v1/public/forms/${encodeURIComponent(slug)}/confirm`)}?token=${encodeURIComponent(token)}`, {
      cache: "no-store",
      credentials: "omit",
    })
      .then(async (response) => {
        const result = await response.json() as { success_message?: string; error?: { message?: string } };
        if (!response.ok) throw new Error(result.error?.message ?? "The subscription could not be confirmed.");
        if (active) setMessage(result.success_message ?? "Your subscription is confirmed.");
      })
      .catch((reason: unknown) => {
        if (active) setError(reason instanceof Error ? reason.message : "The subscription could not be confirmed.");
      });
    return () => { active = false; };
  }, [slug]);

  return (
    <main className="mx-auto my-16 max-w-lg px-5">
      <section className="rounded-2xl border border-slate-200 bg-white p-8 text-center shadow-sm">
        <h1 className="text-2xl font-semibold text-slate-950">{error ? "Confirmation unavailable" : "Subscription confirmation"}</h1>
        <p role={error ? "alert" : "status"} className={`mt-3 text-sm ${error ? "text-rose-700" : "text-slate-600"}`}>{error || message}</p>
      </section>
    </main>
  );
}
