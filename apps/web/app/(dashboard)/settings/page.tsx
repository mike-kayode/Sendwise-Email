"use client";

import { useEffect, useState } from "react";
import { Plus, Trash2, RefreshCw } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Input, Select, Textarea } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import {
  apiFetch,
  type Organization,
  type Membership,
  type Invitation,
  type SendingDomain,
} from "@/lib/auth";
import { formatDate, initials } from "@/lib/utils";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/use-toast";

const sections = [
  { id: "workspace", label: "Workspace" },
  { id: "sending", label: "Sending defaults" },
  { id: "team", label: "Team" },
  { id: "notifications", label: "Notifications" },
  { id: "sender-profile", label: "Sender profile" },
  { id: "compliance", label: "Compliance" },
  { id: "danger", label: "Danger zone" },
];

type SenderIdentity = {
  id: string;
  domain: string;
  local_part: string;
  email: string;
  display_name: string;
  reply_to: string;
  is_default: boolean;
  verified: boolean;
};

type SenderProfile = {
  company_name: string;
  street_address: string;
  city: string;
  region: string;
  postal_code: string;
  country: string;
  website: string;
  support_email: string;
  footer_text: string;
  social_links: Array<{ label: string; url: string }>;
  complete: boolean;
};

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
  const [senderIdentities, setSenderIdentities] = useState<SenderIdentity[]>([]);
  const [verifiedDomains, setVerifiedDomains] = useState<SendingDomain[]>([]);
  const [identityID, setIdentityID] = useState("");
  const [identityDomain, setIdentityDomain] = useState("");
  const [identityLocalPart, setIdentityLocalPart] = useState("");
  const [identityName, setIdentityName] = useState("");
  const [identityReplyTo, setIdentityReplyTo] = useState("");
  const [identitySaving, setIdentitySaving] = useState(false);
  const [senderProfile, setSenderProfile] = useState<SenderProfile>({
    company_name: "",
    street_address: "",
    city: "",
    region: "",
    postal_code: "",
    country: "",
    website: "",
    support_email: "",
    footer_text: "",
    social_links: [],
    complete: false,
  });
  const [socialLinksText, setSocialLinksText] = useState("[]");
  const [profileSaving, setProfileSaving] = useState(false);

  useEffect(() => {
    void loadData();
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
      const [orgData, membersRes, invitationsRes, identitiesRes, profileRes, domainsRes] = await Promise.all([
        orgPromise,
        apiFetch<{ data: Membership[] | null }>("/members"),
        apiFetch<{ data: Invitation[] | null }>("/invitations"),
        apiFetch<{ data: SenderIdentity[] | null }>("/sender-identities"),
        apiFetch<SenderProfile>("/sender-profile"),
        apiFetch<{ data: SendingDomain[] | null }>("/domains"),
      ]);
      setOrg(orgData);
      setMembers(membersRes.data ?? []);
      setInvitations(invitationsRes.data ?? []);
      setSenderIdentities(identitiesRes.data ?? []);
      setVerifiedDomains((domainsRes.data ?? []).filter((domain) => domain.status === "verified" && !domain.owner_conflict));
      setSenderProfile(profileRes);
      setSocialLinksText(JSON.stringify(profileRes.social_links ?? [], null, 2));
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

  function resetIdentityForm() {
    setIdentityID("");
    setIdentityDomain("");
    setIdentityLocalPart("");
    setIdentityName("");
    setIdentityReplyTo("");
  }

  function editIdentity(identity: SenderIdentity) {
    setIdentityID(identity.id);
    setIdentityDomain(identity.domain);
    setIdentityLocalPart(identity.local_part);
    setIdentityName(identity.display_name);
    setIdentityReplyTo(identity.reply_to);
  }

  async function handleSaveIdentity() {
    if (identitySaving) return;
    setIdentitySaving(true);
    try {
      await apiFetch(identityID ? `/sender-identities/${identityID}` : "/sender-identities", {
        method: identityID ? "PATCH" : "POST",
        body: JSON.stringify({
          domain: identityDomain,
          local_part: identityLocalPart,
          display_name: identityName,
          reply_to: identityReplyTo,
          is_default: senderIdentities.length === 0,
        }),
      });
      resetIdentityForm();
      await loadData(false);
      toast({ title: "Sender identity saved", description: "The address is linked to its verified sending domain." });
    } catch (err) {
      toast({
        title: "Could not save sender identity",
        description: err instanceof Error ? err.message : "Unknown error",
        variant: "destructive",
      });
    } finally {
      setIdentitySaving(false);
    }
  }

  async function handleDefaultIdentity(identity: SenderIdentity) {
    try {
      await apiFetch(`/sender-identities/${identity.id}/default`, { method: "POST" });
      await loadData(false);
      toast({ title: "Default sender updated", description: `${identity.email} is now the default sender.` });
    } catch (err) {
      toast({
        title: "Could not set default sender",
        description: err instanceof Error ? err.message : "Unknown error",
        variant: "destructive",
      });
    }
  }

  async function handleDeleteIdentity(identity: SenderIdentity) {
    if (!window.confirm(`Delete sender identity ${identity.email}? Campaigns using it will no longer be sendable.`)) return;
    try {
      await apiFetch(`/sender-identities/${identity.id}`, { method: "DELETE" });
      await loadData(false);
      if (identityID === identity.id) resetIdentityForm();
      toast({ title: "Sender identity deleted" });
    } catch (err) {
      toast({
        title: "Could not delete sender identity",
        description: err instanceof Error ? err.message : "Unknown error",
        variant: "destructive",
      });
    }
  }

  async function handleSaveSenderProfile() {
    if (profileSaving) return;
    setProfileSaving(true);
    try {
      const socialLinks = JSON.parse(socialLinksText) as SenderProfile["social_links"];
      const saved = await apiFetch<SenderProfile>("/sender-profile", {
        method: "PUT",
        body: JSON.stringify({ ...senderProfile, social_links: socialLinks }),
      });
      setSenderProfile(saved);
      setSocialLinksText(JSON.stringify(saved.social_links ?? [], null, 2));
      toast({ title: "Sender profile saved", description: saved.complete ? "Campaigns can now be sent." : "Complete all required fields before sending." });
    } catch (err) {
      toast({
        title: "Could not save sender profile",
        description: err instanceof Error ? err.message : "Social links must be valid JSON.",
        variant: "destructive",
      });
    } finally {
      setProfileSaving(false);
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
              <CardTitle>Sender identities</CardTitle>
              <CardDescription>
                Create addresses on verified domains. Campaigns can only send from an identity owned by this workspace.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-5">
              {senderIdentities.length ? (
                <Table>
                  <Thead>
                    <Tr>
                      <Th>From</Th>
                      <Th>Domain</Th>
                      <Th>Status</Th>
                      <Th>Default</Th>
                      <Th />
                    </Tr>
                  </Thead>
                  <Tbody>
                    {senderIdentities.map((identity) => (
                      <Tr key={identity.id}>
                        <Td>
                          <span className="block font-medium text-slate-900">{identity.display_name}</span>
                          <span className="text-xs text-slate-500">{identity.email}</span>
                        </Td>
                        <Td className="text-sm text-slate-600">{identity.domain}</Td>
                        <Td><Badge variant={identity.verified ? "success" : "warning"}>{identity.verified ? "Verified" : "Unavailable"}</Badge></Td>
                        <Td>{identity.is_default ? <Badge variant="outline">Default</Badge> : "—"}</Td>
                        <Td>
                          <div className="flex items-center gap-3">
                            {!identity.is_default ? (
                              <button type="button" onClick={() => void handleDefaultIdentity(identity)} className="text-xs font-medium text-indigo-600 hover:text-indigo-500">
                                Set default
                              </button>
                            ) : null}
                            <button type="button" onClick={() => editIdentity(identity)} className="text-xs font-medium text-slate-600 hover:text-slate-900">
                              Edit
                            </button>
                            <button type="button" onClick={() => void handleDeleteIdentity(identity)} className="text-xs font-medium text-rose-600 hover:text-rose-500">
                              Delete
                            </button>
                          </div>
                        </Td>
                      </Tr>
                    ))}
                  </Tbody>
                </Table>
              ) : (
                <p className="rounded-lg bg-amber-50 p-3 text-sm text-amber-900">
                  Add and verify a sending domain before creating a sender identity.
                </p>
              )}
              <div className="grid gap-3 rounded-lg border border-slate-200 p-4 sm:grid-cols-2">
                <label className="text-sm font-medium text-slate-700">
                  Verified sending domain
                  <Select className="mt-1" value={identityDomain} onChange={(event) => setIdentityDomain(event.target.value)}>
                    <option value="">Choose a verified domain</option>
                    {verifiedDomains.map((domain) => <option key={domain.id} value={domain.domain}>{domain.domain}</option>)}
                  </Select>
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Local part
                  <Input className="mt-1" value={identityLocalPart} onChange={(event) => setIdentityLocalPart(event.target.value.toLowerCase())} placeholder="newsletter" maxLength={64} />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Display name
                  <Input className="mt-1" value={identityName} onChange={(event) => setIdentityName(event.target.value)} placeholder="Company Newsletter" />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Reply-to
                  <Input className="mt-1" value={identityReplyTo} onChange={(event) => setIdentityReplyTo(event.target.value)} placeholder="support@example.com" type="email" />
                </label>
                <div className="flex gap-2 sm:col-span-2">
                  <Button onClick={() => void handleSaveIdentity()} disabled={identitySaving || !identityDomain || !identityLocalPart.trim() || !identityName.trim()}>
                    {identitySaving ? "Saving..." : identityID ? "Update sender" : "Add sender"}
                  </Button>
                  {identityID ? <Button variant="outline" onClick={resetIdentityForm}>Cancel edit</Button> : null}
                </div>
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

          <Card id="sender-profile">
            <CardHeader>
              <CardTitle>Sender profile</CardTitle>
              <CardDescription>
                This information is required for campaign sending and is included in every compliance footer.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <p className={`rounded-lg p-3 text-sm ${senderProfile.complete ? "bg-emerald-50 text-emerald-800" : "bg-amber-50 text-amber-900"}`}>
                {senderProfile.complete
                  ? "Profile complete. Campaign sends are enabled."
                  : "Complete the company, postal address, website, and support email to enable campaign sends."}
              </p>
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="text-sm font-medium text-slate-700">
                  Company name
                  <Input required className="mt-1" value={senderProfile.company_name} onChange={(event) => setSenderProfile({ ...senderProfile, company_name: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Support email
                  <Input required className="mt-1" type="email" value={senderProfile.support_email} onChange={(event) => setSenderProfile({ ...senderProfile, support_email: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700 sm:col-span-2">
                  Street address
                  <Input required className="mt-1" value={senderProfile.street_address} onChange={(event) => setSenderProfile({ ...senderProfile, street_address: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  City
                  <Input required className="mt-1" value={senderProfile.city} onChange={(event) => setSenderProfile({ ...senderProfile, city: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Region / state
                  <Input className="mt-1" value={senderProfile.region} onChange={(event) => setSenderProfile({ ...senderProfile, region: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Postal code
                  <Input required className="mt-1" value={senderProfile.postal_code} onChange={(event) => setSenderProfile({ ...senderProfile, postal_code: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700">
                  Country
                  <Input required className="mt-1" value={senderProfile.country} onChange={(event) => setSenderProfile({ ...senderProfile, country: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700 sm:col-span-2">
                  Website
                  <Input required className="mt-1" type="url" placeholder="https://example.com" value={senderProfile.website} onChange={(event) => setSenderProfile({ ...senderProfile, website: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700 sm:col-span-2">
                  Optional footer text
                  <Textarea className="mt-1" rows={3} value={senderProfile.footer_text} onChange={(event) => setSenderProfile({ ...senderProfile, footer_text: event.target.value })} />
                </label>
                <label className="text-sm font-medium text-slate-700 sm:col-span-2">
                  Social links (JSON array of {"{ label, url }"} objects)
                  <Textarea className="mt-1 font-mono text-xs" rows={4} value={socialLinksText} onChange={(event) => setSocialLinksText(event.target.value)} />
                </label>
              </div>
              <Button onClick={() => void handleSaveSenderProfile()} disabled={profileSaving}>
                {profileSaving ? "Saving profile..." : "Save sender profile"}
              </Button>
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
