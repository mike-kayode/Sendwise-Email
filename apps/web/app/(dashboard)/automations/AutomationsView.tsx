"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Plus, Workflow, Zap, ArrowRight, Play } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { apiFetch } from "@/lib/auth";
import { formatDate, formatNumber } from "@/lib/utils";

type Automation = {
  id: string;
  name: string;
  description?: string;
  status: string;
  trigger?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  steps?: Array<{ step_type: string; position: number }>;
};

export default function AutomationsView() {
  const automationsQuery = useQuery({
    queryKey: ["automations"],
    queryFn: async () => {
      const res = await apiFetch<{ data: Automation[] }>("/automations");
      return res.data ?? [];
    },
  });

  const automations = automationsQuery.data ?? [];
  const activeCount = automations.filter((a) => a.status === "active" || a.status === "running").length;
  const totalContacts = automations.length;

  if (automationsQuery.isLoading) {
    return (
      <div>
        <PageHeader eyebrow="Marketing" title="Automations" description="Loading your automation flows..." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Automations"
        description="Send the right message automatically when contacts take an action."
        actions={
          <Link href="/automations/new" className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
            <Plus className="h-4 w-4" /> New automation
          </Link>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Active automations" value={activeCount} icon={<Zap className="h-4 w-4" />} changeLabel="running" />
        <StatCard label="Contacts in flows" value={totalContacts} icon={<Play className="h-4 w-4" />} />
        <StatCard label="Total automations" value={automations.length} icon={<Workflow className="h-4 w-4" />} />
      </div>

      {automations.length === 0 ? (
        <Card className="mt-5 border-dashed">
          <CardContent className="p-8 text-center">
            <Zap className="h-10 w-10 mx-auto text-slate-400 mb-3" />
            <h3 className="text-sm font-semibold text-slate-900 mb-1">No automations yet</h3>
            <p className="text-xs text-slate-500 max-w-xs mx-auto mb-4">
              Create your first automation by building a campaign with triggers and delays.
            </p>
            <Link href="/automations/new" className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
              <Plus className="h-4 w-4" /> Create automation
            </Link>
          </CardContent>
        </Card>
      ) : (
        <div className="mt-5 grid gap-4 md:grid-cols-2">
          {automations.map((automation) => (
            <Card key={automation.id}>
              <CardHeader className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-2">
                    <CardTitle>{automation.name}</CardTitle>
                    <StatusBadge status={automation.status} />
                  </div>
                  <CardDescription>{automation.description || "No description yet."}</CardDescription>
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
                  <span className="text-slate-500">Trigger</span>
                  <span className="font-medium text-slate-900">{String((automation.trigger as Record<string, unknown> | undefined)?.type ?? "contact_added_to_list")}</span>
                </div>
                <div className="flex items-center justify-between text-sm">
                  <span className="text-slate-500">Steps</span>
                  <span className="font-medium text-slate-900">{formatNumber(automation.steps?.length ?? 0)}</span>
                </div>
                <div className="flex items-center justify-between border-t border-slate-100 pt-3 text-xs text-slate-500">
                  <span>Updated {formatDate(automation.updated_at)}</span>
                  <Link href={`/automations/${automation.id}`} className="font-medium text-indigo-600 hover:text-indigo-500">
                    Open flow
                  </Link>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
