"use client";

import { notFound } from "next/navigation";
import Link from "next/link";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
};

type AdminWorkspace = {
  id: string;
  name: string;
  slug: string;
  member_count: number;
  created_at: string;
};

type AdminWorkspacesResponse = {
  data: AdminWorkspace[];
  pagination: { total: number; limit: number; offset: number };
};

const pageSize = 25;

export default function AdminWorkspacesPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const params = new URLSearchParams({
    search: search.trim(),
    limit: String(pageSize),
    offset: String(offset),
  });
  const workspacesQuery = useQuery({
    queryKey: ["admin-workspaces", search, offset],
    queryFn: () => apiFetch<AdminWorkspacesResponse>(`/admin/workspaces?${params.toString()}`),
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

  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="Workspaces" description="Verify your admin session before viewing workspace metadata." />
        <a href="/admin/users" className="text-sm font-medium text-emerald-800 underline">
          Continue to admin verification
        </a>
      </div>
    );
  }

  const total = workspacesQuery.data?.pagination.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title="Workspaces"
        description="Workspace names, identifiers, creation dates, and member counts. Customer content is not shown."
      />
      {workspacesQuery.error ? (
        <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">
          Unable to load workspaces: {workspacesQuery.error.message}
        </div>
      ) : null}
      <Card>
        <CardContent className="space-y-4 p-4">
          <Input
            aria-label="Search workspaces"
            placeholder="Search workspace name or slug"
            value={search}
            onChange={(event) => {
              setSearch(event.target.value);
              setOffset(0);
            }}
          />
          <div className="overflow-x-auto">
            <table className="w-full min-w-[700px] text-left text-sm">
              <thead className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-3 py-3 font-medium">Workspace</th>
                  <th className="px-3 py-3 font-medium">Members</th>
                  <th className="px-3 py-3 font-medium">Created</th>
                  <th className="px-3 py-3 font-medium">ID</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {workspacesQuery.isLoading ? (
                  <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={4}>Loading workspaces...</td></tr>
                ) : null}
                {!workspacesQuery.isLoading && (workspacesQuery.data?.data.length ?? 0) === 0 ? (
                  <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={4}>No workspaces match this search.</td></tr>
                ) : null}
                {workspacesQuery.data?.data.map((workspace) => (
                  <tr key={workspace.id}>
                    <td className="px-3 py-3">
                      <Link
                        href={`/admin/workspaces/${workspace.id}`}
                        className="font-medium text-emerald-800 underline"
                      >
                        {workspace.name}
                      </Link>
                      <p className="text-xs text-slate-500">{workspace.slug}</p>
                    </td>
                    <td className="px-3 py-3 text-slate-700">{workspace.member_count}</td>
                    <td className="px-3 py-3 text-slate-700">{new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(workspace.created_at))}</td>
                    <td className="px-3 py-3 font-mono text-xs text-slate-600">{workspace.id}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex items-center justify-between border-t border-slate-100 pt-3 text-sm text-slate-600">
            <span>{total} workspace{total === 1 ? "" : "s"} · Page {Math.floor(offset / pageSize) + 1} of {totalPages}</span>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setOffset(Math.max(0, offset - pageSize))}
                disabled={offset === 0 || workspacesQuery.isFetching}
                className="rounded-md px-3 py-1.5 ring-1 ring-slate-200 disabled:opacity-50"
              >
                Previous
              </button>
              <button
                type="button"
                onClick={() => setOffset(offset + pageSize)}
                disabled={offset + pageSize >= total || workspacesQuery.isFetching}
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
