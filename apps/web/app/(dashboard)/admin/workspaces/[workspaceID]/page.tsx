"use client";

import Link from "next/link";
import { notFound, useParams } from "next/navigation";
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

type WorkspaceMember = {
  organization_id: string;
  user_id: string;
  role: string;
  created_at: string;
};

type AdminWorkspace = {
  id: string;
  name: string;
  slug: string;
  created_at: string;
  members: WorkspaceMember[];
};

type AdminWorkspaceNote = {
  id: number;
  actor_user_id?: string;
  note: string;
  reason: string;
  created_at: string;
};

export default function AdminWorkspaceDetailPage() {
  const { workspaceID } = useParams<{ workspaceID: string }>();
  const [note, setNote] = useState("");
  const [reason, setReason] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [noteError, setNoteError] = useState<string | null>(null);
  const [noteBusy, setNoteBusy] = useState(false);
  const [statusReason, setStatusReason] = useState("");
  const [statusMfaCode, setStatusMfaCode] = useState("");
  const [statusError, setStatusError] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const workspaceQuery = useQuery({
    queryKey: ["admin-workspace", workspaceID],
    queryFn: () => apiFetch<{ data: AdminWorkspace }>(`/admin/workspaces/${encodeURIComponent(workspaceID)}`),
    enabled: Boolean(sessionQuery.data?.mfa_verified && workspaceID),
    retry: false,
  });
  const workspaceStatusQuery = useQuery({
    queryKey: ["admin-workspace-status", workspaceID],
    queryFn: () => apiFetch<{ status: "active" | "suspended" | "closed" }>(`/admin/workspaces/${encodeURIComponent(workspaceID)}/status`),
    enabled: Boolean(sessionQuery.data?.mfa_verified && workspaceID),
    retry: false,
  });
  const notesQuery = useQuery({
    queryKey: ["admin-workspace-notes", workspaceID],
    queryFn: () => apiFetch<{ data: AdminWorkspaceNote[] }>(`/admin/workspaces/${encodeURIComponent(workspaceID)}/notes`),
    enabled: Boolean(sessionQuery.data?.mfa_verified && sessionQuery.data.platform_role === "super_admin" && workspaceID),
    retry: false,
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
  if (workspaceQuery.error instanceof ApiError && workspaceQuery.error.status === 404) {
    notFound();
  }

  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="Workspace details" description="Verify your admin session before viewing workspace metadata." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">
          Continue to admin verification
        </Link>
      </div>
    );
  }

  const workspace = workspaceQuery.data?.data;
  const saveNote = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!note.trim() || !reason.trim() || !mfaCode.trim()) return;
    setNoteBusy(true);
    setNoteError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: mfaCode.trim() }),
      });
      await apiFetch(`/admin/workspaces/${encodeURIComponent(workspaceID)}/notes`, {
        method: "POST",
        body: JSON.stringify({ note: note.trim(), reason: reason.trim() }),
      });
      setNote("");
      setReason("");
      setMfaCode("");
      await notesQuery.refetch();
    } catch (error) {
      setNoteError(error instanceof Error ? error.message : "The workspace note could not be saved.");
    } finally {
      setNoteBusy(false);
    }
  };
  const updateWorkspaceStatus = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const currentStatus = workspaceStatusQuery.data?.status;
    if (!currentStatus || currentStatus === "closed" || !statusReason.trim() || !statusMfaCode.trim()) return;
    setStatusBusy(true);
    setStatusError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: statusMfaCode.trim() }),
      });
      await apiFetch(`/admin/workspaces/${encodeURIComponent(workspaceID)}/status`, {
        method: "PATCH",
        body: JSON.stringify({
          status: currentStatus === "suspended" ? "active" : "suspended",
          reason: statusReason.trim(),
        }),
      });
      setStatusReason("");
      setStatusMfaCode("");
      await workspaceStatusQuery.refetch();
    } catch (error) {
      setStatusError(error instanceof Error ? error.message : "Workspace status could not be changed.");
    } finally {
      setStatusBusy(false);
    }
  };
  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title={workspace?.name ?? "Workspace details"}
        description={workspace ? `/${workspace.slug} · Created ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(workspace.created_at))}` : "Workspace membership metadata"}
        actions={
          <Link href="/admin/workspaces" className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200">
            Back to workspaces
          </Link>
        }
      />
      {workspaceQuery.error ? (
        <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">
          Unable to load workspace: {workspaceQuery.error.message}
        </div>
      ) : null}
      {workspace ? (
        <Card>
          <CardContent className="p-4">
            <div className="mb-4">
              <p className="text-sm font-semibold text-slate-900">Members</p>
              <p className="text-xs text-slate-500">{workspace.members.length} membership records</p>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[640px] text-left text-sm">
                <thead className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500">
                  <tr>
                    <th className="px-3 py-3 font-medium">User ID</th>
                    <th className="px-3 py-3 font-medium">Workspace role</th>
                    <th className="px-3 py-3 font-medium">Joined</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {workspace.members.length === 0 ? (
                    <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={3}>No membership records.</td></tr>
                  ) : null}
                  {workspace.members.map((member) => (
                    <tr key={member.user_id}>
                      <td className="px-3 py-3 font-mono text-xs text-slate-700">{member.user_id}</td>
                      <td className="px-3 py-3 capitalize text-slate-700">{member.role}</td>
                      <td className="px-3 py-3 text-slate-700">{new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(member.created_at))}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      ) : null}
      {workspace && session.platform_role === "super_admin" ? (
        <Card className="mt-5">
          <CardContent className="space-y-4 p-4">
            <div>
              <h2 className="text-sm font-semibold text-slate-900">Workspace status</h2>
              {workspaceStatusQuery.isLoading ? <p className="mt-1 text-sm text-slate-500">Loading status...</p> : null}
              {workspaceStatusQuery.error ? <p role="alert" className="mt-1 text-sm text-rose-700">Unable to load status: {workspaceStatusQuery.error.message}</p> : null}
              {workspaceStatusQuery.data ? (
                <p className="mt-1 text-sm text-slate-600">
                  Current status: <span className="font-medium capitalize">{workspaceStatusQuery.data.status}</span>
                  {workspaceStatusQuery.data.status === "suspended" ? " · Campaign and transactional sending is blocked." : ""}
                </p>
              ) : null}
            </div>
            {workspaceStatusQuery.data && workspaceStatusQuery.data.status !== "closed" ? (
              <form onSubmit={(event) => void updateWorkspaceStatus(event)} className="grid gap-3 md:grid-cols-3">
                <label className="block text-sm font-medium text-slate-700">
                  Audit reason
                  <input required maxLength={500} value={statusReason} onChange={(event) => setStatusReason(event.target.value)} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
                </label>
                <label className="block text-sm font-medium text-slate-700">
                  Authenticator or recovery code
                  <input required autoComplete="one-time-code" value={statusMfaCode} onChange={(event) => setStatusMfaCode(event.target.value)} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
                </label>
                <div className="flex items-end">
                  <button type="submit" disabled={statusBusy || workspaceStatusQuery.isFetching} className="rounded-lg bg-rose-700 px-4 py-3 text-sm font-semibold text-white disabled:opacity-50">
                    {statusBusy ? "Updating..." : workspaceStatusQuery.data.status === "suspended" ? "Resume workspace" : "Suspend workspace"}
                  </button>
                </div>
                {statusError ? <p role="alert" className="text-sm text-rose-700 md:col-span-3">{statusError}</p> : null}
              </form>
            ) : null}
          </CardContent>
        </Card>
      ) : null}
      {workspace && session.platform_role === "super_admin" ? (
        <Card className="mt-5">
          <CardContent className="space-y-5 p-4">
            <div>
              <h2 className="text-sm font-semibold text-slate-900">Internal notes</h2>
              <p className="mt-1 text-xs text-slate-500">Append-only and visible to platform super admins. Do not include credentials or customer message content.</p>
            </div>
            {notesQuery.isLoading ? <p className="text-sm text-slate-500">Loading notes...</p> : null}
            {notesQuery.error ? <p role="alert" className="text-sm text-rose-700">Unable to load notes: {notesQuery.error.message}</p> : null}
            {notesQuery.data?.data.length === 0 ? <p className="text-sm text-slate-500">No internal notes.</p> : null}
            <ul className="space-y-3">
              {notesQuery.data?.data.map((item) => (
                <li key={item.id} className="rounded-lg border border-slate-200 p-3">
                  <p className="whitespace-pre-wrap text-sm text-slate-800">{item.note}</p>
                  <p className="mt-2 text-xs text-slate-500">
                    {new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(item.created_at))}
                    {item.actor_user_id ? ` · ${item.actor_user_id}` : ""}
                  </p>
                  <p className="mt-1 text-xs text-slate-500">Reason: {item.reason}</p>
                </li>
              ))}
            </ul>
            <form onSubmit={(event) => void saveNote(event)} className="space-y-3 border-t border-slate-100 pt-4">
              <label className="block text-sm font-medium text-slate-700">
                Note
                <textarea required maxLength={2000} value={note} onChange={(event) => setNote(event.target.value)} className="mt-1 min-h-24 w-full rounded-lg border border-slate-200 p-3 text-sm" />
              </label>
              <label className="block text-sm font-medium text-slate-700">
                Audit reason
                <input required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
              </label>
              <label className="block text-sm font-medium text-slate-700">
                Authenticator or recovery code
                <input required autoComplete="one-time-code" value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} className="mt-1 w-full rounded-lg border border-slate-200 p-3 text-sm" />
              </label>
              {noteError ? <p role="alert" className="text-sm text-rose-700">{noteError}</p> : null}
              <button type="submit" disabled={noteBusy} className="rounded-lg bg-emerald-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">
                {noteBusy ? "Saving..." : "Add note"}
              </button>
            </form>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
