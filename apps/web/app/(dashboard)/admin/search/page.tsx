"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type SearchPage<T> = {
  data: T[];
  pagination: { total: number };
};

type UserResult = {
  id: string;
  name: string;
  email: string;
  status: string;
  platform_role: string;
};

type WorkspaceResult = {
  id: string;
  name: string;
  slug: string;
  member_count: number;
};

type DomainResult = {
  id: string;
  workspace_id: string;
  workspace_name: string;
  domain: string;
  status: string;
};

type CampaignResult = {
  id: string;
  workspace_id: string;
  workspace_name: string;
  name: string;
  status: string;
};

type TransactionalMessageResult = {
  id: string;
  message_id: string;
  workspace_id: string;
  workspace_name: string;
  status: string;
};

type SearchResults = {
  users: SearchPage<UserResult>;
  workspaces: SearchPage<WorkspaceResult>;
  domains: SearchPage<DomainResult>;
  campaigns: SearchPage<CampaignResult>;
  transactionalMessages: SearchPage<TransactionalMessageResult>;
};

async function searchPlatform(value: string): Promise<SearchResults> {
  const params = new URLSearchParams({ search: value, limit: "5", offset: "0" });
  const query = params.toString();
  const [users, workspaces, domains, campaigns, transactionalMessages] = await Promise.all([
    apiFetch<SearchPage<UserResult>>(`/admin/users?${query}`),
    apiFetch<SearchPage<WorkspaceResult>>(`/admin/workspaces?${query}`),
    apiFetch<SearchPage<DomainResult>>(`/admin/domains?${query}`),
    apiFetch<SearchPage<CampaignResult>>(`/admin/campaigns?${query}`),
    apiFetch<SearchPage<TransactionalMessageResult>>(`/admin/transactional-messages?${query}`),
  ]);
  return { users, workspaces, domains, campaigns, transactionalMessages };
}

function ResultGroup({
  title,
  total,
  empty,
  children,
}: {
  title: string;
  total: number;
  empty: boolean;
  children: ReactNode;
}) {
  return (
    <section className="rounded-xl border border-slate-200 bg-white">
      <div className="flex items-center justify-between border-b border-slate-100 px-4 py-3">
        <h2 className="text-sm font-semibold text-slate-900">{title}</h2>
        <span className="text-xs text-slate-500">{total.toLocaleString()} match{total === 1 ? "" : "es"}</span>
      </div>
      {empty ? <p className="px-4 py-6 text-sm text-slate-500">No matches.</p> : <ul className="divide-y divide-slate-100">{children}</ul>}
    </section>
  );
}

export default function AdminSearchPage() {
  const [input, setInput] = useState("");
  const [search, setSearch] = useState("");
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const searchQuery = useQuery({
    queryKey: ["admin-global-search", search],
    queryFn: () => searchPlatform(search),
    enabled: Boolean(sessionQuery.data?.mfa_verified && search.length >= 2),
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
        <PageHeader eyebrow="Platform admin" title="Search" description="Verify your admin session before searching platform metadata." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }
  const results = searchQuery.data;
  const resultCount = results
    ? results.users.pagination.total + results.workspaces.pagination.total + results.domains.pagination.total +
      results.campaigns.pagination.total + results.transactionalMessages.pagination.total
    : 0;

  return (
    <div>
      <PageHeader eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`} title="Search everywhere" description="Search user email, workspace, sending domain, campaign, or transactional message identifiers." />
      <form className="mb-4 rounded-xl border border-slate-200 bg-white p-4" onSubmit={(event) => { event.preventDefault(); setSearch(input.trim()); }}>
        <label className="block text-sm font-medium text-slate-700">
          Search term
          <div className="mt-1 flex gap-2">
            <input value={input} onChange={(event) => setInput(event.target.value)} minLength={2} maxLength={100} className="h-10 min-w-0 flex-1 rounded-lg border border-slate-200 px-3 text-sm" placeholder="Email, workspace, domain, campaign ID, or message ID" />
            <button type="submit" disabled={input.trim().length < 2} className="rounded-lg bg-emerald-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">Search</button>
          </div>
        </label>
      </form>
      {search.length < 2 ? <p className="text-sm text-slate-500">Enter at least two characters to search.</p> : null}
      {searchQuery.isLoading ? <p className="text-sm text-slate-500">Searching platform records...</p> : null}
      {searchQuery.error ? <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Search failed: {searchQuery.error.message}</div> : null}
      {results ? (
        <>
          <p className="mb-3 text-sm text-slate-500">{resultCount.toLocaleString()} total matching records (showing up to five per category).</p>
          <div className="grid gap-4 xl:grid-cols-2">
            <ResultGroup title="Users" total={results.users.pagination.total} empty={results.users.data.length === 0}>
              {results.users.data.map((user) => (
                <li key={user.id} className="flex items-center justify-between gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <Link href={`/admin/users/${encodeURIComponent(user.id)}`} className="block truncate text-sm font-medium text-emerald-800 hover:underline">{user.name || "Unnamed user"}</Link>
                    <p className="truncate text-xs text-slate-500">{user.email}</p>
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">{user.status}</span>
                </li>
              ))}
            </ResultGroup>
            <ResultGroup title="Workspaces" total={results.workspaces.pagination.total} empty={results.workspaces.data.length === 0}>
              {results.workspaces.data.map((workspace) => (
                <li key={workspace.id} className="flex items-center justify-between gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <Link href={`/admin/workspaces/${encodeURIComponent(workspace.id)}`} className="block truncate text-sm font-medium text-emerald-800 hover:underline">{workspace.name}</Link>
                    <p className="truncate text-xs text-slate-500">/{workspace.slug}</p>
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">{workspace.member_count} members</span>
                </li>
              ))}
            </ResultGroup>
            <ResultGroup title="Sending domains" total={results.domains.pagination.total} empty={results.domains.data.length === 0}>
              {results.domains.data.map((domain) => (
                <li key={domain.id} className="flex items-center justify-between gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <Link href={`/admin/domains/${encodeURIComponent(domain.id)}`} className="block truncate text-sm font-medium text-emerald-800 hover:underline">{domain.domain}</Link>
                    <p className="truncate text-xs text-slate-500">{domain.workspace_name}</p>
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">{domain.status}</span>
                </li>
              ))}
            </ResultGroup>
            <ResultGroup title="Campaigns / messages" total={results.campaigns.pagination.total} empty={results.campaigns.data.length === 0}>
              {results.campaigns.data.map((campaign) => (
                <li key={campaign.id} className="flex items-center justify-between gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <Link href="/admin/campaigns" className="block truncate text-sm font-medium text-emerald-800 hover:underline">{campaign.name}</Link>
                    <p className="truncate text-xs text-slate-500">{campaign.workspace_name} · {campaign.id}</p>
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">{campaign.status}</span>
                </li>
              ))}
            </ResultGroup>
            <ResultGroup title="Transactional messages" total={results.transactionalMessages.pagination.total} empty={results.transactionalMessages.data.length === 0}>
              {results.transactionalMessages.data.map((message) => (
                <li key={message.id} className="flex items-center justify-between gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <p className="break-all font-mono text-xs font-medium text-slate-800">{message.message_id}</p>
                    <Link href={`/admin/workspaces/${encodeURIComponent(message.workspace_id)}`} className="mt-1 inline-block truncate text-xs text-emerald-800 hover:underline">{message.workspace_name}</Link>
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">{message.status}</span>
                </li>
              ))}
            </ResultGroup>
          </div>
          <p className="mt-4 text-xs text-slate-500">Search results show metadata only. Transactional message identifiers may be shown; message content and recipient details are never displayed.</p>
        </>
      ) : null}
    </div>
  );
}
