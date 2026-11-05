"use client";

import { useEffect, useState } from "react";
import { Plus, Trash2, RefreshCw } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Input, Select } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import { apiFetch, type Organization, type Membership, type Invitation } from "@/lib/auth";
import { formatDate, initials } from "@/lib/utils";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/use-toast";

const sections = [
  { id: "workspace", label: "Workspace" },
  { id: "sending", label: "Sending defaults" },
  { id: "team", label: "Team" },
  { id: "notifications", label: "Notifications" },
  { id: "compliance", label: "Compliance" },
  { id: "danger", label: "Danger zone" },
];

export default function SettingsPage() {
  const user = useAuthStore((state) => state.user);
  const organization = useAuthStore((state) => state.organization);
  const { toast } = useToast();

  const [org, setOrg] = useState<Organization | null>(null);
  const [members, setMembers] = useState<Membership[]>([]);
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [orgName, setOrgName] = useState("");
  const [orgSlug, setOrgSlug] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState("viewer");
  const [inviting, setInviting] = useState(false);

  useEffect(() => {
    loadData();
  }, []);

  async function loadData(showSpinner = true) {
    if (showSpinner) setLoading(true);
    try {
      // The token is scoped to one organization; fall back to the membership
      // list when the auth store has not hydrated the org id yet.
      const orgPromise = organization?.id
        ? apiFetch<Organization>(`/organizations/${organization.id}`)
        : apiFetch<{ data: { organization_id: string; name: string; slug: string }[] | null }>(
            "/organizations",
          ).then((res) => {
            const first = (res.data ?? [])[0];
            if (!first) throw new Error("No workspace found for this account.");
            return { id: first.organization_id, name: first.name, slug: first.slug } as Organization;
          });
      const [orgData, membersRes, invitationsRes] = await Promise.all([
        orgPromise,
        apiFetch<{ data: Membership[] | null }>("/members"),
        apiFetch<{ data: Invitation[] | null }>("/invitations"),
      ]);
      setOrg(orgData);
      setMembers(membersRes.data ?? []);
      setInvitations(invitationsRes.data ?? []);
      setOrgName(orgData.name);
      setOrgSlug(orgData.slug);
    } catch (err: any) {
      toast({ title: "Unable to load settings", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      if (showSpinner) setLoading(false);
    }
  }

  async function handleInvite() {
    const email = inviteEmail.trim();
    if (!email || inviting) return;
    setInviting(true);
    try {
      await apiFetch("/invitations", {
        method: "POST",
        body: JSON.stringify({ email, role: inviteRole }),
      });
      setInviteEmail("");
      setInviteOpen(false);
      toast({ title: "Invitation sent", description: `An invitation was sent to ${email}.` });
      await loadData(false);
    } catch (err: any) {
      toast({ title: "Could not send invitation", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setInviting(false);
    }
  }

  async function handleRevoke(id: string) {
    try {
      await apiFetch(`/invitations/${id}`, { method: "DELETE" });
      toast({ title: "Invitation revoked" });
      await loadData(false);
    } catch (err: any) {
      toast({ title: "Could not revoke invitation", description: err?.message ?? "Unknown error", variant: "destructive" });
    }
  }

  async function handleSaveOrg() {
    if (!org?.id) return;
    setSaving(true);
    try {
      await apiFetch(`/organizations/${org.id}`, {
        method: "PATCH",
        body: JSON.stringify({ name: orgName, slug: orgSlug }),
      });
      setOrg({ ...org, name: orgName, slug: orgSlug });
      toast({ title: "Workspace updated", description: "Your organization details have been saved." });
    } catch (err: any) {
      toast({ title: "Failed to save", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return (
      <div>
        <PageHeader title="Settings" description="Loading your workspace settings..." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Settings"
        description="Manage your workspace, sender identity, team access, and compliance defaults."
      />

      <div className="grid gap-5 lg:grid-cols-[200px_minmax(0,1fr)]">
        <nav className="hidden space-y-1 lg:block">
          {sections.map((section, index) => (
            <a
              key={section.id}
              href={`#${section.id}`}
              className={
                index === 0
                  ? "block rounded-lg bg-white px-3 py-2 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200"
                  : "block rounded-lg px-3 py-2 text-sm text-slate-600 hover:bg-white hover:text-slate-900"
              }
            >
              {section.label}
            </a>
          ))}
        </nav>

        <div className="space-y-5">
          <Card id="workspace">
            <CardHeader>
              <CardTitle>Workspace</CardTitle>
              <CardDescription>Your organization profile and brand details.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Workspace name</label>
                <Input value={orgName} onChange={(e) => setOrgName(e.target.value)} disabled={saving} />
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Workspace slug</label>
                <Input value={orgSlug} onChange={(e) => setOrgSlug(e.target.value)} disabled={saving} />
              </div>
              <div className="col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Timezone</label>
                <Select defaultValue="lagos" disabled={saving}>
                  <option value="lagos">Africa/Lagos (WAT)</option>
                  <option value="london">Europe/London (GMT)</option>
                  <option value="ny">America/New_York (EST)</option>
                </Select>
              </div>
              <div className="col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Default sender email</label>
                <Input placeholder="hello@yourdomain.com" disabled={saving} className="text-slate-500" />
                <p className="mt-1 text-xs text-slate-500">Add and verify a sending domain in Domains to use it here.</p>
              </div>
              <div className="col-span-2 flex items-center gap-2">
                <Button onClick={handleSaveOrg} disabled={saving} className="w-auto">
                  {saving ? "Saving..." : "Save changes"}
                </Button>
                <button
                  onClick={() => void loadData()}
                  disabled={saving}
                  className="rounded p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-700 disabled:opacity-50"
                  aria-label="Refresh"
                >
                  <RefreshCw className="h-4 w-4" />
                </button>
              </div>
            </CardContent>
          </Card>

          <Card id="sending">
            <CardHeader>
              <CardTitle>Sending defaults</CardTitle>
              <CardDescription>Used when you create a new campaign.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Default sender name</label>
                <Input defaultValue={org?.name ?? ""} disabled />
                <p className="mt-1 text-xs text-slate-500">Set in Workspace settings.</p>
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Default sender email</label>
                <Select defaultValue="none" disabled>
                  <option value="none">No verified domain — add one in Domains</option>
                </Select>
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Reply-to</label>
                <Input placeholder="reply@yourdomain.com" disabled />
              </div>
              <div className="col-span-2 space-y-2 rounded-lg bg-slate-50 p-4 text-sm">
                <label className="flex items-center gap-2 text-slate-700">
                  <input type="checkbox" defaultChecked disabled />
                  Enable open tracking by default
                </label>
                <label className="flex items-center gap-2 text-slate-700">
                  <input type="checkbox" defaultChecked disabled />
                  Enable click tracking by default
                </label>
                <label className="flex items-center gap-2 text-slate-700">
                  <input type="checkbox" defaultChecked disabled />
                  Include unsubscribe footer
                </label>
              </div>
              <div className="col-span-2">
                <Button variant="outline" disabled>
                  Save defaults
                </Button>
              </div>
            </CardContent>
          </Card>

          <Card id="team">
            <CardHeader className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <CardTitle>Team</CardTitle>
                <CardDescription>Invite teammates and control their access.</CardDescription>
              </div>
              <Button size="sm" onClick={() => setInviteOpen(true)}>
                <Plus className="h-3.5 w-3.5" /> Invite
              </Button>
            </CardHeader>
            <CardContent className="px-0 py-0">
              <Table>
                <Thead>
                  <Tr>
                    <Th>Member</Th>
                    <Th>Role</Th>
                    <Th>Status</Th>
                    <Th />
                  </Tr>
                </Thead>
                <Tbody>
                  {members.map((member) => (
                    <Tr key={member.user_id}>
                      <Td>
                        <div className="flex items-center gap-2.5">
                          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-slate-100 text-xs font-semibold text-slate-600">
                            {initials(member.user_id.slice(0, 8))}
                          </span>
                          <div>
                            <p className="font-medium text-slate-900" title={member.user_id}>
                              {member.user_id.slice(0, 8)}…
                            </p>
                            <p className="text-xs text-slate-500">Joined {formatDate(member.created_at)}</p>
                          </div>
                        </div>
                      </Td>
                      <Td>
                        <Badge variant="outline">{member.role}</Badge>
                      </Td>
                      <Td>
                        <Badge variant="outline">Active</Badge>
                      </Td>
                      <Td>
                        <button className="text-xs font-medium text-slate-500 hover:text-slate-900 disabled:opacity-50" disabled>
                          Manage
                        </button>
                      </Td>
                    </Tr>
                  ))}
                  {members.length === 0 && (
                    <Tr>
                      <Td colSpan={4} className="text-center text-sm text-slate-500 py-4">
                        No team members yet.
                      </Td>
                    </Tr>
                  )}
                </Tbody>
              </Table>

              <div className="mt-4 border-t border-slate-100 pt-4">
                <h3 className="text-sm font-semibold text-slate-900 mb-3">Pending invitations</h3>
                {invitations.length === 0 ? (
                  <p className="text-sm text-slate-500">No pending invitations.</p>
                ) : (
                  <Table className="mt-2">
                    <Tbody>
                      {invitations.map((inv) => (
                        <Tr key={inv.id}>
                          <Td className="font-medium text-slate-900 text-sm">{inv.email}</Td>
                          <Td className="text-sm text-slate-600">{inv.role}</Td>
                          <Td className="text-sm">
                            <Badge variant="outline">{inv.status}</Badge>
                          </Td>
                          <Td className="text-right">
                            <button
                              onClick={() => void handleRevoke(inv.id)}
                              className="text-xs font-medium text-rose-600 hover:text-rose-500 disabled:opacity-50"
                            >
                              Revoke
                            </button>
                          </Td>
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                )}
              </div>
            </CardContent>
          </Card>

          <Card id="notifications">
            <CardHeader>
              <CardTitle>Notifications</CardTitle>
              <CardDescription>Choose what lands in your inbox.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              {[
                { label: "Campaign finished sending", detail: "Summary with opens, clicks, and bounces", checked: true },
                { label: "Domain verification issues", detail: "When SPF, DKIM, or DMARC fails", checked: true },
                { label: "Weekly performance digest", detail: "Every Monday at 8:00 AM", checked: true },
                { label: "Contact import finished", detail: "Includes skipped and invalid rows", checked: false },
                { label: "Billing receipts", detail: "Invoices and payment confirmations", checked: true },
              ].map((item) => (
                <label key={item.label} className="flex items-start justify-between gap-4 rounded-lg border border-slate-200 p-3">
                  <span>
                    <span className="block font-medium text-slate-900">{item.label}</span>
                    <span className="block text-xs text-slate-500">{item.detail}</span>
                  </span>
                  <input type="checkbox" defaultChecked={item.checked} className="mt-1" />
                </label>
              ))}
              <Button>Save notifications</Button>
            </CardContent>
          </Card>

          <Card id="compliance">
            <CardHeader>
              <CardTitle>Compliance</CardTitle>
              <CardDescription>Consent and data handling defaults.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <label className="flex items-center gap-2 text-slate-700">
                <input type="checkbox" defaultChecked /> Require confirmed opt-in for imported contacts
              </label>
              <label className="flex items-center gap-2 text-slate-700">
                <input type="checkbox" defaultChecked /> Add one-click unsubscribe headers (RFC 8058)
              </label>
              <label className="flex items-center gap-2 text-slate-700">
                <input type="checkbox" defaultChecked /> Suppress bounced and complained addresses automatically
              </label>
              <label className="flex items-center gap-2 text-slate-700">
                <input type="checkbox" /> Enable double opt-in for all forms
              </label>
              <p className="rounded-lg bg-slate-50 p-3 text-xs text-slate-500">
                Retention: contact events are kept for 24 months. Delivery logs are kept for 90 days.
              </p>
            </CardContent>
          </Card>

          <Card id="danger" className="border-rose-200">
            <CardHeader>
              <CardTitle className="text-rose-700">Danger zone</CardTitle>
              <CardDescription>These actions cannot be undone.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p className="text-sm font-medium text-slate-900">Delete workspace</p>
                <p className="text-xs text-slate-500">Remove all contacts, campaigns, and delivery history.</p>
              </div>
              <Button variant="danger" size="sm">
                <Trash2 className="h-3.5 w-3.5" /> Delete workspace
              </Button>
            </CardContent>
          </Card>
        </div>
      </div>

      <Dialog open={inviteOpen} onOpenChange={setInviteOpen}>
        <DialogContent>
          <DialogTitle className="text-base font-semibold text-slate-900">Invite teammate</DialogTitle>
          <DialogDescription>
            They will receive an email invitation to join {org?.name ?? "your workspace"}.
          </DialogDescription>
          <div className="space-y-3">
            <div>
              <label htmlFor="invite-email" className="mb-1.5 block text-sm font-medium text-slate-700">
                Email address <span className="text-rose-500">*</span>
              </label>
              <Input
                id="invite-email"
                type="email"
                placeholder="teammate@company.com"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void handleInvite();
                }}
              />
            </div>
            <div>
              <label htmlFor="invite-role" className="mb-1.5 block text-sm font-medium text-slate-700">
                Role
              </label>
              <Select id="invite-role" value={inviteRole} onChange={(e) => setInviteRole(e.target.value)}>
                <option value="admin">Admin — full access</option>
                <option value="manager">Manager — manage campaigns and audience</option>
                <option value="author">Author — create and edit campaigns</option>
                <option value="analyst">Analyst — read-only plus reports</option>
                <option value="viewer">Viewer — read-only</option>
              </Select>
            </div>
          </div>
          <div className="flex items-center justify-end gap-2">
            <Button variant="secondary" onClick={() => setInviteOpen(false)} disabled={inviting}>
              Cancel
            </Button>
            <Button onClick={() => void handleInvite()} disabled={!inviteEmail.trim() || inviting}>
              {inviting ? "Sending..." : "Send invitation"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
