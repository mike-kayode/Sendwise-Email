"use client";

import Link from "next/link";
import { notFound, useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type ResolverResult = {
  resolver: string;
  status: string;
  found_value?: string;
  error?: string;
};

type DNSRecordResult = {
  status: string;
  found_value?: string;
  resolvers?: ResolverResult[];
};

type AdminDomain = {
  id: string;
  workspace_id: string;
  workspace_name: string;
  workspace_slug: string;
  domain: string;
  status: string;
  spf_status: string;
  dkim_status: string;
  dmarc_status: string;
  verification_status: string;
  owner_conflict: boolean;
  stalwart_authenticated: boolean;
  transactional_enabled: boolean;
  admin_disabled_at?: string | null;
  last_checked_at?: string | null;
  created_at: string;
  dns_check_details?: {
    checked_at?: string | null;
    records?: Record<string, DNSRecordResult>;
  };
};

type DomainCandidatesResponse = {
  data: AdminDomain[];
  pagination: { total: number; limit: number; offset: number };
};

type QuotaSnapshot = {
  daily_limit: number | null;
  monthly_limit: number | null;
  per_second_limit: number;
  daily_override: number | null;
  monthly_override: number | null;
  per_second_override: number | null;
  unlimited: boolean;
  daily_used: number;
  monthly_used: number;
  per_second_used: number;
  daily_reset_at: string;
  monthly_reset_at: string;
  per_second_reset_at: string;
  daily_usage_percent: number;
  monthly_usage_percent: number;
};

type QuotaData = {
  streams: Record<"marketing" | "transactional", QuotaSnapshot>;
  risk: {
    young_domain: boolean;
    verified_days: number;
    high_bounce_rate: boolean;
    bounce_rate_7_days: number;
  };
  requests: Array<{
    id: string;
    stream: "marketing" | "transactional";
    daily_limit: number | null;
    monthly_limit: number | null;
    per_second_limit: number | null;
    reason: string;
    status: string;
    created_at: string;
  }>;
};

type QuotaDraft = {
  daily_limit: string;
  monthly_limit: string;
  per_second_limit: string;
  unlimited: boolean;
};

type WarmupData = {
  profile: string;
  successful_days: number;
  curve_cap: number;
  effective_cap_today: number;
  override: null | {
    daily_cap: number;
    expires_at: string;
    reason: string;
    set_by: string | null;
    set_at: string | null;
    active: boolean;
  };
  usage_today: {
    reserved: number;
    successful: number;
    bounced: number;
  };
  warnings: {
    verified_within_14_days: boolean;
    has_sending_history: boolean;
    recent_bounce_rate: number;
  };
};

function AdminDomainQuotaPanel({ domainID, role }: { domainID: string; role: AdminSession["platform_role"] }) {
  const [drafts, setDrafts] = useState<Record<string, QuotaDraft>>({});
  const [reason, setReason] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const quotaQuery = useQuery({
    queryKey: ["admin-domain-quotas", domainID],
    queryFn: () => apiFetch<{ data: QuotaData }>(`/admin/domains/${encodeURIComponent(domainID)}/quotas`),
    retry: false,
  });

  useEffect(() => {
    const streams = quotaQuery.data?.data.streams;
    if (!streams) return;
    setDrafts(Object.fromEntries((["marketing", "transactional"] as const).map((stream) => {
      const quota = streams[stream];
      return [stream, {
        daily_limit: quota.daily_override?.toString() ?? "",
        monthly_limit: quota.monthly_override?.toString() ?? "",
        per_second_limit: quota.per_second_override?.toString() ?? "",
        unlimited: quota.unlimited,
      }];
    })));
  }, [quotaQuery.data]);

  const updateDraft = (stream: "marketing" | "transactional", key: keyof QuotaDraft, value: string | boolean) => {
    setDrafts((current) => ({ ...current, [stream]: { ...current[stream], [key]: value } }));
  };
  const updateQuotas = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (role !== "super_admin" || !reason.trim() || !mfaCode.trim() || busy) return;
    if (!window.confirm("Apply these quota overrides? The change will be recorded in the immutable admin audit log.")) return;
    setBusy(true);
    setError(null);
    try {
      await apiFetch("/admin/mfa/verify", { method: "POST", body: JSON.stringify({ code: mfaCode.trim() }) });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}/quotas`, {
        method: "PUT",
        body: JSON.stringify({
          streams: (["marketing", "transactional"] as const).map((stream) => ({
            stream,
            daily_limit: drafts[stream]?.daily_limit ? Number(drafts[stream].daily_limit) : null,
            monthly_limit: drafts[stream]?.monthly_limit ? Number(drafts[stream].monthly_limit) : null,
            per_second_limit: drafts[stream]?.per_second_limit ? Number(drafts[stream].per_second_limit) : null,
            unlimited: drafts[stream]?.unlimited ?? false,
          })),
          reason: reason.trim(),
        }),
      });
      setReason("");
      setMfaCode("");
      await quotaQuery.refetch();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "The domain quotas could not be updated.");
    } finally {
      setBusy(false);
    }
  };

  const timeLabel = (value: string) => new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(value)) + " UTC";
  const limitLabel = (value: number | null, unlimited: boolean) => unlimited || value === null ? "No cap" : value.toLocaleString();
  const riskyDomain = Boolean(quotaQuery.data?.data.risk.young_domain || quotaQuery.data?.data.risk.high_bounce_rate);
  const sharplyRaised = (["marketing", "transactional"] as const).some((stream) => {
    const snapshot = quotaQuery.data?.data.streams[stream];
    const draft = drafts[stream];
    if (!snapshot || !draft) return false;
    if (draft.unlimited && !snapshot.unlimited) return true;
    return (
      (snapshot.daily_limit !== null && draft.daily_limit !== "" && Number(draft.daily_limit) > snapshot.daily_limit * 2) ||
      (snapshot.monthly_limit !== null && draft.monthly_limit !== "" && Number(draft.monthly_limit) > snapshot.monthly_limit * 2) ||
      (draft.per_second_limit !== "" && Number(draft.per_second_limit) > snapshot.per_second_limit * 2)
    );
  });

  return (
    <section aria-labelledby="domain-quota-heading" className="mt-5 rounded-xl border border-slate-200 bg-white">
      <div className="border-b border-slate-100 p-4">
        <h2 id="domain-quota-heading" className="text-sm font-semibold text-slate-900">Domain quotas</h2>
        <p className="mt-1 text-xs text-slate-500">Usage and effective limits for each sending stream. Blank override fields inherit the platform default.</p>
      </div>
      {quotaQuery.isLoading ? <p className="p-4 text-sm text-slate-500">Loading domain quotas...</p> : null}
      {quotaQuery.error ? <p role="alert" className="p-4 text-sm text-rose-700">Domain quotas could not be loaded: {quotaQuery.error.message}</p> : null}
      {quotaQuery.data ? (
        <div className="space-y-4 p-4">
          {(["marketing", "transactional"] as const).map((stream) => {
            const quota = quotaQuery.data.data.streams[stream];
            const draft = drafts[stream];
            return (
              <article key={stream} className="rounded-lg border border-slate-200 p-4">
                <h3 className="text-sm font-semibold capitalize text-slate-900">{stream}</h3>
                <div className="mt-3 grid gap-3 sm:grid-cols-3">
                  {[
                    ["Daily", quota.daily_used, limitLabel(quota.daily_limit, quota.unlimited), quota.daily_usage_percent, quota.daily_reset_at],
                    ["Monthly", quota.monthly_used, limitLabel(quota.monthly_limit, quota.unlimited), quota.monthly_usage_percent, quota.monthly_reset_at],
                    ["Per second", quota.per_second_used, `${quota.per_second_limit.toLocaleString()}`, Math.min(100, 100 * quota.per_second_used / Math.max(1, quota.per_second_limit)), quota.per_second_reset_at],
                  ].map(([label, used, limit, percent, reset]) => (
                    <div key={String(label)} className="rounded-md bg-slate-50 p-3">
                      <div className="flex justify-between gap-2 text-xs"><span className="font-medium text-slate-700">{label}</span><span className="text-slate-600">{Number(used).toLocaleString()} / {limit}</span></div>
                      <div className="mt-2 h-2 overflow-hidden rounded-full bg-slate-200"><div className="h-full rounded-full bg-emerald-600" style={{ width: `${Math.min(100, Number(percent))}%` }} /></div>
                      <p className="mt-2 text-[11px] text-slate-500">Resets {timeLabel(String(reset))}</p>
                    </div>
                  ))}
                </div>
                {role === "super_admin" && draft ? (
                  <div className="mt-4 grid gap-3 border-t border-slate-100 pt-4 sm:grid-cols-3">
                    {([
                      ["daily_limit", "Daily override"],
                      ["monthly_limit", "Monthly override"],
                      ["per_second_limit", "Per-second override"],
                    ] as const).map(([key, label]) => (
                      <label key={key} className="text-xs font-medium text-slate-700">
                        {label}
                        <input type="number" min="1" value={draft[key]} onChange={(event) => updateDraft(stream, key, event.target.value)} placeholder="Inherit default" className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
                      </label>
                    ))}
                    <label className="flex items-center gap-2 text-sm text-slate-700 sm:col-span-3">
                      <input type="checkbox" checked={draft.unlimited} onChange={(event) => updateDraft(stream, "unlimited", event.target.checked)} />
                      Unlimited daily and monthly quotas (per-second safety limit still applies)
                    </label>
                    {riskyDomain && sharplyRaised && (
                      <p className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950 sm:col-span-3">
                        Risk warning: this domain is {quotaQuery.data.data.risk.young_domain ? `only ${quotaQuery.data.data.risk.verified_days} days past verification` : `showing a ${(quotaQuery.data.data.risk.bounce_rate_7_days * 100).toFixed(2)}% bounce rate over seven days`}. Review reputation before substantially raising its limits; proceeding requires a reason.
                      </p>
                    )}
                  </div>
                ) : null}
              </article>
            );
          })}
          {role === "super_admin" ? (
            <form onSubmit={(event) => void updateQuotas(event)} className="grid gap-3 rounded-lg bg-slate-50 p-4 sm:grid-cols-2">
              <label className="text-sm font-medium text-slate-700">
                Required reason
                <textarea required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} className="mt-1 min-h-20 w-full rounded-lg border border-slate-200 bg-white p-3 text-sm" />
              </label>
              <label className="text-sm font-medium text-slate-700">
                Authenticator or recovery code
                <input required autoComplete="one-time-code" value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
              </label>
              {error ? <p role="alert" className="text-sm text-rose-700 sm:col-span-2">{error}</p> : null}
              <button type="submit" disabled={busy || quotaQuery.isLoading} className="justify-self-start rounded-lg bg-emerald-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">{busy ? "Updating quotas..." : "Review and save quotas"}</button>
            </form>
          ) : null}
          <div>
            <h3 className="text-sm font-semibold text-slate-900">Customer increase requests</h3>
            {quotaQuery.data.data.requests.length === 0 ? <p className="mt-2 text-sm text-slate-500">No increase requests recorded.</p> : (
              <ul className="mt-2 divide-y divide-slate-100">
                {quotaQuery.data.data.requests.map((request) => (
                  <li key={request.id} className="grid gap-1 py-3 text-sm sm:grid-cols-[140px_1fr_auto]">
                    <span className="capitalize text-slate-700">{request.stream} · {request.status}</span>
                    <span className="text-slate-600">{request.reason}</span>
                    <time className="text-xs text-slate-500">{timeLabel(request.created_at)}</time>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      ) : null}
    </section>
  );
}

function dateTimeLocal(value: Date) {
  const local = new Date(value.getTime() - value.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function AdminDomainWarmupPanel({ domainID, role }: { domainID: string; role: AdminSession["platform_role"] }) {
  const [dailyCap, setDailyCap] = useState("");
  const [expiresAt, setExpiresAt] = useState(() => dateTimeLocal(new Date(Date.now() + 3 * 24 * 60 * 60 * 1000)));
  const [reason, setReason] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [clearReason, setClearReason] = useState("");
  const [clearMFACode, setClearMFACode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [clearOpen, setClearOpen] = useState(false);
  const warmupQuery = useQuery({
    queryKey: ["admin-domain-warmup", domainID],
    queryFn: () => apiFetch<{ data: WarmupData }>(`/admin/domains/${encodeURIComponent(domainID)}/warmup`),
    retry: false,
  });

  const setOverride = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (role !== "super_admin" || !dailyCap || !expiresAt || !reason.trim() || !mfaCode.trim() || busy) return;
    if (!window.confirm("Apply this temporary warm-up cap? The change will be recorded in the immutable admin audit log.")) return;
    setBusy(true);
    setError(null);
    try {
      await apiFetch("/admin/mfa/verify", { method: "POST", body: JSON.stringify({ code: mfaCode.trim() }) });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}/warmup-override`, {
        method: "PUT",
        body: JSON.stringify({
          daily_cap: Number(dailyCap),
          expires_at: new Date(expiresAt).toISOString(),
          reason: reason.trim(),
        }),
      });
      setReason("");
      setMfaCode("");
      await warmupQuery.refetch();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "The warm-up override could not be saved.");
    } finally {
      setBusy(false);
    }
  };

  const clearOverride = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (role !== "super_admin" || !clearReason.trim() || !clearMFACode.trim() || busy) return;
    if (!window.confirm("Clear the warm-up override and return this domain to its profile curve? This will be recorded in the immutable admin audit log.")) return;
    setBusy(true);
    setError(null);
    try {
      await apiFetch("/admin/mfa/verify", { method: "POST", body: JSON.stringify({ code: clearMFACode.trim() }) });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}/warmup-override`, {
        method: "DELETE",
        body: JSON.stringify({ reason: clearReason.trim() }),
      });
      setClearReason("");
      setClearMFACode("");
      setClearOpen(false);
      await warmupQuery.refetch();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "The warm-up override could not be cleared.");
    } finally {
      setBusy(false);
    }
  };

  const timeLabel = (value: string) => new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium", timeStyle: "short", timeZone: "UTC",
  }).format(new Date(value)) + " UTC";
  const warmup = warmupQuery.data?.data;
  const warning = warmup && (
    warmup.warnings.verified_within_14_days ||
    !warmup.warnings.has_sending_history ||
    warmup.warnings.recent_bounce_rate > 0.01
  );

  return (
    <section aria-labelledby="domain-warmup-heading" className="mt-5 rounded-xl border border-slate-200 bg-white">
      <div className="border-b border-slate-100 p-4">
        <h2 id="domain-warmup-heading" className="text-sm font-semibold text-slate-900">Warm-up</h2>
        <p className="mt-1 text-xs text-slate-500">A temporary cap replaces the profile curve while active, but cannot bypass sending safeguards or the domain&apos;s daily marketing quota.</p>
      </div>
      {warmupQuery.isLoading ? <p className="p-4 text-sm text-slate-500">Loading warm-up state...</p> : null}
      {warmupQuery.error ? <p role="alert" className="p-4 text-sm text-rose-700">Warm-up state could not be loaded: {warmupQuery.error.message}</p> : null}
      {warmup ? (
        <div className="space-y-4 p-4">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {[
              ["Profile", warmup.profile],
              ["Successful days", warmup.successful_days.toLocaleString()],
              ["Curve cap", warmup.curve_cap.toLocaleString()],
              ["Effective cap today", warmup.effective_cap_today.toLocaleString()],
            ].map(([label, value]) => (
              <div key={label} className="rounded-lg bg-slate-50 p-3">
                <p className="text-[11px] font-medium uppercase tracking-wide text-slate-500">{label}</p>
                <p className="mt-1 text-sm font-semibold text-slate-900">{value}</p>
              </div>
            ))}
          </div>
          <div className="rounded-lg border border-slate-100 p-3 text-xs text-slate-600">
            Usage today: {warmup.usage_today.reserved.toLocaleString()} reserved · {warmup.usage_today.successful.toLocaleString()} successful · {warmup.usage_today.bounced.toLocaleString()} bounced
          </div>
          {warning ? (
            <div role="status" className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950">
              <p className="font-semibold">Reputation warning — review before setting a higher cap.</p>
              <ul className="mt-1 list-disc pl-5">
                {warmup.warnings.verified_within_14_days ? <li>This domain was verified less than 14 days ago.</li> : null}
                {!warmup.warnings.has_sending_history ? <li>This domain has no sending history.</li> : null}
                {warmup.warnings.recent_bounce_rate > 0.01 ? <li>Recent bounce rate is {(warmup.warnings.recent_bounce_rate * 100).toFixed(2)}%, above 1%.</li> : null}
              </ul>
              <p className="mt-1">An override remains possible, but a specific reason is required and will be audited.</p>
            </div>
          ) : null}
          <div className="rounded-lg border border-slate-200 p-3">
            <h3 className="text-xs font-semibold text-slate-900">Override status</h3>
            {warmup.override ? (
              <div className="mt-2 space-y-1 text-xs text-slate-600">
                <p>
                  {warmup.override.active ? "Active" : "Expired"} · {warmup.override.daily_cap.toLocaleString()} per day · expires {timeLabel(warmup.override.expires_at)}
                </p>
                <p>Reason: {warmup.override.reason}</p>
                <p>Set by: {warmup.override.set_by ?? "Unknown"} · {warmup.override.set_at ? timeLabel(warmup.override.set_at) : "time unavailable"}</p>
              </div>
            ) : <p className="mt-1 text-xs text-slate-600">No override is configured.</p>}
            {role === "super_admin" && warmup.override?.active ? (
              <button type="button" onClick={() => setClearOpen((open) => !open)} className="mt-3 rounded-lg px-3 py-2 text-xs font-semibold text-rose-800 ring-1 ring-rose-200 hover:bg-rose-50">
                {clearOpen ? "Cancel clear" : "Clear override"}
              </button>
            ) : null}
          </div>
          {role === "super_admin" ? (
            <>
              <form onSubmit={(event) => void setOverride(event)} className="grid gap-3 rounded-lg bg-slate-50 p-4 sm:grid-cols-2">
                <label className="text-xs font-medium text-slate-700">
                  Daily cap
                  <input required type="number" min="1" step="1" value={dailyCap} onChange={(event) => setDailyCap(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                </label>
                <label className="text-xs font-medium text-slate-700">
                  Expires (max 14 days)
                  <input required type="datetime-local" min={dateTimeLocal(new Date())} max={dateTimeLocal(new Date(Date.now() + 14 * 24 * 60 * 60 * 1000))} value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                </label>
                <label className="text-xs font-medium text-slate-700">
                  Required reason
                  <textarea required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} className="mt-1 min-h-20 w-full rounded-lg border border-slate-200 bg-white p-3 text-sm" />
                </label>
                <label className="text-xs font-medium text-slate-700">
                  Authenticator or recovery code
                  <input required autoComplete="one-time-code" value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                </label>
                <button type="submit" disabled={busy || warmupQuery.isLoading} className="justify-self-start rounded-lg bg-emerald-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">
                  {busy ? "Saving..." : "Review and set override"}
                </button>
              </form>
              {clearOpen ? (
                <form onSubmit={(event) => void clearOverride(event)} className="grid gap-3 rounded-lg border border-rose-200 bg-rose-50 p-4 sm:grid-cols-2">
                  <label className="text-xs font-medium text-slate-700">
                    Required clear reason
                    <textarea required maxLength={500} value={clearReason} onChange={(event) => setClearReason(event.target.value)} className="mt-1 min-h-20 w-full rounded-lg border border-slate-200 bg-white p-3 text-sm" />
                  </label>
                  <label className="text-xs font-medium text-slate-700">
                    Authenticator or recovery code
                    <input required autoComplete="one-time-code" value={clearMFACode} onChange={(event) => setClearMFACode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  <button type="submit" disabled={busy} className="justify-self-start rounded-lg bg-rose-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">
                    {busy ? "Clearing..." : "Review and clear override"}
                  </button>
                </form>
              ) : null}
            </>
          ) : null}
          {error ? <p role="alert" className="text-sm text-rose-700">{error}</p> : null}
        </div>
      ) : null}
    </section>
  );
}

function timeLabel(value?: string | null) {
  if (!value) return "Never";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

export default function AdminDomainDetailsPage() {
  const { domainID } = useParams<{ domainID: string }>();
  const router = useRouter();
  const [recheckOpen, setRecheckOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [recheckError, setRecheckError] = useState<string | null>(null);
  const [recheckBusy, setRecheckBusy] = useState(false);
  const [statusOpen, setStatusOpen] = useState(false);
  const [statusReason, setStatusReason] = useState("");
  const [statusMFACode, setStatusMFACode] = useState("");
  const [statusError, setStatusError] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [removeReason, setRemoveReason] = useState("");
  const [removeMFACode, setRemoveMFACode] = useState("");
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [selectedOwnerID, setSelectedOwnerID] = useState("");
  const [resolveReason, setResolveReason] = useState("");
  const [resolveMFACode, setResolveMFACode] = useState("");
  const [resolveError, setResolveError] = useState<string | null>(null);
  const [resolveBusy, setResolveBusy] = useState(false);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const domainQuery = useQuery({
    queryKey: ["admin-domain", domainID],
    queryFn: () => apiFetch<{ data: AdminDomain }>(`/admin/domains/${encodeURIComponent(domainID)}`),
    enabled: Boolean(sessionQuery.data?.mfa_verified && domainID),
    retry: false,
  });
  const conflictCandidatesQuery = useQuery({
    queryKey: ["admin-domain-conflict-candidates", domainQuery.data?.data.domain],
    queryFn: () => apiFetch<DomainCandidatesResponse>(
      `/admin/domains?${new URLSearchParams({ search: domainQuery.data?.data.domain ?? "", limit: "100", offset: "0" })}`,
    ),
    enabled: Boolean(sessionQuery.data?.mfa_verified && domainQuery.data?.data.owner_conflict),
    retry: false,
  });

  if (sessionQuery.isLoading) return <p className="text-sm text-slate-500">Checking platform admin access...</p>;
  if (sessionQuery.error instanceof ApiError && sessionQuery.error.code === "admin_forbidden") notFound();
  if (sessionQuery.error) {
    return <div role="alert" className="rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Admin access could not be verified: {sessionQuery.error.message}</div>;
  }
  const session = sessionQuery.data;
  if (!session) return null;
  if (domainQuery.error instanceof ApiError && domainQuery.error.status === 404) notFound();
  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="Domain details" description="Verify your admin session before viewing DNS metadata." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }
  const domain = domainQuery.data?.data;
  const records = Object.entries(domain?.dns_check_details?.records ?? {}).sort(([left], [right]) => left.localeCompare(right));
  const submitRecheck = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!reason.trim() || !mfaCode.trim()) return;
    setRecheckBusy(true);
    setRecheckError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: mfaCode.trim() }),
      });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}/verify`, {
        method: "POST",
        body: JSON.stringify({ reason: reason.trim() }),
      });
      setReason("");
      setMfaCode("");
      setRecheckOpen(false);
      await domainQuery.refetch();
    } catch (error) {
      setRecheckError(error instanceof Error ? error.message : "The domain DNS check failed.");
    } finally {
      setRecheckBusy(false);
    }
  };
  const submitDomainStatus = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!domain || !statusReason.trim() || !statusMFACode.trim()) return;
    setStatusBusy(true);
    setStatusError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: statusMFACode.trim() }),
      });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}/disabled`, {
        method: "PATCH",
        body: JSON.stringify({ disabled: !domain.admin_disabled_at, reason: statusReason.trim() }),
      });
      setStatusReason("");
      setStatusMFACode("");
      setStatusOpen(false);
      await domainQuery.refetch();
    } catch (error) {
      setStatusError(error instanceof Error ? error.message : "The domain status could not be changed.");
    } finally {
      setStatusBusy(false);
    }
  };
  const submitDomainRemoval = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!removeReason.trim() || !removeMFACode.trim()) return;
    setRemoveBusy(true);
    setRemoveError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: removeMFACode.trim() }),
      });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}`, {
        method: "DELETE",
        body: JSON.stringify({ reason: removeReason.trim() }),
      });
      router.replace("/admin/domains");
    } catch (error) {
      setRemoveError(error instanceof Error ? error.message : "The domain could not be removed.");
    } finally {
      setRemoveBusy(false);
    }
  };
  const submitConflictResolution = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!selectedOwnerID || !resolveReason.trim() || !resolveMFACode.trim()) return;
    setResolveBusy(true);
    setResolveError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: resolveMFACode.trim() }),
      });
      await apiFetch(`/admin/domains/${encodeURIComponent(domainID)}/resolve-conflict`, {
        method: "POST",
        body: JSON.stringify({
          rightful_domain_id: selectedOwnerID,
          reason: resolveReason.trim(),
        }),
      });
      setResolveReason("");
      setResolveMFACode("");
      router.replace(`/admin/domains/${encodeURIComponent(selectedOwnerID)}`);
    } catch (error) {
      setResolveError(error instanceof Error ? error.message : "Domain ownership could not be resolved.");
    } finally {
      setResolveBusy(false);
    }
  };

  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title={domain?.domain ?? "Domain details"}
        description={domain ? `${domain.workspace_name} · /${domain.workspace_slug}` : "DNS verification metadata"}
        actions={<Link href="/admin/domains" className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200">Back to domains</Link>}
      />
      {domainQuery.error ? <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Unable to load domain: {domainQuery.error.message}</div> : null}
      {domainQuery.isLoading ? <p className="text-sm text-slate-500">Loading domain details...</p> : null}
      {domain ? (
        <>
          <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {[
              ["Domain status", domain.status],
              ["SPF", domain.spf_status],
              ["DKIM", domain.dkim_status],
              ["DMARC", domain.dmarc_status],
            ].map(([label, value]) => (
              <div key={label} className="rounded-xl border border-slate-200 bg-white p-4">
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">{label}</p>
                <p className="mt-2 text-sm font-semibold capitalize text-slate-900">{value.replaceAll("_", " ")}</p>
              </div>
            ))}
          </section>
          {session.platform_role === "super_admin" ? (
            <section className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-200 bg-white p-4">
              <div>
                <h2 className="text-sm font-semibold text-slate-900">Platform sending control</h2>
                <p className="mt-1 text-sm text-slate-600">
                  {domain.admin_disabled_at
                    ? `Disabled at ${timeLabel(domain.admin_disabled_at)}. Campaign and transactional delivery are blocked.`
                    : "Enabled. Disabling blocks new campaign and transactional delivery but preserves DNS and domain configuration."}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <button type="button" onClick={() => setStatusOpen((open) => !open)} className={`rounded-lg px-3 py-2 text-sm font-semibold ${domain.admin_disabled_at ? "bg-emerald-700 text-white" : "bg-rose-700 text-white"}`}>
                  {domain.admin_disabled_at ? "Enable sending domain" : "Disable sending domain"}
                </button>
                <button type="button" onClick={() => setRemoveOpen((open) => !open)} className="rounded-lg px-3 py-2 text-sm font-semibold text-rose-800 ring-1 ring-rose-200 hover:bg-rose-50">
                  Remove domain
                </button>
              </div>
              {statusOpen ? (
                <form onSubmit={(event) => void submitDomainStatus(event)} className="grid w-full gap-3 rounded-lg bg-slate-50 p-3 sm:grid-cols-2">
                  <p className="text-sm text-slate-700 sm:col-span-2">
                    {domain.admin_disabled_at
                      ? "Re-enabling permits sends only if the existing verification and workspace settings still allow them."
                      : "This immediately blocks campaign and transactional delivery using this domain. DNS records are not modified."}
                  </p>
                  <label className="text-sm font-medium text-slate-700">
                    Audit reason
                    <input required maxLength={500} value={statusReason} onChange={(event) => setStatusReason(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  <label className="text-sm font-medium text-slate-700">
                    Authenticator or recovery code
                    <input required autoComplete="one-time-code" value={statusMFACode} onChange={(event) => setStatusMFACode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  {statusError ? <p role="alert" className="text-sm text-rose-700 sm:col-span-2">{statusError}</p> : null}
                  <button type="submit" disabled={statusBusy} className="justify-self-start rounded-lg bg-slate-900 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">
                    {statusBusy ? "Updating..." : domain.admin_disabled_at ? "Confirm enable" : "Confirm disable"}
                  </button>
                </form>
              ) : null}
            </section>
          ) : null}
          {session.platform_role === "super_admin" && domain.owner_conflict ? (
            <section className="mt-4 rounded-xl border border-amber-300 bg-amber-50 p-4">
              <h2 className="text-sm font-semibold text-amber-950">Resolve duplicate domain ownership</h2>
              <p className="mt-1 text-sm text-amber-900">Choose the workspace that can prove control of {domain.domain}. This changes the platform ownership marker only; it does not change DNS or move other workspace data.</p>
              {conflictCandidatesQuery.isLoading ? <p className="mt-3 text-sm text-amber-900">Loading ownership candidates...</p> : null}
              {conflictCandidatesQuery.error ? <p role="alert" className="mt-3 text-sm text-rose-700">Could not load ownership candidates: {conflictCandidatesQuery.error.message}</p> : null}
              <form onSubmit={(event) => void submitConflictResolution(event)} className="mt-3 grid gap-3 sm:grid-cols-2">
                <label className="text-sm font-medium text-slate-800">
                  Rightful workspace
                  <select required value={selectedOwnerID} onChange={(event) => setSelectedOwnerID(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm">
                    <option value="">Select a workspace</option>
                    {(conflictCandidatesQuery.data?.data ?? [])
                      .filter((candidate) => candidate.domain.toLowerCase() === domain.domain.toLowerCase())
                      .map((candidate) => (
                        <option key={candidate.id} value={candidate.id}>
                          {candidate.workspace_name} · {candidate.workspace_id}{candidate.owner_conflict ? " · conflict" : " · current owner"}
                        </option>
                      ))}
                  </select>
                </label>
                <label className="text-sm font-medium text-slate-800">
                  Reason
                  <input required maxLength={500} value={resolveReason} onChange={(event) => setResolveReason(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                </label>
                <label className="text-sm font-medium text-slate-800 sm:col-span-2">
                  Authenticator or recovery code
                  <input required autoComplete="one-time-code" value={resolveMFACode} onChange={(event) => setResolveMFACode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm sm:max-w-md" />
                </label>
                {resolveError ? <p role="alert" className="text-sm text-rose-700 sm:col-span-2">{resolveError}</p> : null}
                <button type="submit" disabled={resolveBusy || conflictCandidatesQuery.isLoading || !selectedOwnerID} className="justify-self-start rounded-lg bg-amber-800 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">
                  {resolveBusy ? "Resolving..." : "Confirm rightful owner"}
                </button>
              </form>
            </section>
          ) : null}
          <AdminDomainQuotaPanel domainID={domainID} role={session.platform_role} />
          <AdminDomainWarmupPanel domainID={domainID} role={session.platform_role} />
          <section className="mt-5 rounded-xl border border-slate-200 bg-white">
            <div className="border-b border-slate-100 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h2 className="text-sm font-semibold text-slate-900">DNS resolver results</h2>
                  <p className="mt-1 text-xs text-slate-500">Last checked {timeLabel(domain.dns_check_details?.checked_at ?? domain.last_checked_at)}</p>
                </div>
                {session.platform_role === "super_admin" ? (
                  <button type="button" onClick={() => setRecheckOpen((open) => !open)} className="rounded-lg px-3 py-2 text-sm font-medium text-slate-700 ring-1 ring-slate-200">
                    Force DNS re-check
                  </button>
                ) : null}
              </div>
              {recheckOpen ? (
                <form onSubmit={(event) => void submitRecheck(event)} className="mt-4 grid gap-3 rounded-lg bg-slate-50 p-3 sm:grid-cols-2">
                  <label className="text-sm font-medium text-slate-700">
                    Audit reason
                    <input required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  <label className="text-sm font-medium text-slate-700">
                    Authenticator or recovery code
                    <input required autoComplete="one-time-code" value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  {recheckError ? <p role="alert" className="text-sm text-rose-700 sm:col-span-2">{recheckError}</p> : null}
                  <button type="submit" disabled={recheckBusy} className="justify-self-start rounded-lg bg-emerald-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">
                    {recheckBusy ? "Checking DNS..." : "Confirm re-check"}
                  </button>
                </form>
              ) : null}
              {removeOpen ? (
                <form onSubmit={(event) => void submitDomainRemoval(event)} className="mt-4 grid gap-3 rounded-lg border border-rose-200 bg-rose-50 p-3 sm:grid-cols-2">
                  <p className="text-sm text-rose-900 sm:col-span-2">
                    This permanently removes the Sendwise domain record and its sender identities for {domain.domain}. It does not modify DNS or the shared SMTP server configuration.
                  </p>
                  <label className="text-sm font-medium text-slate-700">
                    Audit reason
                    <input required maxLength={500} value={removeReason} onChange={(event) => setRemoveReason(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  <label className="text-sm font-medium text-slate-700">
                    Authenticator or recovery code
                    <input required autoComplete="one-time-code" value={removeMFACode} onChange={(event) => setRemoveMFACode(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 bg-white px-3 text-sm" />
                  </label>
                  {removeError ? <p role="alert" className="text-sm text-rose-700 sm:col-span-2">{removeError}</p> : null}
                  <button type="submit" disabled={removeBusy} className="justify-self-start rounded-lg bg-rose-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 sm:col-span-2">
                    {removeBusy ? "Removing..." : "Confirm permanent removal"}
                  </button>
                </form>
              ) : null}
            </div>
            {records.length === 0 ? <p className="p-4 text-sm text-slate-500">No resolver results have been recorded.</p> : null}
            <div className="divide-y divide-slate-100">
              {records.map(([recordName, record]) => (
                <div key={recordName} className="p-4">
                  <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                    <h3 className="text-sm font-semibold uppercase text-slate-800">{recordName}</h3>
                    <span className="rounded-full bg-slate-100 px-2 py-1 text-xs text-slate-700">{record.status.replaceAll("_", " ")}</span>
                  </div>
                  {record.found_value ? <p className="mb-3 break-all rounded bg-slate-50 p-2 font-mono text-xs text-slate-700">{record.found_value}</p> : null}
                  {!record.resolvers?.length ? <p className="text-xs text-slate-500">No per-resolver results.</p> : (
                    <ul className="space-y-2">
                      {record.resolvers.map((resolver, index) => (
                        <li key={`${resolver.resolver}-${index}`} className="grid gap-1 text-xs sm:grid-cols-[180px_120px_1fr]">
                          <span className="font-mono text-slate-600">{resolver.resolver}</span>
                          <span className={resolver.status === "found" ? "text-emerald-700" : resolver.status === "lookup_failed" ? "text-rose-700" : "text-amber-700"}>{resolver.status.replaceAll("_", " ")}</span>
                          {resolver.error ? <span className="break-all text-rose-700">{resolver.error}</span> : resolver.found_value ? <span className="break-all text-slate-600">{resolver.found_value}</span> : <span className="text-slate-400">—</span>}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              ))}
            </div>
          </section>
        </>
      ) : null}
    </div>
  );
}
