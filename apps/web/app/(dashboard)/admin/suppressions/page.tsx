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

type Suppression = {
  id: string;
  workspace_id: string;
  workspace_name: string;
  workspace_slug: string;
  email: string;
  reason: string;
  source: string;
  created_at: string;
};

type SuppressionResponse = {
  data: Suppression[];
  pagination: { total: number; limit: number; offset: number };
};

const pageSize = 25;

function formatTime(value: string) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

export default function AdminSuppressionsPage() {
  const queryClient = useQueryClient();
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [workspaceInput, setWorkspaceInput] = useState("");
  const [workspaceID, setWorkspaceID] = useState("");
  const [offset, setOffset] = useState(0);
  const [newWorkspaceID, setNewWorkspaceID] = useState("");
  const [newEmail, setNewEmail] = useState("");
  const [addReason, setAddReason] = useState("");
  const [removeTarget, setRemoveTarget] = useState<Suppression | null>(null);
  const [removeReason, setRemoveReason] = useState("");
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const params = useMemo(() => {
    const query = new URLSearchParams({ limit: String(pageSize), offset: String(offset) });
    if (search) query.set("search", search);
    if (workspaceID) query.set("workspace_id", workspaceID);
    return query.toString();
  }, [offset, search, workspaceID]);
  const suppressionsQuery = useQuery({
    queryKey: ["admin-suppressions", search, workspaceID, offset],
    queryFn: () => apiFetch<SuppressionResponse>(`/admin/suppressions?${params}`),
    enabled: sessionQuery.data?.platform_role === "super_admin" && Boolean(sessionQuery.data.mfa_verified),
    retry: false,
  });
  const addMutation = useMutation({
    mutationFn: () => apiFetch<{ data: Suppression }>("/admin/suppressions", {
      method: "POST",
      body: JSON.stringify({
        workspace_id: newWorkspaceID.trim(),
        email: newEmail.trim(),
        reason: addReason.trim(),
      }),
    }),
    onSuccess: async () => {
      setNewEmail("");
      setAddReason("");
      await queryClient.invalidateQueries({ queryKey: ["admin-suppressions"] });
    },
  });
  const removeMutation = useMutation({
    mutationFn: () => {
      if (!removeTarget) throw new Error("Select a suppression to remove.");
      return apiFetch<{ status: string }>(`/admin/suppressions/${encodeURIComponent(removeTarget.id)}`, {
        method: "DELETE",
        body: JSON.stringify({ reason: removeReason.trim() }),
      });
    },
    onSuccess: async () => {
      setRemoveTarget(null);
      setRemoveReason("");
      await queryClient.invalidateQueries({ queryKey: ["admin-suppressions"] });
    },
  });

  if (sessionQuery.isLoading) return <p className="text-sm text-slate-500">Checking platform admin access...</p>;
  if (sessionQuery.error instanceof ApiError && sessionQuery.error.code === "admin_forbidden") notFound();
  if (sessionQuery.error) {
    return <div role="alert" className="rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Admin access could not be verified: {sessionQuery.error.message}</div>;
  }
  const session = sessionQuery.data;
  if (!session) return null;
  if (session.platform_role !== "super_admin") notFound();
  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="Suppressions" description="Verify your admin session before viewing suppression addresses." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">Continue to admin verification</Link>
      </div>
    );
  }
  const result = suppressionsQuery.data;

  return (
    <div>
      <PageHeader eyebrow="Platform admin · super admin" title="Global suppressions" description="View and manage suppressed addresses across workspaces. Writes require fresh MFA and are audited with a reason." />
      <form
        className="mb-4 grid gap-3 rounded-xl border border-slate-200 bg-white p-4 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_auto]"
        onSubmit={(event) => {
          event.preventDefault();
          setOffset(0);
          setSearch(searchInput.trim());
          setWorkspaceID(workspaceInput.trim());
        }}
      >
        <label className="text-sm font-medium text-slate-700">
          Search address or workspace
          <input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Workspace ID (optional)
          <input value={workspaceInput} onChange={(event) => setWorkspaceInput(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
        </label>
        <button type="submit" className="self-end rounded-lg bg-emerald-700 px-4 py-2.5 text-sm font-semibold text-white">Search</button>
      </form>

      <section className="mb-5 rounded-xl border border-slate-200 bg-white p-4">
        <h2 className="font-semibold text-slate-900">Add suppression</h2>
        <p className="mt-1 text-sm text-slate-600">The address will be suppressed only in the selected workspace. The reason is required and recorded in the platform audit log.</p>
        <form className="mt-3 grid gap-3 md:grid-cols-2" onSubmit={(event) => { event.preventDefault(); addMutation.mutate(); }}>
          <label className="text-sm font-medium text-slate-700">
            Workspace ID
            <input required value={newWorkspaceID} onChange={(event) => setNewWorkspaceID(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
          </label>
          <label className="text-sm font-medium text-slate-700">
            Email address
            <input required type="email" value={newEmail} onChange={(event) => setNewEmail(event.target.value)} className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm" />
          </label>
          <label className="text-sm font-medium text-slate-700 md:col-span-2">
            Reason
            <textarea required minLength={1} maxLength={500} value={addReason} onChange={(event) => setAddReason(event.target.value)} className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm" rows={2} />
          </label>
          {addMutation.error ? <p role="alert" className="text-sm text-rose-700 md:col-span-2">{addMutation.error.message}</p> : null}
          <button type="submit" disabled={addMutation.isPending || !newWorkspaceID.trim() || !newEmail.trim() || !addReason.trim()} className="w-fit rounded-lg bg-emerald-700 px-4 py-2.5 text-sm font-semibold text-white disabled:opacity-50">
            {addMutation.isPending ? "Adding..." : "Add suppression"}
          </button>
        </form>
      </section>

      {suppressionsQuery.error ? <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">Unable to load suppressions: {suppressionsQuery.error.message}</div> : null}
      {suppressionsQuery.isLoading ? <p className="text-sm text-slate-500">Loading suppression records...</p> : null}
      {result ? (
        <>
          <div className="mb-3 text-sm text-slate-500">{result.pagination.total} suppression{result.pagination.total === 1 ? "" : "s"}</div>
          <div className="overflow-x-auto rounded-xl border border-slate-200 bg-white">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="border-b border-slate-200 bg-slate-50 text-xs uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-4 py-3 font-medium">Address</th>
                  <th className="px-4 py-3 font-medium">Workspace</th>
                  <th className="px-4 py-3 font-medium">Reason / source</th>
                  <th className="px-4 py-3 font-medium">Added</th>
                  <th className="px-4 py-3 font-medium">Action</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {result.data.length === 0 ? <tr><td colSpan={5} className="px-4 py-12 text-center text-slate-500">No suppression records match these filters.</td></tr> : null}
                {result.data.map((item) => (
                  <tr key={item.id}>
                    <td className="px-4 py-3 font-medium text-slate-900">{item.email}</td>
                    <td className="px-4 py-3">
                      <Link href={`/admin/workspaces/${encodeURIComponent(item.workspace_id)}`} className="font-medium text-emerald-800 hover:underline">{item.workspace_name}</Link>
                      <p className="mt-1 text-xs text-slate-500">/{item.workspace_slug} · {item.workspace_id}</p>
                    </td>
                    <td className="px-4 py-3 text-slate-600">{item.reason} · {item.source || "unknown"}</td>
                    <td className="px-4 py-3 text-xs text-slate-600">{formatTime(item.created_at)}</td>
                    <td className="px-4 py-3">
                      <button type="button" onClick={() => { setRemoveTarget(item); setRemoveReason(""); removeMutation.reset(); }} className="rounded-lg px-3 py-2 text-xs font-semibold text-rose-700 ring-1 ring-rose-200 hover:bg-rose-50">Remove</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <button type="button" disabled={offset === 0 || suppressionsQuery.isFetching} onClick={() => setOffset(Math.max(0, offset - pageSize))} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Previous</button>
            <p className="text-xs text-slate-500">{result.pagination.total === 0 ? 0 : offset + 1}–{Math.min(offset + pageSize, result.pagination.total)} of {result.pagination.total}</p>
            <button type="button" disabled={offset + pageSize >= result.pagination.total || suppressionsQuery.isFetching} onClick={() => setOffset(offset + pageSize)} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Next</button>
          </div>
        </>
      ) : null}

      {removeTarget ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/50 p-4" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setRemoveTarget(null); }}>
          <section role="dialog" aria-modal="true" aria-labelledby="remove-suppression-title" className="w-full max-w-lg rounded-xl bg-white p-5 shadow-xl">
            <h2 id="remove-suppression-title" className="text-lg font-semibold text-slate-900">Remove suppression?</h2>
            <p className="mt-2 text-sm text-slate-600">This removes the suppression record for {removeTarget.email} in {removeTarget.workspace_name}. Any separate unsubscribe or contact-consent status is unchanged. Enter a reason to continue.</p>
            <label className="mt-4 block text-sm font-medium text-slate-700">
              Reason
              <textarea autoFocus required maxLength={500} value={removeReason} onChange={(event) => setRemoveReason(event.target.value)} className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm" rows={3} />
            </label>
            {removeMutation.error ? <p role="alert" className="mt-3 text-sm text-rose-700">{removeMutation.error.message}</p> : null}
            <div className="mt-4 flex justify-end gap-2">
              <button type="button" onClick={() => setRemoveTarget(null)} className="rounded-lg px-4 py-2 text-sm text-slate-700 ring-1 ring-slate-200">Cancel</button>
              <button type="button" disabled={removeMutation.isPending || !removeReason.trim()} onClick={() => removeMutation.mutate()} className="rounded-lg bg-rose-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">{removeMutation.isPending ? "Removing..." : "Remove suppression"}</button>
            </div>
          </section>
        </div>
      ) : null}
    </div>
  );
}
