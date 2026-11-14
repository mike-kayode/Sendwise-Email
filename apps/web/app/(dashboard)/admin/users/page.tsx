"use client";

import { notFound } from "next/navigation";
import { useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-errors";
import { apiFetch } from "@/lib/auth";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

type AdminSession = {
  platform_role: "support_admin" | "super_admin";
  mfa_enabled: boolean;
  mfa_verified: boolean;
  expires_at: string;
};

type AdminUser = {
  id: string;
  email: string;
  name: string;
  status: string;
  platform_role: string;
  email_verified_at?: string;
  last_login_at?: string;
  created_at: string;
};

type AdminUsersResponse = {
  data: AdminUser[];
  pagination: { total: number; limit: number; offset: number };
};

type MFASetup = {
  secret: string;
  otpauth_url: string;
  recovery_codes: string[];
};

const pageSize = 25;

function dateLabel(value?: string) {
  if (!value) return "Never";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value));
}

export default function AdminUsersPage() {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
  const [mfaCode, setMfaCode] = useState("");
  const [mfaSetup, setMfaSetup] = useState<MFASetup | null>(null);
  const [mfaError, setMfaError] = useState<string | null>(null);
  const [mfaBusy, setMfaBusy] = useState(false);

  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const searchParams = new URLSearchParams({
    limit: String(pageSize),
    offset: String(offset),
  });
  if (search.trim()) searchParams.set("search", search.trim());
  if (status) searchParams.set("status", status);

  const usersQuery = useQuery({
    queryKey: ["admin-users", search, status, offset],
    queryFn: () => apiFetch<AdminUsersResponse>(`/admin/users?${searchParams.toString()}`),
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

  const startMFASetup = async () => {
    setMfaBusy(true);
    setMfaError(null);
    try {
      setMfaSetup(await apiFetch<MFASetup>("/admin/mfa/setup", { method: "POST" }));
    } catch (error) {
      setMfaError(error instanceof Error ? error.message : "MFA setup could not be started.");
    } finally {
      setMfaBusy(false);
    }
  };

  const verifyMFA = async () => {
    setMfaBusy(true);
    setMfaError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: mfaCode }),
      });
      setMfaCode("");
      setMfaSetup(null);
      await sessionQuery.refetch();
    } catch (error) {
      setMfaError(error instanceof Error ? error.message : "The code could not be verified.");
    } finally {
      setMfaBusy(false);
    }
  };

  if (!session.mfa_verified) {
    return (
      <div className="mx-auto max-w-2xl">
        <PageHeader
          eyebrow="Platform admin"
          title="Admin verification"
          description={`Admin data is protected by a fresh authenticator check. This session expires at ${new Date(session.expires_at).toLocaleString()}.`}
        />
        <Card>
          <CardContent className="space-y-4 p-5">
            <p className="text-sm font-medium text-slate-800">
              {session.mfa_enabled ? "Verify your authenticator code." : "Set up an authenticator app to continue."}
            </p>
            {!session.mfa_enabled && !mfaSetup ? (
              <button
                type="button"
                onClick={() => void startMFASetup()}
                disabled={mfaBusy}
                className="rounded-lg bg-emerald-800 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
              >
                {mfaBusy ? "Starting setup..." : "Set up MFA"}
              </button>
            ) : null}
            {mfaSetup ? (
              <div className="space-y-3 rounded-lg bg-slate-50 p-4">
                <p className="text-sm text-slate-700">Add this secret to your authenticator app:</p>
                <code className="block break-all rounded bg-white p-3 text-sm">{mfaSetup.secret}</code>
                <a className="text-sm font-medium text-emerald-800 underline" href={mfaSetup.otpauth_url}>
                  Open authenticator setup
                </a>
                <div>
                  <p className="text-sm font-medium text-slate-800">Save these recovery codes securely:</p>
                  <ul className="mt-2 grid grid-cols-2 gap-2 font-mono text-xs text-slate-700">
                    {mfaSetup.recovery_codes.map((code) => <li key={code}>{code}</li>)}
                  </ul>
                </div>
              </div>
            ) : null}
            {session.mfa_enabled || mfaSetup ? (
              <form
                className="flex flex-col gap-3 sm:flex-row"
                onSubmit={(event) => {
                  event.preventDefault();
                  void verifyMFA();
                }}
              >
                <Input
                  aria-label="Authenticator or recovery code"
                  autoComplete="one-time-code"
                  value={mfaCode}
                  onChange={(event) => setMfaCode(event.target.value)}
                  placeholder="Authenticator or recovery code"
                />
                <button
                  type="submit"
                  disabled={mfaBusy || !mfaCode.trim()}
                  className="rounded-lg bg-emerald-800 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
                >
                  {mfaBusy ? "Verifying..." : "Verify"}
                </button>
              </form>
            ) : null}
            {mfaError ? <p role="alert" className="text-sm text-rose-700">{mfaError}</p> : null}
          </CardContent>
        </Card>
      </div>
    );
  }

  const total = usersQuery.data?.pagination.total ?? 0;
  const currentPage = Math.floor(offset / pageSize) + 1;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title="Users"
        description="Search account metadata. Passwords, customer content, and secrets are never displayed here."
      />
      {usersQuery.error ? (
        <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">
          Unable to load users: {usersQuery.error.message}
        </div>
      ) : null}
      <Card>
        <CardContent className="space-y-4 p-4">
          <div className="grid gap-3 sm:grid-cols-[1fr_180px]">
            <Input
              aria-label="Search users"
              placeholder="Search name or email"
              value={search}
              onChange={(event) => {
                setSearch(event.target.value);
                setOffset(0);
              }}
            />
            <select
              aria-label="Filter by status"
              value={status}
              onChange={(event) => {
                setStatus(event.target.value);
                setOffset(0);
              }}
              className="h-9 rounded-lg border-0 bg-white px-3 text-sm text-slate-900 shadow-sm ring-1 ring-inset ring-slate-200"
            >
              <option value="">All statuses</option>
              <option value="active">Active</option>
              <option value="pending">Pending</option>
              <option value="suspended">Suspended</option>
              <option value="closed">Closed</option>
            </select>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-3 py-3 font-medium">User</th>
                  <th className="px-3 py-3 font-medium">Status</th>
                  <th className="px-3 py-3 font-medium">Platform role</th>
                  <th className="px-3 py-3 font-medium">Email verified</th>
                  <th className="px-3 py-3 font-medium">Last login</th>
                  <th className="px-3 py-3 font-medium">Created</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {usersQuery.isLoading ? (
                  <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={6}>Loading users...</td></tr>
                ) : null}
                {!usersQuery.isLoading && (usersQuery.data?.data.length ?? 0) === 0 ? (
                  <tr><td className="px-3 py-8 text-center text-slate-500" colSpan={6}>No users match these filters.</td></tr>
                ) : null}
                {usersQuery.data?.data.map((user) => (
                  <tr key={user.id}>
                    <td className="px-3 py-3">
                      <Link
                        href={`/admin/users/${encodeURIComponent(user.id)}`}
                        className="font-medium text-emerald-800 hover:underline"
                      >
                        {user.name || "Unnamed user"}
                      </Link>
                      <p className="text-xs text-slate-500">{user.email}</p>
                    </td>
                    <td className="px-3 py-3 capitalize text-slate-700">{user.status}</td>
                    <td className="px-3 py-3 text-slate-700">{user.platform_role.replace("_", " ")}</td>
                    <td className="px-3 py-3 text-slate-700">{dateLabel(user.email_verified_at)}</td>
                    <td className="px-3 py-3 text-slate-700">{dateLabel(user.last_login_at)}</td>
                    <td className="px-3 py-3 text-slate-700">{dateLabel(user.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex items-center justify-between border-t border-slate-100 pt-3 text-sm text-slate-600">
            <span>{total} user{total === 1 ? "" : "s"} · Page {currentPage} of {totalPages}</span>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setOffset(Math.max(0, offset - pageSize))}
                disabled={offset === 0 || usersQuery.isFetching}
                className="rounded-md px-3 py-1.5 ring-1 ring-slate-200 disabled:opacity-50"
              >
                Previous
              </button>
              <button
                type="button"
                onClick={() => setOffset(offset + pageSize)}
                disabled={offset + pageSize >= total || usersQuery.isFetching}
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
