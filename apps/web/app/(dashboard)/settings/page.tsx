"use client";

import { Plus, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Input, Select, Textarea } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import { initials } from "@/lib/utils";

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
  const team = [
    { name: user?.name ?? "Workspace owner", email: user?.email ?? "owner@example.com", role: organization?.role ?? "Owner", status: "active" },
    { name: "Tola Adeyemi", email: "tola@acme.studio", role: "Admin", status: "active" },
    { name: "Maya Chen", email: "maya@acme.studio", role: "Marketer", status: "active" },
    { name: "Sam Rivera", email: "sam@acme.studio", role: "Analyst", status: "active" },
    { name: "Ifeoma Nwosu", email: "ifeoma@acme.studio", role: "Marketer", status: "invited" },
  ];

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
                <Input defaultValue={organization?.name ?? "Sendwise"} />
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Workspace slug</label>
                <Input defaultValue={organization?.slug ?? "sendwise"} />
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Timezone</label>
                <Select defaultValue="lagos">
                  <option value="lagos">Africa/Lagos (WAT)</option>
                  <option value="london">Europe/London (GMT)</option>
                  <option value="ny">America/New_York (EST)</option>
                </Select>
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Default language</label>
                <Select defaultValue="en">
                  <option value="en">English</option>
                  <option value="fr">French</option>
                  <option value="pt">Portuguese</option>
                </Select>
              </div>
              <div className="sm:col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Mailing address</label>
                <Textarea rows={2} defaultValue="12 Marina Road, Lagos, Nigeria" />
                <p className="mt-1 text-xs text-slate-500">Included in the footer of every marketing email.</p>
              </div>
              <div className="sm:col-span-2">
                <Button>Save workspace</Button>
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
                <Input defaultValue="Acme Studio" />
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Default sender email</label>
                <Input defaultValue="hello@acme.studio" />
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Reply-to</label>
                <Input defaultValue="support@acme.studio" />
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">Sending domain</label>
                <Select defaultValue="acme">
                  <option value="acme">acme.studio · verified</option>
                  <option value="news">news.acme.studio · needs attention</option>
                </Select>
              </div>
              <div className="sm:col-span-2 space-y-2 rounded-lg bg-slate-50 p-4 text-sm">
                <label className="flex items-center gap-2 text-slate-700">
                  <input type="checkbox" defaultChecked /> Enable open tracking by default
                </label>
                <label className="flex items-center gap-2 text-slate-700">
                  <input type="checkbox" defaultChecked /> Enable click tracking by default
                </label>
                <label className="flex items-center gap-2 text-slate-700">
                  <input type="checkbox" defaultChecked /> Include unsubscribe footer
                </label>
              </div>
              <div className="sm:col-span-2">
                <Button>Save defaults</Button>
              </div>
            </CardContent>
          </Card>

          <Card id="team">
            <CardHeader className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <CardTitle>Team</CardTitle>
                <CardDescription>Invite teammates and control their access.</CardDescription>
              </div>
              <Button size="sm">
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
                  {team.map((member) => (
                    <Tr key={member.email}>
                      <Td>
                        <div className="flex items-center gap-2.5">
                          <span className="flex h-8 w-8 items-center justify-center rounded-full bg-slate-100 text-xs font-semibold text-slate-600">
                            {initials(member.name)}
                          </span>
                          <div>
                            <p className="font-medium text-slate-900">{member.name}</p>
                            <p className="text-xs text-slate-500">{member.email}</p>
                          </div>
                        </div>
                      </Td>
                      <Td>
                        <Badge variant="outline">{member.role}</Badge>
                      </Td>
                      <Td>
                        <Badge variant={member.status === "active" ? "success" : "warning"}>{member.status}</Badge>
                      </Td>
                      <Td>
                        <button className="text-xs font-medium text-slate-500 hover:text-slate-900">Manage</button>
                      </Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
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
    </div>
  );
}
