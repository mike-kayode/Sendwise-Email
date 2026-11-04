import Link from "next/link";
import type { Metadata } from "next";
import { ArrowRight, GitBranch, Plus, Workflow, Zap } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { ProgressBar } from "@/components/charts";
import { formatNumber, relativeTime } from "@/lib/utils";

const fallbackAutomations = [
  { id: "auto-1", name: "Welcome flow", trigger: "Contact subscribes", status: "active", contacts: 2240, completion: 68, updated: "2026-05-14T08:30:00Z" },
  { id: "auto-2", name: "Re-engagement", trigger: "Inactive for 14 days", status: "draft", contacts: 980, completion: 41, updated: "2026-05-11T19:00:00Z" },
  { id: "auto-3", name: "Purchase follow-up", trigger: "Order confirmed", status: "paused", contacts: 640, completion: 52, updated: "2026-05-02T13:20:00Z" },
];

const automations = fallbackAutomations;

export const metadata: Metadata = { title: "Automations" };

export default function AutomationsPage() {
  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Automations"
        description="Send the right message automatically when contacts take an action."
        actions={
          <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
            <Plus className="h-4 w-4" /> New automation
          </button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Active automations" value={2} icon={<Zap className="h-4 w-4" />} changeLabel="2 drafts, 1 paused" />
        <StatCard label="Contacts in flows" value={2240} icon={<GitBranch className="h-4 w-4" />} />
        <StatCard label="Revenue attributed" value="$18,420" valueIsNumber={false} icon={<Workflow className="h-4 w-4" />} />
      </div>

      <div className="mt-5 grid gap-4 md:grid-cols-2">
        {automations.map((automation) => (
          <Card key={automation.id}>
            <CardHeader className="flex items-start justify-between">
              <div>
                <div className="flex items-center gap-2">
                  <CardTitle>{automation.name}</CardTitle>
                  <StatusBadge status={automation.status} />
                </div>
                <CardDescription>{automation.trigger}</CardDescription>
              </div>
              <Link
                href={`/automations/${automation.id}`}
                className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                aria-label={`Open ${automation.name}`}
              >
                <ArrowRight className="h-4 w-4" />
              </Link>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-center justify-between text-sm">
                <span className="text-slate-500">Contacts in flow</span>
                <span className="font-medium tabular-nums text-slate-900">{formatNumber(automation.contacts)}</span>
              </div>
              <div>
                <div className="mb-1.5 flex justify-between text-xs text-slate-500">
                  <span>Completion rate</span>
                  <span>{automation.completion}%</span>
                </div>
                <ProgressBar value={automation.completion} />
              </div>
              <div className="flex items-center justify-between border-t border-slate-100 pt-3 text-xs text-slate-500">
                <span>Updated {relativeTime(automation.updated)}</span>
                <Link href={`/automations/${automation.id}`} className="font-medium text-indigo-600 hover:text-indigo-500">
                  Open flow
                </Link>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}
