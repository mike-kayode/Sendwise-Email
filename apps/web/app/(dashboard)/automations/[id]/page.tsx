"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Pause, Play, Workflow, Clock3 } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { apiFetch } from "@/lib/auth";
import { formatDate } from "@/lib/utils";

type Automation = {
  id: string;
  name: string;
  description?: string;
  status: string;
  trigger?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  steps?: Array<{ id: string; step_type: string; position: number; config?: Record<string, unknown> }>;
};

export default function AutomationDetailPage() {
  const { id } = useParams<{ id: string }>();

  const automationQuery = useQuery({
    queryKey: ["automation", id],
    queryFn: async () => apiFetch<Automation>(`/automations/${id}`),
  });

  if (automationQuery.isLoading) {
    return <PageHeader eyebrow="Automation" title="Automation" description="Loading automation..." />;
  }
  if (automationQuery.error || !automationQuery.data) {
    return <PageHeader eyebrow="Automation" title="Automation" description="Automation could not be loaded." />;
  }

  const automation = automationQuery.data;
  const steps = automation.steps ?? [];
  const triggerType = String((automation.trigger as Record<string, unknown> | undefined)?.type ?? "contact_added_to_list");

  return (
    <div>
      <Link href="/automations" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to automations
      </Link>

      <PageHeader
        eyebrow="Automation"
        title={automation.name}
        description={automation.description || "No description provided."}
        actions={
          automation.status === "active" ? (
            <button
              type="button"
              onClick={() => void apiFetch(`/automations/${id}/pause`, { method: "POST" })}
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
            >
              <Pause className="h-4 w-4" /> Pause
            </button>
          ) : (
            <button
              type="button"
              onClick={() => void apiFetch(`/automations/${id}/activate`, { method: "POST" })}
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
            >
              <Play className="h-4 w-4" /> Activate
            </button>
          )
        }
      />

      <div className="mb-5 flex flex-wrap items-center gap-3 text-sm text-slate-500">
        <StatusBadge status={automation.status} />
        <span className="inline-flex items-center gap-1"><Workflow className="h-3.5 w-3.5" /> {triggerType}</span>
        <span>·</span>
        <span>{steps.length} step(s)</span>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Flow details</CardTitle>
          <CardDescription>Updated {formatDate(automation.updated_at)}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {steps.length === 0 ? (
            <p className="text-sm text-slate-500">No steps yet. Add a send email step to make the automation active.</p>
          ) : (
            steps.map((step) => (
              <div key={step.id} className="flex items-center gap-3 rounded-lg border border-slate-200 bg-white p-3">
                <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-slate-100 text-slate-700">
                  <Clock3 className="h-4 w-4" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="font-medium text-slate-900">{step.step_type}</div>
                  <div className="text-xs text-slate-500">Position {step.position}</div>
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>
    </div>
  );
}
