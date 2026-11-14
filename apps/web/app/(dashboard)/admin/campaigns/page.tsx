"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type AdminCampaign = {
  id: string;
  workspace_id: string;
  workspace_name: string;
  workspace_slug: string;
  name: string;
  status: string;
  recipient_count: number;
  submitted_count: number;
  bounced_count: number;
  complaint_count: number;
  failed_count: number;
  scheduled_at?: string | null;
  created_at: string;
};

type AdminCampaignResponse = {
  data: AdminCampaign[];
  pagination: { total: number; limit: number; offset: number };
};

const pageSize = 25;

function timeLabel(value?: string | null) {
  if (!value) return "—";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

export default function AdminCampaignsPage() {
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [workspaceInput, setWorkspaceInput] = useState("");
  const [workspaceID, setWorkspaceID] = useState("");
  const [offset, setOffset] = useState(0);
  const [stopCampaign, setStopCampaign] = useState<AdminCampaign | null>(null);
  const [stopReason, setStopReason] = useState("");
  const queryClient = useQueryClient();
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const params = useMemo(() => {
    const query = new URLSearchParams({ limit: String(pageSize), offset: String(offset) });
    if (search) query.set("search", search);
    if (status) query.set("status", status);
    if (workspaceID) query.set("workspace_id", workspaceID);
    return query.toString();
  }, [offset, search, status, workspaceID]);
  const campaignsQuery = useQuery({
    queryKey: ["admin-campaigns", search, status, workspaceID, offset],
    queryFn: () => apiFetch<AdminCampaignResponse>(`/admin/campaigns?${params}`),
    enabled: Boolean(sessionQuery.data?.mfa_verified),
    retry: false,
  });
  const stopMutation = useMutation({
    mutationFn: () => {
      if (!stopCampaign) throw new Error("Select a campaign to stop.");
      return apiFetch<{ status: string }>(`/admin/campaigns/${encodeURIComponent(stopCampaign.id)}/stop`, {
        method: "POST",
        body: JSON.stringify({ reason: stopReason.trim() }),
      });
    },
    onSuccess: async () => {
      setStopCampaign(null);
      setStopReason("");
      await queryClient.invalidateQueries({ queryKey: ["admin-campaigns"] });
    },
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
        <PageHeader eyebrow="Platform admin" title="Campaigns" description="Verify your admin session before viewing campaign metadata." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }
  const result = campaignsQuery.data;

  return (
    <div>
      <PageHeader eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`} title="Campaigns" description="Privacy-safe campaign status and delivery counts across workspaces." />
      <form
        className="mb-4 grid gap-3 rounded-xl border border-slate-200 bg-white p-4 sm:grid-cols-2 xl:grid-cols-[1fr_190px_1fr_auto]"
        onSubmit={(event) => {
          event.preventDefault();
          setOffset(0);
          setSearch(searchInput.trim());
          setWorkspaceID(workspaceInput.trim());
        }}
      >
        <label className="text-sm font-medium text-slate-700">
          Search campaign, workspace, or message ID
          <input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Status
          <select value={status} onChange={(event) => { setStatus(event.target.value); setOffset(0); }} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm">
            <option value="">All statuses</option>
            {["draft", "ready", "scheduled", "processing", "sending", "completed", "paused", "cancelled", "failed"].map((item) => <option key={item} value={item}>{item}</option>)}
          </select>
        </label>
        <label className="text-sm font-medium text-slate-700">
          Workspace ID
          <input value={workspaceInput} onChange={(event) => setWorkspaceInput(event.target.value)} placeholder="Optional UUID" className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
        </label>
        <button type="submit" className="self-end rounded-lg bg-emerald-700 px-4 py-2.5 text-sm font-semibold text-white">Search</button>
      </form>
      {campaignsQuery.error ? <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Unable to load campaigns: {campaignsQuery.error.message}</div> : null}
      {campaignsQuery.isLoading ? <p className="text-sm text-slate-500">Loading campaign metadata...</p> : null}
      {result ? (
        <>
          <div className="mb-3 text-sm text-slate-500">{result.pagination.total} campaign{result.pagination.total === 1 ? "" : "s"}</div>
          <div className="overflow-x-auto rounded-xl border border-slate-200 bg-white">
            <table className="w-full min-w-[1050px] text-left text-sm">
              <thead className="border-b border-slate-200 bg-slate-50 text-xs uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-4 py-3 font-medium">Campaign / workspace</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                  <th className="px-4 py-3 font-medium">Recipients</th>
                  <th className="px-4 py-3 font-medium">Accepted</th>
                  <th className="px-4 py-3 font-medium">Bounced</th>
                  <th className="px-4 py-3 font-medium">Complaints</th>
                  <th className="px-4 py-3 font-medium">Failed</th>
                  <th className="px-4 py-3 font-medium">Created / scheduled</th>
                  {session.platform_role === "super_admin" ? <th className="px-4 py-3 font-medium">Action</th> : null}
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {result.data.length === 0 ? <tr><td colSpan={session.platform_role === "super_admin" ? 9 : 8} className="px-4 py-12 text-center text-slate-500">No campaigns match these filters.</td></tr> : null}
                {result.data.map((campaign) => (
                  <tr key={campaign.id}>
                    <td className="px-4 py-3">
                      <p className="font-medium text-slate-900">{campaign.name}</p>
                      <Link href={`/admin/workspaces/${encodeURIComponent(campaign.workspace_id)}`} className="mt-1 inline-block text-xs text-emerald-800 hover:underline">
                        {campaign.workspace_name} · /{campaign.workspace_slug}
                      </Link>
                      <p className="mt-1 break-all font-mono text-[10px] text-slate-400">{campaign.id}</p>
                    </td>
                    <td className="px-4 py-3"><span className="rounded-full bg-slate-100 px-2 py-1 text-xs text-slate-700">{campaign.status}</span></td>
                    <td className="px-4 py-3">{campaign.recipient_count.toLocaleString()}</td>
                    <td className="px-4 py-3">{campaign.submitted_count.toLocaleString()}</td>
                    <td className="px-4 py-3">{campaign.bounced_count.toLocaleString()}</td>
                    <td className="px-4 py-3">{campaign.complaint_count.toLocaleString()}</td>
                    <td className="px-4 py-3">{campaign.failed_count.toLocaleString()}</td>
                    <td className="px-4 py-3 text-xs text-slate-600">{timeLabel(campaign.created_at)}{campaign.scheduled_at ? <p className="mt-1 text-slate-500">Scheduled {timeLabel(campaign.scheduled_at)}</p> : null}</td>
                    {session.platform_role === "super_admin" ? (
                      <td className="px-4 py-3">
                        {["scheduled", "processing", "sending", "paused"].includes(campaign.status) ? (
                          <button
                            type="button"
                            onClick={() => { setStopCampaign(campaign); setStopReason(""); stopMutation.reset(); }}
                            className="rounded-lg border border-rose-200 px-3 py-1.5 text-xs font-medium text-rose-700 hover:bg-rose-50"
                          >
                            Stop
                          </button>
                        ) : <span className="text-xs text-slate-400">—</span>}
                      </td>
                    ) : null}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <button type="button" disabled={offset === 0 || campaignsQuery.isFetching} onClick={() => setOffset(Math.max(0, offset - pageSize))} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Previous</button>
            <span className="text-xs text-slate-500">Showing {result.data.length ? offset + 1 : 0}–{offset + result.data.length} of {result.pagination.total}</span>
            <button type="button" disabled={offset + pageSize >= result.pagination.total || campaignsQuery.isFetching} onClick={() => setOffset(offset + pageSize)} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Next</button>
          </div>
        </>
      ) : null}
      {stopCampaign ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/40 p-4" role="presentation">
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby="stop-campaign-title"
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                setStopCampaign(null);
                stopMutation.reset();
              }
            }}
            className="w-full max-w-lg rounded-xl bg-white p-5 shadow-xl"
          >
            <h2 id="stop-campaign-title" className="text-lg font-semibold text-slate-900">Stop campaign?</h2>
            <p className="mt-2 text-sm text-slate-600">
              This cancels <span className="font-medium">{stopCampaign.name}</span>, cancels queued deliveries, and skips pending recipients. The action is permanent and audited.
            </p>
            <label className="mt-4 block text-sm font-medium text-slate-700">
              Reason (required)
              <textarea autoFocus value={stopReason} onChange={(event) => setStopReason(event.target.value)} maxLength={500} rows={3} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
            </label>
            {stopMutation.error ? <p role="alert" className="mt-3 text-sm text-rose-700">{stopMutation.error.message}</p> : null}
            <div className="mt-4 flex justify-end gap-2">
              <button type="button" onClick={() => { setStopCampaign(null); stopMutation.reset(); }} className="rounded-lg px-4 py-2 text-sm text-slate-700 ring-1 ring-slate-200">Cancel</button>
              <button type="button" onClick={() => stopMutation.mutate()} disabled={!stopReason.trim() || stopMutation.isPending} className="rounded-lg bg-rose-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">
                {stopMutation.isPending ? "Stopping..." : "Stop campaign"}
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </div>
  );
}
