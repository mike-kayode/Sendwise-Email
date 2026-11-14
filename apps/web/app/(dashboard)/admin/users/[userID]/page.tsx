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
  user_id: string;
  platform_role: "support_admin" | "super_admin";
  mfa_verified: boolean;
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

function dateLabel(value?: string) {
  if (!value) return "Never";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

export default function AdminUserDetailPage() {
  const { userID } = useParams<{ userID: string }>();
  const [action, setAction] = useState<"suspend" | "reactivate" | "revoke-sessions" | "resend-verification" | "verify-email" | "password-reset" | "demote-role" | null>(null);
  const [reason, setReason] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionBusy, setActionBusy] = useState(false);
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: () => apiFetch<AdminSession>("/admin/session"),
    retry: false,
  });
  const userQuery = useQuery({
    queryKey: ["admin-user", userID],
    queryFn: () => apiFetch<{ data: AdminUser }>(`/admin/users/${encodeURIComponent(userID)}`),
    enabled: Boolean(sessionQuery.data?.mfa_verified && userID),
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
  if (userQuery.error instanceof ApiError && userQuery.error.status === 404) {
    notFound();
  }
  if (!session.mfa_verified) {
    return (
      <div>
        <PageHeader eyebrow="Platform admin" title="User details" description="Verify your admin session before viewing user metadata." />
        <Link href="/admin/users" className="text-sm font-medium text-emerald-800 underline">
          Continue to admin verification
        </Link>
      </div>
    );
  }

  const user = userQuery.data?.data;

  const submitAction = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!action || !reason.trim() || !mfaCode.trim()) return;
    setActionBusy(true);
    setActionError(null);
    try {
      await apiFetch("/admin/mfa/verify", {
        method: "POST",
        body: JSON.stringify({ code: mfaCode.trim() }),
      });
      if (action === "resend-verification") {
        await apiFetch(`/admin/users/${encodeURIComponent(userID)}/resend-verification`, {
          method: "POST",
          body: JSON.stringify({ reason: reason.trim() }),
        });
      } else if (action === "verify-email") {
        await apiFetch(`/admin/users/${encodeURIComponent(userID)}/verify-email`, {
          method: "POST",
          body: JSON.stringify({ reason: reason.trim() }),
        });
      } else if (action === "password-reset") {
        await apiFetch(`/admin/users/${encodeURIComponent(userID)}/password-reset`, {
          method: "POST",
          body: JSON.stringify({ reason: reason.trim() }),
        });
      } else if (action === "demote-role") {
        await apiFetch(`/admin/users/${encodeURIComponent(userID)}/platform-role`, {
          method: "PATCH",
          body: JSON.stringify({ platform_role: "none", reason: reason.trim() }),
        });
      } else if (action === "revoke-sessions") {
        await apiFetch(`/admin/users/${encodeURIComponent(userID)}/revoke-sessions`, {
          method: "POST",
          body: JSON.stringify({ reason: reason.trim() }),
        });
      } else {
        await apiFetch(`/admin/users/${encodeURIComponent(userID)}/status`, {
          method: "PATCH",
          body: JSON.stringify({
            status: action === "suspend" ? "suspended" : "active",
            reason: reason.trim(),
          }),
        });
      }
      setAction(null);
      setReason("");
      setMfaCode("");
      await userQuery.refetch();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "The admin action failed.");
    } finally {
      setActionBusy(false);
    }
  };

  return (
    <div>
      <PageHeader
        eyebrow={`Platform admin · ${session.platform_role.replace("_", " ")}`}
        title={user?.name || "User details"}
        description={user?.email ?? "Account metadata"}
        actions={
          <Link href="/admin/users" className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200">
            Back to users
          </Link>
        }
      />
      {userQuery.isLoading ? <p className="text-sm text-slate-500">Loading user metadata...</p> : null}
      {userQuery.error ? (
        <div role="alert" className="mb-4 rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-800">
          Unable to load user: {userQuery.error.message}
        </div>
      ) : null}
      {user ? (
        <>
          <Card>
            <CardContent className="grid gap-5 p-5 sm:grid-cols-2">
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Account ID</p>
                <p className="mt-1 break-all font-mono text-sm text-slate-800">{user.id}</p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Status</p>
                <p className="mt-1 text-sm capitalize text-slate-800">{user.status}</p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Platform role</p>
                <p className="mt-1 text-sm text-slate-800">{user.platform_role.replace("_", " ")}</p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Email verified</p>
                <p className="mt-1 text-sm text-slate-800">{dateLabel(user.email_verified_at)}</p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Last login</p>
                <p className="mt-1 text-sm text-slate-800">{dateLabel(user.last_login_at)}</p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Account created</p>
                <p className="mt-1 text-sm text-slate-800">{dateLabel(user.created_at)}</p>
              </div>
            </CardContent>
          </Card>
          {session.platform_role === "super_admin" ? (
            <Card className="mt-5">
              <CardContent className="space-y-3 p-5">
                <div>
                  <h2 className="text-sm font-semibold text-slate-900">Account controls</h2>
                  <p className="mt-1 text-xs text-slate-500">Every action requires a reason, a fresh authenticator check, and is recorded in the audit log.</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  {user.status === "pending" && !user.email_verified_at ? (
                    <button type="button" onClick={() => setAction("verify-email")} className="rounded-lg px-3 py-2 text-sm font-medium text-emerald-800 ring-1 ring-emerald-200">
                      Mark email verified
                    </button>
                  ) : null}
                  {user.platform_role !== "none" && user.id !== session.user_id ? (
                    <button type="button" onClick={() => setAction("demote-role")} className="rounded-lg px-3 py-2 text-sm font-medium text-rose-800 ring-1 ring-rose-200">
                      Remove platform-admin role
                    </button>
                  ) : null}
                  {user.status === "active" && user.email_verified_at ? (
                    <button type="button" onClick={() => setAction("password-reset")} className="rounded-lg px-3 py-2 text-sm font-medium text-slate-700 ring-1 ring-slate-200">
                      Send password reset link
                    </button>
                  ) : null}
                  {user.status === "active" ? (
                    <button type="button" onClick={() => setAction("suspend")} className="rounded-lg px-3 py-2 text-sm font-medium text-rose-800 ring-1 ring-rose-200">
                      Suspend account
                    </button>
                  ) : null}
                  {user.status === "suspended" ? (
                    <button type="button" onClick={() => setAction("reactivate")} className="rounded-lg px-3 py-2 text-sm font-medium text-emerald-800 ring-1 ring-emerald-200">
                      Reactivate account
                    </button>
                  ) : null}
                  <button type="button" onClick={() => setAction("revoke-sessions")} className="rounded-lg px-3 py-2 text-sm font-medium text-slate-700 ring-1 ring-slate-200">
                    Revoke all sessions
                  </button>
                </div>
              </CardContent>
            </Card>
          ) : null}
        </>
      ) : null}
      {user?.status === "pending" && !user.email_verified_at ? (
        <Card className="mt-5">
          <CardContent className="flex flex-wrap items-center justify-between gap-3 p-5">
            <div>
              <h2 className="text-sm font-semibold text-slate-900">Verification email</h2>
              <p className="mt-1 text-xs text-slate-500">Resend a verification link. The action is rate limited and audited.</p>
            </div>
            <button type="button" onClick={() => setAction("resend-verification")} className="rounded-lg px-3 py-2 text-sm font-medium text-slate-700 ring-1 ring-slate-200">
              Resend verification
            </button>
          </CardContent>
        </Card>
      ) : null}
      {action ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/40 p-4">
          <form
            role="dialog"
            aria-modal="true"
            aria-labelledby="admin-user-action-title"
            onKeyDown={(event) => {
              if (event.key === "Escape" && !actionBusy) {
                setAction(null);
                setReason("");
                setMfaCode("");
                setActionError(null);
              }
            }}
            onSubmit={(event) => void submitAction(event)}
            className="w-full max-w-lg space-y-4 rounded-xl bg-white p-5 shadow-xl"
          >
            <div>
              <h2 id="admin-user-action-title" className="text-base font-semibold text-slate-900">
                {action === "revoke-sessions"
                  ? "Revoke user sessions"
                  : action === "resend-verification"
                    ? "Resend verification email"
                    : action === "verify-email"
                      ? "Mark email verified"
                        : action === "password-reset"
                          ? "Send password reset link"
                        : action === "demote-role"
                          ? "Remove platform-admin role"
                          : `${action === "suspend" ? "Suspend" : "Reactivate"} account`}
              </h2>
              <p className="mt-1 text-sm text-slate-600">Confirm the action with a current authenticator code and an audit reason.</p>
            </div>
            <label className="block text-sm font-medium text-slate-700">
              Reason
              <textarea
                required
                autoFocus
                maxLength={500}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                className="mt-1 min-h-24 w-full rounded-lg border border-slate-200 p-3 text-sm"
              />
            </label>
            <label className="block text-sm font-medium text-slate-700">
              Authenticator or recovery code
              <input
                required
                autoComplete="one-time-code"
                value={mfaCode}
                onChange={(event) => setMfaCode(event.target.value)}
                className="mt-1 h-10 w-full rounded-lg border border-slate-200 px-3 text-sm"
              />
            </label>
            {actionError ? <p role="alert" className="text-sm text-rose-700">{actionError}</p> : null}
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => { setAction(null); setReason(""); setMfaCode(""); setActionError(null); }} disabled={actionBusy} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200">
                Cancel
              </button>
              <button type="submit" disabled={actionBusy || !reason.trim() || !mfaCode.trim()} className="rounded-lg bg-emerald-800 px-3 py-2 text-sm font-medium text-white disabled:opacity-50">
                {actionBusy ? "Applying..." : "Confirm action"}
              </button>
            </div>
          </form>
        </div>
      ) : null}
    </div>
  );
}
