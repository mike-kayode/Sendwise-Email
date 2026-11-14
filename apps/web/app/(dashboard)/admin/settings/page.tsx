"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type QuotaDefault = {
  stream: "marketing" | "transactional";
  daily_limit: number | null;
  monthly_limit: number | null;
  per_second_limit: number;
};

type QuotaDefaultsResponse = {
  defaults: QuotaDefault[];
  hard_rate_limit: number;
};

export default function AdminQuotaSettingsPage() {
  const [defaults, setDefaults] = useState<Record<string, QuotaDefault>>({});
  const [reason, setReason] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const defaultsQuery = useQuery({
    queryKey: ["admin-quota-defaults"],
    queryFn: () => apiFetch<{ data: QuotaDefaultsResponse }>("/admin/quota-defaults"),
    enabled: Boolean(sessionQuery.data?.mfa_verified),
    retry: false,
  });

  useEffect(() => {
    const values = defaultsQuery.data?.data.defaults;
    if (values) setDefaults(Object.fromEntries(values.map((item) => [item.stream, item])));
  }, [defaultsQuery.data]);

  if (sessionQuery.isLoading) return <p className="text-sm text-slate-500">Checking platform admin access...</p>;
  if (sessionQuery.error instanceof ApiError && sessionQuery.error.code === "admin_forbidden") notFound();
  if (sessionQuery.error) {
    return <div role="alert" className="rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Admin access could not be verified: {sessionQuery.error.message}</div>;
  }
  const session = sessionQuery.data;
  if (!session) return null;
  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="Quota defaults" description="Verify your authenticator before viewing platform quota settings." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }

  const updateDefault = (
    stream: "marketing" | "transactional",
    field: "daily_limit" | "monthly_limit" | "per_second_limit",
    value: string,
  ) => {
    setDefaults((current) => ({
      ...current,
      [stream]: {
        ...current[stream],
        [field]: value === "" && field !== "per_second_limit" ? null : Number(value),
      },
    }));
  };
  const save = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (session.platform_role !== "super_admin" || !reason.trim() || !mfaCode.trim() || busy) return;
    if (!window.confirm("Save these platform defaults? Domains inheriting a default will use the new limit immediately.")) return;
    setBusy(true);
    setError(null);
    try {
      await apiFetch("/admin/mfa/verify", { method: "POST", body: JSON.stringify({ code: mfaCode.trim() }) });
      await apiFetch("/admin/quota-defaults", {
        method: "PUT",
        body: JSON.stringify({
          defaults: (["marketing", "transactional"] as const).map((stream) => defaults[stream]),
          reason: reason.trim(),
        }),
      });
      setReason("");
      setMfaCode("");
      await defaultsQuery.refetch();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "Platform quota defaults could not be saved.");
    } finally {
      setBusy(false);
    }
  };

  const data = defaultsQuery.data?.data;
  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title="Quota defaults"
        description="Set the inherited daily, monthly, and per-second quotas for marketing and transactional sending domains."
      />
      {defaultsQuery.error ? <p role="alert" className="mb-4 rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-800">Quota defaults could not be loaded: {defaultsQuery.error.message}</p> : null}
      {defaultsQuery.isLoading ? <p className="text-sm text-slate-500">Loading platform quota defaults...</p> : null}
      {data ? (
        <form onSubmit={(event) => void save(event)} className="space-y-4">
          <p className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
            These values are the initial seed defaults for domains without overrides. Per-domain changes are made from the domain detail page. The hard per-domain rate ceiling is {data.hard_rate_limit.toLocaleString()} messages per second.
          </p>
          {(["marketing", "transactional"] as const).map((stream) => {
            const item = defaults[stream];
            if (!item) return null;
            return (
              <section key={stream} className="rounded-xl border border-slate-200 bg-white p-4">
                <h2 className="text-sm font-semibold capitalize text-slate-900">{stream} defaults</h2>
                <div className="mt-3 grid gap-3 sm:grid-cols-3">
                  {([
                    ["daily_limit", "Daily limit"],
                    ["monthly_limit", "Monthly limit"],
                    ["per_second_limit", "Per-second limit"],
                  ] as const).map(([field, label]) => (
                    <label key={field} className="text-sm font-medium text-slate-700">
                      {label}
                      <input
                        type="number"
                        min="1"
                        max={field === "per_second_limit" ? data.hard_rate_limit : undefined}
                        required={field === "per_second_limit"}
                        value={item[field] ?? ""}
                        placeholder={field === "per_second_limit" ? "Required" : "No daily/monthly cap"}
                        onChange={(event) => updateDefault(stream, field, event.target.value)}
                        disabled={session.platform_role !== "super_admin"}
                        className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm disabled:bg-slate-50"
                      />
                    </label>
                  ))}
                </div>
              </section>
            );
          })}
          {session.platform_role === "super_admin" ? (
            <section className="grid gap-3 rounded-xl border border-slate-200 bg-white p-4 sm:grid-cols-2">
              <label className="text-sm font-medium text-slate-700">
                Required reason
                <textarea required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} className="mt-1 min-h-20 w-full rounded-lg border border-slate-200 p-3 text-sm" />
              </label>
              <label className="text-sm font-medium text-slate-700">
                Authenticator or recovery code
                <input required autoComplete="one-time-code" value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
              </label>
              {error ? <p role="alert" className="text-sm text-rose-700 sm:col-span-2">{error}</p> : null}
              <button type="submit" disabled={busy || defaultsQuery.isLoading} className="justify-self-start rounded-lg bg-emerald-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">{busy ? "Saving defaults..." : "Review and save defaults"}</button>
            </section>
          ) : <p className="text-sm text-slate-500">Support admins can review defaults but cannot change them.</p>}
        </form>
      ) : null}
    </div>
  );
}
