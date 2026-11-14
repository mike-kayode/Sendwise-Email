"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
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
  quota_usage: Record<string, number>;
};

type AdminDomainResponse = {
  data: AdminDomain[];
  pagination: { total: number; limit: number; offset: number };
};

const pageSize = 25;

function timeLabel(value?: string | null) {
  if (!value) return "Never";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function StatusBadge({ value }: { value: string }) {
  const tone = value === "verified" || value === "authenticated"
    ? "bg-emerald-50 text-emerald-800"
    : value === "pending" || value === "unknown"
      ? "bg-amber-50 text-amber-800"
      : "bg-rose-50 text-rose-800";
  return <span className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${tone}`}>{value.replaceAll("_", " ")}</span>;
}

export default function AdminDomainsPage() {
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [nearLimit, setNearLimit] = useState(false);
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    setNearLimit(new URLSearchParams(window.location.search).get("near_limit") === "true");
  }, []);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const params = useMemo(() => {
    const query = new URLSearchParams({ limit: String(pageSize), offset: String(offset) });
    if (search) query.set("search", search);
    if (status) query.set("status", status);
    if (nearLimit) query.set("near_limit", "true");
    return query.toString();
  }, [nearLimit, offset, search, status]);
  const domainsQuery = useQuery({
    queryKey: ["admin-domains", search, status, nearLimit, offset],
    queryFn: () => apiFetch<AdminDomainResponse>(`/admin/domains?${params}`),
    enabled: Boolean(sessionQuery.data?.mfa_verified),
    retry: false,
  });

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
        <PageHeader eyebrow="Platform admin" title="Sending domains" description="Verify your admin session before viewing domain metadata." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }
  const result = domainsQuery.data;

  return (
    <div>
      <PageHeader eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`} title="Sending domains" description="Global domain ownership and DNS verification metadata from the sending API." />
      <form
        className="mb-4 grid gap-3 rounded-xl border border-slate-200 bg-white p-4 sm:grid-cols-[1fr_200px_1fr_auto]"
        onSubmit={(event) => {
          event.preventDefault();
          setOffset(0);
          setSearch(searchInput.trim());
        }}
      >
        <label className="text-sm font-medium text-slate-700">
          Search domains and workspaces
          <input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Domain status
          <select value={status} onChange={(event) => { setStatus(event.target.value); setOffset(0); }} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm">
            <option value="">All statuses</option>
            <option value="not_configured">Not configured</option>
            <option value="pending">Pending</option>
            <option value="verified">Verified</option>
            <option value="misconfigured">Misconfigured</option>
          </select>
        </label>
        <label className="flex items-center gap-2 self-end pb-2 text-sm text-slate-700">
          <input type="checkbox" checked={nearLimit} onChange={(event) => { setNearLimit(event.target.checked); setOffset(0); }} />
          Near quota limit (&gt;80%)
        </label>
        <button type="submit" className="self-end rounded-lg bg-emerald-700 px-4 py-2.5 text-sm font-semibold text-white">Search</button>
      </form>
      {domainsQuery.error ? <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Unable to load domains: {domainsQuery.error.message}</div> : null}
      {domainsQuery.isLoading ? <p className="text-sm text-slate-500">Loading sending domains...</p> : null}
      {result ? (
        <>
          <div className="mb-3 text-sm text-slate-500">{result.pagination.total} domain{result.pagination.total === 1 ? "" : "s"}</div>
          <div className="overflow-x-auto rounded-xl border border-slate-200 bg-white">
            <table className="w-full min-w-[980px] text-left text-sm">
              <thead className="border-b border-slate-200 bg-slate-50 text-xs uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-4 py-3 font-medium">Domain / workspace</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                  <th className="px-4 py-3 font-medium">SPF</th>
                  <th className="px-4 py-3 font-medium">DKIM</th>
                  <th className="px-4 py-3 font-medium">DMARC</th>
                  <th className="px-4 py-3 font-medium">Ownership</th>
                  <th className="px-4 py-3 font-medium">Marketing quota</th>
                  <th className="px-4 py-3 font-medium">Transactional quota</th>
                  <th className="px-4 py-3 font-medium">Last checked</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {result.data.length === 0 ? <tr><td colSpan={9} className="px-4 py-12 text-center text-slate-500">No sending domains match these filters.</td></tr> : null}
                {result.data.map((domain) => (
                  <tr key={domain.id}>
                    <td className="px-4 py-3">
                      <Link href={`/admin/domains/${encodeURIComponent(domain.id)}`} className="font-medium text-emerald-800 hover:underline">{domain.domain}</Link>
                      <Link href={`/admin/workspaces/${encodeURIComponent(domain.workspace_id)}`} className="mt-1 inline-block text-xs text-emerald-800 hover:underline">
                        {domain.workspace_name} · /{domain.workspace_slug}
                      </Link>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge value={domain.status} />
                      {domain.admin_disabled_at ? <p className="mt-1 text-xs font-semibold text-rose-700">Platform disabled</p> : null}
                    </td>
                    <td className="px-4 py-3"><StatusBadge value={domain.spf_status} /></td>
                    <td className="px-4 py-3"><StatusBadge value={domain.dkim_status} /></td>
                    <td className="px-4 py-3"><StatusBadge value={domain.dmarc_status} /></td>
                    <td className="px-4 py-3">
                      {domain.owner_conflict ? <span className="text-xs font-semibold text-rose-700">Owner conflict</span> : <span className="text-xs text-slate-600">Workspace-owned</span>}
                      {domain.transactional_enabled ? <p className="mt-1 text-xs text-slate-500">Transactional enabled</p> : null}
                    </td>
                    <td className="px-4 py-3 text-xs font-medium text-slate-700">{(domain.quota_usage?.marketing ?? 0).toFixed(0)}%</td>
                    <td className="px-4 py-3 text-xs font-medium text-slate-700">{(domain.quota_usage?.transactional ?? 0).toFixed(0)}%</td>
                    <td className="px-4 py-3 text-xs text-slate-600">{timeLabel(domain.last_checked_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <button type="button" disabled={offset === 0 || domainsQuery.isFetching} onClick={() => setOffset(Math.max(0, offset - pageSize))} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Previous</button>
            <p className="text-xs text-slate-500">{result.pagination.total === 0 ? 0 : offset + 1}–{Math.min(offset + pageSize, result.pagination.total)} of {result.pagination.total}</p>
            <button type="button" disabled={offset + pageSize >= result.pagination.total || domainsQuery.isFetching} onClick={() => setOffset(offset + pageSize)} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Next</button>
          </div>
        </>
      ) : null}
    </div>
  );
}
