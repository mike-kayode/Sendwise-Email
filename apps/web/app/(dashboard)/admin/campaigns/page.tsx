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

type CampaignDeliveryDiagnostics = {
  status_counts: Record<string, number>;
  common_errors: { error: string; count: number }[];
  oldest_queued_at: string | null;
  stuck_count: number;
  oldest_unresolved: {
    job_id: string;
    stage: string;
    last_event_at: string;
    remote_host: string;
    smtp_code?: number;
    enhanced_code: string;
    last_error: string;
  }[];
};

type JourneyBackfillResult = {
  counts: Record<string, number>;
  processed: number;
  next_cursor?: string;
  has_more: boolean;
  dry_run: boolean;
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
  const [deliveryCampaign, setDeliveryCampaign] = useState<AdminCampaign | null>(null);
  const [deliveryReason, setDeliveryReason] = useState("");
  const [deliveryMFACode, setDeliveryMFACode] = useState("");
  const [backfillReason, setBackfillReason] = useState("");
  const [backfillMFACode, setBackfillMFACode] = useState("");
  const [backfillCursor, setBackfillCursor] = useState("");
  const [backfillPreviewCursor, setBackfillPreviewCursor] = useState<string | null>(null);
  const [backfillResult, setBackfillResult] = useState<JourneyBackfillResult | null>(null);
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
  const deliveryQuery = useQuery({
    queryKey: ["admin-campaign-delivery", deliveryCampaign?.id],
    queryFn: () => apiFetch<{ data: CampaignDeliveryDiagnostics }>(`/admin/campaigns/${encodeURIComponent(deliveryCampaign!.id)}/delivery`),
    enabled: Boolean(deliveryCampaign && sessionQuery.data?.mfa_verified),
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
  const deliveryActionMutation = useMutation({
    mutationFn: async (action: "resume-deferred" | "replay-dead-letter") => {
      if (!deliveryCampaign) throw new Error("Select a campaign first.");
      await apiFetch("/admin/mfa/verify", { method: "POST", body: JSON.stringify({ code: deliveryMFACode.trim() }) });
      return apiFetch<{ count: number }>(`/admin/campaigns/${encodeURIComponent(deliveryCampaign.id)}/${action}`, {
        method: "POST",
        body: JSON.stringify({ reason: deliveryReason.trim() }),
      });
    },
    onSuccess: async () => {
      setDeliveryReason("");
      setDeliveryMFACode("");
      await deliveryQuery.refetch();
      await queryClient.invalidateQueries({ queryKey: ["admin-campaigns"] });
    },
  });
  const backfillMutation = useMutation({
    mutationFn: async ({ dryRun, afterJobID }: { dryRun: boolean; afterJobID: string }) => {
      await apiFetch("/admin/mfa/verify", { method: "POST", body: JSON.stringify({ code: backfillMFACode.trim() }) });
      return apiFetch<{ data: JourneyBackfillResult }>("/admin/delivery-journey/backfill", {
        method: "POST",
        body: JSON.stringify({
          after_job_id: afterJobID,
          limit: 100,
          dry_run: dryRun,
          reason: backfillReason.trim(),
        }),
      });
    },
    onSuccess: ({ data }, variables) => {
      setBackfillResult(data);
      if (variables.dryRun) setBackfillPreviewCursor(variables.afterJobID);
      else {
        setBackfillCursor(data.next_cursor ?? "");
        setBackfillPreviewCursor(null);
      }
      setBackfillMFACode("");
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
      {session.platform_role === "super_admin" ? (
        <section className="mb-5 rounded-xl border border-slate-200 bg-white p-4">
          <h2 className="text-sm font-semibold text-slate-900">Submitted-message journey backfill</h2>
          <p className="mt-1 text-xs text-slate-600">Reconcile up to 100 submitted jobs per batch against Stalwart. Dry run is read-only. Continue with the returned cursor to resume; no messages are resent.</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <label className="text-xs font-medium text-slate-700">Required reason
              <textarea value={backfillReason} onChange={(event) => setBackfillReason(event.target.value)} maxLength={500} rows={2} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
            </label>
            <label className="text-xs font-medium text-slate-700">Authenticator or recovery code
              <input value={backfillMFACode} onChange={(event) => setBackfillMFACode(event.target.value)} autoComplete="one-time-code" className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
            </label>
          </div>
          {backfillMutation.error ? <p role="alert" className="mt-3 text-sm text-rose-700">{backfillMutation.error.message}</p> : null}
          <div className="mt-3 flex flex-wrap gap-2">
            <button type="button" onClick={() => backfillMutation.mutate({ dryRun: true, afterJobID: backfillCursor })} disabled={!backfillReason.trim() || !backfillMFACode.trim() || backfillMutation.isPending || backfillPreviewCursor !== null} className="rounded-lg border border-slate-300 px-3 py-2 text-xs font-semibold text-slate-700 disabled:opacity-50">
              {backfillMutation.isPending ? "Working..." : "Preview next batch"}
            </button>
            <button type="button" onClick={() => {
              const afterJobID = backfillPreviewCursor ?? backfillCursor;
              if (window.confirm("Apply this batch? It records reconciled journey states and never resends messages.")) backfillMutation.mutate({ dryRun: false, afterJobID });
            }} disabled={!backfillReason.trim() || !backfillMFACode.trim() || backfillMutation.isPending} className="rounded-lg bg-emerald-700 px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">
              Apply next batch
            </button>
            <button type="button" onClick={() => { setBackfillCursor(""); setBackfillPreviewCursor(null); setBackfillResult(null); }} className="rounded-lg px-3 py-2 text-xs font-semibold text-slate-600 ring-1 ring-slate-200">Restart cursor</button>
          </div>
          {backfillResult ? (
            <div className="mt-3 rounded-lg bg-slate-50 p-3 text-xs text-slate-700">
              <p>{backfillResult.dry_run ? "Preview" : "Applied"} {backfillResult.processed} jobs · {backfillResult.has_more ? "more batches available" : "complete"}</p>
              <p className="mt-1">{Object.entries(backfillResult.counts).map(([stage, count]) => `${stage}: ${count.toLocaleString()}`).join(" · ") || "No jobs in this batch."}</p>
              {backfillResult.next_cursor ? <p className="mt-1 break-all font-mono text-[10px]">Resume cursor: {backfillResult.next_cursor}</p> : null}
            </div>
          ) : null}
        </section>
      ) : null}
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
                      <button type="button" onClick={() => { setDeliveryCampaign(campaign); setDeliveryReason(""); setDeliveryMFACode(""); deliveryActionMutation.reset(); }} className="mt-1 text-xs font-medium text-emerald-800 underline">
                        Delivery jobs
                      </button>
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
      {deliveryCampaign ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/40 p-4" role="presentation">
          <section role="dialog" aria-modal="true" aria-labelledby="campaign-delivery-title" className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-xl bg-white p-5 shadow-xl">
            <div className="flex items-start justify-between gap-3">
              <div>
                <h2 id="campaign-delivery-title" className="text-lg font-semibold text-slate-900">Delivery jobs</h2>
                <p className="mt-1 text-sm text-slate-600">{deliveryCampaign.name}</p>
              </div>
              <button type="button" onClick={() => setDeliveryCampaign(null)} className="rounded-lg px-3 py-1.5 text-sm text-slate-700 ring-1 ring-slate-200">Close</button>
            </div>
            {deliveryQuery.isLoading ? <p className="mt-4 text-sm text-slate-500">Loading delivery diagnostics...</p> : null}
            {deliveryQuery.error ? <p role="alert" className="mt-4 text-sm text-rose-700">Diagnostics unavailable: {deliveryQuery.error.message}</p> : null}
            {deliveryQuery.data ? (
              <div className="mt-4 space-y-4">
                <div className="grid gap-2 sm:grid-cols-3">
                  {Object.entries(deliveryQuery.data.data.status_counts).map(([jobStatus, count]) => (
                    <div key={jobStatus} className="rounded-lg bg-slate-50 p-3 text-sm">
                      <span className="capitalize text-slate-500">{jobStatus}</span>
                      <span className="ml-2 font-semibold text-slate-900">{count.toLocaleString()}</span>
                    </div>
                  ))}
                </div>
                <p className="text-xs text-slate-600">Oldest queued job: {timeLabel(deliveryQuery.data.data.oldest_queued_at)}</p>
                <section className="rounded-lg border border-amber-200 bg-amber-50 p-3">
                  <h3 className="text-sm font-semibold text-amber-950">Stuck messages · {deliveryQuery.data.data.stuck_count.toLocaleString()}</h3>
                  <p className="mt-1 text-xs text-amber-900">Submitted to Stalwart with no journey update beyond the configured alert threshold. This does not mean the recipient server accepted the message.</p>
                  {deliveryQuery.data.data.oldest_unresolved.length ? (
                    <ul className="mt-3 space-y-2">
                      {deliveryQuery.data.data.oldest_unresolved.map((entry) => (
                        <li key={entry.job_id} className="break-all border-t border-amber-200 pt-2 text-xs text-amber-950">
                          <span className="font-medium">{entry.stage}</span> · {timeLabel(entry.last_event_at)}
                          {entry.remote_host ? <p className="mt-1">Remote host: {entry.remote_host}</p> : null}
                          {entry.smtp_code || entry.enhanced_code ? <p className="mt-1 font-mono">SMTP {entry.smtp_code ?? "—"} {entry.enhanced_code}</p> : null}
                          {entry.last_error ? <p className="mt-1">{entry.last_error}</p> : null}
                          <p className="font-mono text-[10px] text-amber-800">{entry.job_id}</p>
                        </li>
                      ))}
                    </ul>
                  ) : <p className="mt-2 text-xs text-amber-900">No unresolved messages are currently past the threshold.</p>}
                </section>
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-500">Common delivery errors</h3>
                  {deliveryQuery.data.data.common_errors.length ? (
                    <ul className="mt-2 space-y-1 text-xs text-slate-700">
                      {deliveryQuery.data.data.common_errors.map((entry) => <li key={entry.error}>{entry.count.toLocaleString()} × {entry.error}</li>)}
                    </ul>
                  ) : <p className="mt-2 text-xs text-slate-500">No delivery errors recorded.</p>}
                </div>
                {session.platform_role === "super_admin" ? (
                  <div className="border-t border-slate-100 pt-4">
                    <label className="block text-xs font-medium text-slate-700">Required reason
                      <textarea value={deliveryReason} onChange={(event) => setDeliveryReason(event.target.value)} maxLength={500} rows={2} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
                    </label>
                    <label className="mt-3 block text-xs font-medium text-slate-700">Authenticator or recovery code
                      <input value={deliveryMFACode} onChange={(event) => setDeliveryMFACode(event.target.value)} autoComplete="one-time-code" className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
                    </label>
                    {deliveryActionMutation.error ? <p role="alert" className="mt-3 text-sm text-rose-700">{deliveryActionMutation.error.message}</p> : null}
                    {deliveryActionMutation.data ? <p role="status" className="mt-3 text-sm text-emerald-800">{deliveryActionMutation.data.count.toLocaleString()} jobs updated.</p> : null}
                    <div className="mt-3 flex flex-wrap gap-2">
                      <button type="button" disabled={!deliveryReason.trim() || !deliveryMFACode.trim() || deliveryActionMutation.isPending} onClick={() => { if (window.confirm("Make deferred jobs for this sending campaign due now? This action is audited.")) deliveryActionMutation.mutate("resume-deferred"); }} className="rounded-lg bg-emerald-700 px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">Resume deferred jobs</button>
                      <button type="button" disabled={!deliveryReason.trim() || !deliveryMFACode.trim() || deliveryActionMutation.isPending} onClick={() => { if (window.confirm("Replay up to 1,000 unsubmitted dead-lettered jobs? Replay is idempotent and audited.")) deliveryActionMutation.mutate("replay-dead-letter"); }} className="rounded-lg bg-amber-700 px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">Replay dead-lettered jobs</button>
                    </div>
                  </div>
                ) : <p className="border-t border-slate-100 pt-3 text-xs text-slate-500">Support admins can inspect diagnostics but cannot change delivery jobs.</p>}
              </div>
            ) : null}
          </section>
        </div>
      ) : null}
    </div>
  );
}
