import Link from "next/link";
import type { Metadata } from "next";
import { ArrowLeft, Clock, GitBranch, Mail, MousePointerClick, Play, Plus, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { formatNumber } from "@/lib/utils";

const automations = [
  { id: "auto-1", name: "Welcome flow", trigger: "Contact subscribes", status: "active", contacts: 2240, completion: 68 },
  { id: "auto-2", name: "Re-engagement", trigger: "Inactive for 14 days", status: "draft", contacts: 980, completion: 41 },
  { id: "auto-3", name: "Purchase follow-up", trigger: "Order confirmed", status: "paused", contacts: 640, completion: 52 },
];

export const metadata: Metadata = { title: "Automation" };

const nodes = [
  { type: "trigger", title: "Contact subscribes", detail: "Newsletter subscribers list", icon: Users },
  { type: "email", title: "Welcome email", detail: "Sent immediately · 62.4% open rate", icon: Mail },
  { type: "wait", title: "Wait 2 days", detail: "Business days only", icon: Clock },
  { type: "email", title: "Getting started guide", detail: "Sent 2 days after step 1 · 48.1% open", icon: Mail },
  { type: "wait", title: "Wait 3 days", detail: "Business days only", icon: Clock },
  { type: "email", title: "Invite to a demo", detail: "Sent 5 days after step 1 · 38.9% open", icon: MousePointerClick },
];

export default async function AutomationDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const automation = automations.find((item) => item.id === id) ?? automations[0];

  return (
    <div>
      <Link href="/automations" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to automations
      </Link>

      <PageHeader
        eyebrow="Automation"
        title={automation.name}
        description={automation.trigger}
        actions={
          <>
            <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
              <Play className="h-4 w-4" /> {automation.status === "active" ? "Pause" : "Activate"}
            </button>
            <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white hover:bg-indigo-500">
              <Plus className="h-4 w-4" /> Add step
            </button>
          </>
        }
      />

      <div className="mb-5 flex flex-wrap items-center gap-3 text-sm text-slate-500">
        <StatusBadge status={automation.status} />
        <span className="inline-flex items-center gap-1"><GitBranch className="h-3.5 w-3.5" /> 3 emails · 2 waits</span>
        <span>·</span>
        <span>{formatNumber(automation.contacts)} contacts in flow</span>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Contacts in flow" value={automation.contacts} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Completion rate" value={`${automation.completion}%`} valueIsNumber={false} />
        <StatCard label="Emails sent" value={3420} />
        <StatCard label="Revenue" value="$6,240" valueIsNumber={false} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Flow</CardTitle>
            <CardDescription>Contacts move top to bottom through each step.</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-0">
              {nodes.map((node, index) => {
                const Icon = node.icon;
                return (
                  <div key={`${node.type}-${index}`}>
                    <div className="flex items-start gap-3 rounded-lg border border-slate-200 bg-white p-3.5">
                      <span
                        className={
                          node.type === "trigger"
                            ? "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600"
                            : node.type === "wait"
                              ? "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-amber-50 text-amber-600"
                              : "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600"
                        }
                      >
                        <Icon className="h-4 w-4" />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <p className="text-sm font-medium text-slate-900">{node.title}</p>
                          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[10px] uppercase tracking-wide text-slate-500">
                            {node.type}
                          </span>
                        </div>
                        <p className="text-xs text-slate-500">{node.detail}</p>
                      </div>
                      <button className="text-xs font-medium text-indigo-600 hover:text-indigo-500">Edit</button>
                    </div>
                    {index < nodes.length - 1 ? (
                      <div className="ml-[29px] h-6 w-px bg-slate-200" />
                    ) : null}
                  </div>
                );
              })}
            </div>
          </CardContent>
        </Card>

        <div className="space-y-5">
          <Card>
            <CardHeader>
              <CardTitle>Settings</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Audience</span>
                <span className="text-right font-medium text-slate-900">Newsletter subscribers</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Sender</span>
                <span className="text-right font-medium text-slate-900">hello@acme.studio</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Timezone</span>
                <span className="text-right font-medium text-slate-900">Africa/Lagos</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Exit criteria</span>
                <span className="text-right font-medium text-slate-900">Makes a purchase</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Recent activity</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              {[
                "142 contacts entered the flow this week",
                "68 completed all three emails",
                "12 replied to the welcome email",
                "3 unsubscribed after email 2",
              ].map((item) => (
                <div key={item} className="flex gap-2 text-slate-600">
                  <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-indigo-500" />
                  {item}
                </div>
              ))}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
