"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type AdminOverview = {
  user_counts: {
    total: number;
    active: number;
    pending: number;
    suspended: number;
    closed: number;
  };
  workspace_count: number;
  operational_metrics: {
    domain_count: number;
    campaign_count: number;
    messages_today: number;
    messages_7_days: number;
    messages_30_days: number;
    delivery_rate_30_days: number;
    bounce_rate_30_days: number;
    complaint_rate_30_days: number;
    queue_depth: number;
    updated_at: string;
  };
  recent_signups: Array<{
    id: string;
    name: string;
    email: string;
    status: string;
    created_at: string;
  }>;
  recent_admin_actions: Array<{
    id: number;
    actor_role: string;
    action: string;
    target_type?: string;
    target_id?: string;
    reason: string;
    created_at: string;
  }>;
};

const dateLabel = (value: string) =>
  new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));

export default function AdminOverviewPage() {
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const overviewQuery = useQuery({
    queryKey: ["admin-overview"],
    queryFn: () => apiFetch<AdminOverview>("/admin/overview"),
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
        <PageHeader eyebrow="Platform admin" title="Admin verification" description="Verify your authenticator before viewing platform metrics." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }

  const overview = overviewQuery.data;
  const cards: Array<[string, string | number]> = overview ? [
    ["Users", overview.user_counts.total],
    ["Active users", overview.user_counts.active],
    ["Pending verification", overview.user_counts.pending],
    ["Suspended users", overview.user_counts.suspended],
    ["Closed users", overview.user_counts.closed],
    ["Workspaces", overview.workspace_count],
    ["Sending domains", overview.operational_metrics.domain_count],
    ["Campaigns", overview.operational_metrics.campaign_count],
    ["Messages today", overview.operational_metrics.messages_today],
    ["Messages · 7 days", overview.operational_metrics.messages_7_days],
    ["Messages · 30 days", overview.operational_metrics.messages_30_days],
    ["Delivery · 30 days", `${overview.operational_metrics.delivery_rate_30_days.toFixed(1)}%`],
    ["Bounces · 30 days", `${overview.operational_metrics.bounce_rate_30_days.toFixed(2)}%`],
    ["Complaints · 30 days", `${overview.operational_metrics.complaint_rate_30_days.toFixed(3)}%`],
    ["Delivery queue depth", overview.operational_metrics.queue_depth],
  ] : [];

  return (
    <div>
      <PageHeader eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`} title="Overview" description="Live account, workspace, campaign, domain, and delivery metrics from their owning services." />
      {overviewQuery.error ? <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Unable to load platform overview: {overviewQuery.error.message}</div> : null}
      {overviewQuery.isLoading ? <p className="text-sm text-slate-500">Loading live platform counts...</p> : null}
      {overview ? (
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5">
            {cards.map(([label, count]) => (
              <Card key={label}><CardContent className="p-4">
                <p className="text-sm text-slate-500">{label}</p>
                <p className="mt-2 text-2xl font-semibold text-slate-900">{typeof count === "number" ? count.toLocaleString() : count}</p>
              </CardContent></Card>
            ))}
          </div>
          <p className="mt-4 rounded-lg border border-slate-200 bg-white p-3 text-xs text-slate-600">
            Delivery rates use terminal campaign and transactional outcomes from the last 30 days. Queue depth is read from the API database. Stalwart, NATS, worker health, and top-sender rankings are not included yet.
          </p>
          <div className="mt-5 grid gap-5 xl:grid-cols-2">
            <Card>
              <CardContent className="p-4">
                <div className="mb-3 flex items-center justify-between">
                  <h2 className="text-sm font-semibold text-slate-900">Recent signups</h2>
                  <Link href="/admin/users" className="text-xs font-medium text-emerald-800 hover:underline">View users</Link>
                </div>
                <ul className="divide-y divide-slate-100">
                  {overview.recent_signups.map((user) => (
                    <li key={user.id} className="flex items-center justify-between gap-3 py-3">
                      <div className="min-w-0">
                        <Link href={`/admin/users/${encodeURIComponent(user.id)}`} className="block truncate text-sm font-medium text-slate-800 hover:underline">{user.name || "Unnamed user"}</Link>
                        <p className="truncate text-xs text-slate-500">{user.email}</p>
                      </div>
                      <span className="shrink-0 text-xs text-slate-500">{dateLabel(user.created_at)}</span>
                    </li>
                  ))}
                  {overview.recent_signups.length === 0 ? <li className="py-6 text-center text-sm text-slate-500">No signups found.</li> : null}
                </ul>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4">
                <div className="mb-3 flex items-center justify-between">
                  <h2 className="text-sm font-semibold text-slate-900">Recent admin actions</h2>
                  <Link href="/admin/audit" className="text-xs font-medium text-emerald-800 hover:underline">Audit log</Link>
                </div>
                <ul className="divide-y divide-slate-100">
                  {overview.recent_admin_actions.map((item) => (
                    <li key={item.id} className="py-3">
                      <p className="text-sm font-medium text-slate-800">{item.action.replaceAll("_", " ")}</p>
                      <p className="mt-1 text-xs text-slate-600">{item.actor_role.replaceAll("_", " ")} · {item.target_type || "platform"} {item.target_id || ""}</p>
                      <p className="mt-1 text-xs text-slate-500">{item.reason} · {dateLabel(item.created_at)}</p>
                    </li>
                  ))}
                  {overview.recent_admin_actions.length === 0 ? <li className="py-6 text-center text-sm text-slate-500">No admin actions recorded.</li> : null}
                </ul>
              </CardContent>
            </Card>
          </div>
        </>
      ) : null}
    </div>
  );
}
