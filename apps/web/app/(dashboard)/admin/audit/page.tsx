"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type AuditRecord = {
  id: number;
  actor_user_id?: string;
  actor_role: string;
  action: string;
  target_type?: string;
  target_id?: string;
  reason: string;
  request_id?: string;
  created_at: string;
};

type AuditResponse = {
  data: AuditRecord[];
  limit: number;
  offset: number;
  total: number;
};

const pageSize = 50;

export default function AdminAuditPage() {
  const [offset, setOffset] = useState(0);
  const [action, setAction] = useState("");
  const [actorID, setActorID] = useState("");
  const [exportReason, setExportReason] = useState("");
  const [exportError, setExportError] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const auditQuery = useQuery({
    queryKey: ["admin-audit", action, actorID, offset],
    queryFn: () => {
      const params = new URLSearchParams({
        limit: String(pageSize),
        offset: String(offset),
      });
      if (action.trim()) params.set("action", action.trim());
      if (actorID.trim()) params.set("actor_user_id", actorID.trim());
      return apiFetch<AuditResponse>(`/admin/audit?${params.toString()}`);
    },
    enabled: Boolean(sessionQuery.data?.mfa_verified),
  });

  if (sessionQuery.isLoading) {
    return <p className="text-sm text-slate-500">Checking platform admin access...</p>;
  }
  if (sessionQuery.error instanceof ApiError && sessionQuery.error.code === "admin_forbidden") {
    notFound();
  }
  if (sessionQuery.error) {
    return (
      <div role="alert" className="rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">
        Admin access could not be verified: {sessionQuery.error.message}
      </div>
    );
  }
  const session = sessionQuery.data;
  if (!session) return null;

  const exportAudit = async () => {
    if (!exportReason.trim() || exporting) return;
    setExporting(true);
    setExportError(null);
    try {
      const result = await apiFetch<{ filename: string; csv: string }>("/admin/audit/export", {
        method: "POST",
        body: JSON.stringify({
          reason: exportReason.trim(),
          action: action.trim(),
          actor_user_id: actorID.trim(),
          limit: 1000,
          offset: 0,
        }),
      });
      const url = URL.createObjectURL(new Blob([result.csv], { type: "text/csv;charset=utf-8" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = result.filename;
      link.click();
      URL.revokeObjectURL(url);
      setExportReason("");
    } catch (error) {
      setExportError(error instanceof Error ? error.message : "The audit log could not be exported.");
    } finally {
      setExporting(false);
    }
  };

  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="Audit log" description="Verify your admin session before viewing audit events." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">
          Continue to admin verification
        </Link>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title="Audit log"
        description="Append-only administrative actions. Sensitive customer content is not included."
      />
      {auditQuery.error ? (
        <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">
          Unable to load the audit log: {auditQuery.error.message}
        </div>
      ) : null}
      <Card>
        <CardContent className="p-4">
          {session.platform_role === "super_admin" ? (
            <div className="mb-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
              <form
                className="flex flex-col gap-3 sm:flex-row"
                onSubmit={(event) => {
                  event.preventDefault();
                  void exportAudit();
                }}
              >
                <input
                  aria-label="Audit export reason"
                  placeholder="Reason required to export audit metadata"
                  value={exportReason}
                  onChange={(event) => setExportReason(event.target.value)}
                  maxLength={500}
                  className="h-9 min-w-0 flex-1 rounded-lg border-0 bg-white px-3 text-sm text-slate-900 shadow-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400"
                />
                <button
                  type="submit"
                  disabled={exporting || !exportReason.trim()}
                  className="rounded-lg bg-emerald-800 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
                >
                  {exporting ? "Exporting..." : "Export up to 1,000"}
                </button>
              </form>
              {exportError ? <p role="alert" className="mt-2 text-sm text-rose-700">{exportError}</p> : null}
            </div>
          ) : null}
          <div className="mb-4 grid gap-3 sm:grid-cols-2">
            <input
              aria-label="Filter audit action"
              placeholder="Filter by action"
              value={action}
              onChange={(event) => {
                setAction(event.target.value);
                setOffset(0);
              }}
              className="h-9 rounded-lg border-0 bg-white px-3 text-sm text-slate-900 shadow-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400"
            />
            <input
              aria-label="Filter audit actor ID"
              placeholder="Filter by actor user ID"
              value={actorID}
              onChange={(event) => {
                setActorID(event.target.value);
                setOffset(0);
              }}
              className="h-9 rounded-lg border-0 bg-white px-3 text-sm text-slate-900 shadow-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400"
            />
          </div>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[780px] text-left text-sm">
              <thead className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-3 py-3 font-medium">Time</th>
                  <th className="px-3 py-3 font-medium">Actor</th>
                  <th className="px-3 py-3 font-medium">Action</th>
                  <th className="px-3 py-3 font-medium">Target</th>
                  <th className="px-3 py-3 font-medium">Reason</th>
                  <th className="px-3 py-3 font-medium">Request ID</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {auditQuery.isLoading ? (
                  <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={6}>Loading audit events...</td></tr>
                ) : null}
                {!auditQuery.isLoading && (auditQuery.data?.data.length ?? 0) === 0 ? (
                  <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={6}>No audit events on this page.</td></tr>
                ) : null}
                {auditQuery.data?.data.map((record) => (
                  <tr key={record.id}>
                    <td className="px-3 py-3 text-slate-700">{new Date(record.created_at).toLocaleString()}</td>
                    <td className="px-3 py-3">
                      <p className="font-medium text-slate-800">{record.actor_role}</p>
                      <p className="text-xs text-slate-500">{record.actor_user_id ?? "System"}</p>
                    </td>
                    <td className="px-3 py-3 font-medium text-slate-800">{record.action}</td>
                    <td className="px-3 py-3 text-slate-700">
                      {record.target_type ? `${record.target_type}: ` : ""}
                      {record.target_id ?? "-"}
                    </td>
                    <td className="max-w-xs px-3 py-3 text-slate-700">{record.reason}</td>
                    <td className="px-3 py-3 font-mono text-xs text-slate-600">{record.request_id ?? "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-4 flex items-center justify-between border-t border-slate-100 pt-3 text-sm text-slate-600">
            <span>
              {auditQuery.data?.total === 0
                ? "No events"
                : `Showing ${offset + 1}-${Math.min(offset + (auditQuery.data?.data.length ?? 0), auditQuery.data?.total ?? 0)} of ${auditQuery.data?.total ?? 0}`}
              {" · "}Page {Math.floor(offset / pageSize) + 1}
            </span>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setOffset(Math.max(0, offset - pageSize))}
                disabled={offset === 0 || auditQuery.isFetching}
                className="rounded-md px-3 py-1.5 ring-1 ring-slate-200 disabled:opacity-50"
              >
                Previous
              </button>
              <button
                type="button"
                onClick={() => setOffset(offset + pageSize)}
                disabled={offset + pageSize >= (auditQuery.data?.total ?? 0) || auditQuery.isFetching}
                className="rounded-md px-3 py-1.5 ring-1 ring-slate-200 disabled:opacity-50"
              >
                Next
              </button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
