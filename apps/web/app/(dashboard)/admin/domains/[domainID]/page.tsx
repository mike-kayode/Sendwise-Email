"use client";

import Link from "next/link";
import { notFound, useParams, useRouter } from "next/navigation";
import { useState } from "react";
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
