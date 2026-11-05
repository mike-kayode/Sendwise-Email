"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Clock, Mail, MousePointerClick, Pause, Play, Users, PencilLine, MousePointer2 } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { apiFetch, type Campaign, type CampaignReport } from "@/lib/auth";
import { formatDateTime, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type ContactList = { id: string; name: string };

export default function AutomationDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const campaignQuery = useQuery({
    queryKey: ["campaign", id],
    queryFn: async () => apiFetch<Campaign>(`/campaigns/${id}`),
  });
  const reportQuery = useQuery({
    queryKey: ["campaign-report", id],
    queryFn: async () => apiFetch<CampaignReport>(`/analytics/campaigns/${id}`),
  });
  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: async () => {
      const res = await apiFetch<{ data: ContactList[] | null }>("/lists");
      return res.data ?? [];
    },
  });

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["campaign", id] });
    void queryClient.invalidateQueries({ queryKey: ["campaign-report", id] });
  };

  const handleStatusAction = async (action: "pause" | "resume") => {
    try {
      await apiFetch(`/campaigns/${id}/${action}`, { method: "POST" });
      toast({
        title: action === "pause" ? "Automation paused" : "Automation resumed",
        description:
          action === "pause"
            ? "Queued delivery jobs will stop being processed."
            : "Delivery jobs are queued again.",
      });
      refresh();
    } catch (err: any) {
      toast({ title: "Action failed", description: err?.message ?? "Unknown error", variant: "destructive" });
    }
  };

  if (campaignQuery.isLoading || reportQuery.isLoading) {
    return <PageHeader eyebrow="Automation" title="Automation" description="Loading automation..." />;
  }
  if (campaignQuery.error || !campaignQuery.data) {
    return (
      <PageHeader
        eyebrow="Automation"
        title="Automation"
        description={campaignQuery.error?.message ?? "Automation not found."}
      />
    );
  }

  const campaign = campaignQuery.data;
  const report = reportQuery.data;
  const recipientsTotal = report?.recipients.total ?? campaign.recipient_count;
  const byStatus = report?.recipients.by_status ?? {};
  const events = report?.events.by_type ?? {};
  const rates = report?.rates ?? { open_rate: 0, click_rate: 0, bounce_rate: 0 };

  const lists = listsQuery.data ?? [];
  const audienceLabel = campaign.list_id
    ? (lists.find((l) => l.id === campaign.list_id)?.name ?? "Selected list")
    : campaign.segment_id
      ? "Saved segment"
      : "All subscribed contacts";

  const isPaused = campaign.status === "paused";
  const isPausable = ["scheduled", "processing", "sending"].includes(campaign.status);
  const isDraft = campaign.status === "draft";

  const steps = [
    {
      type: "trigger",
      title: "Contact enters",
      detail: audienceLabel,
      icon: Users,
    },
    {
      type: "email",
      title: campaign.subject || "(no subject line)",
      detail: `From ${campaign.sender_name || campaign.sender_email}${campaign.sender_email ? ` <${campaign.sender_email}>` : ""}`,
      icon: Mail,
    },
    {
      type: "status",
      title:
        campaign.status === "completed"
          ? "Delivery completed"
          : campaign.status === "sending" || campaign.status === "processing"
            ? "Delivery in progress"
            : campaign.status === "paused"
              ? "Delivery paused"
              : campaign.status === "scheduled"
                ? `Scheduled${campaign.scheduled_at ? ` for ${formatDateTime(campaign.scheduled_at)}` : ""}`
                : "Waiting to be sent",
      detail: campaign.completed_at
        ? `Finished ${formatDateTime(campaign.completed_at)}`
        : campaign.started_at
          ? `Started ${formatDateTime(campaign.started_at)}`
          : "Send it from the preview page when the content is ready",
      icon: Clock,
    },
  ];

  return (
    <div>
      <Link href="/automations" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to automations
      </Link>

      <PageHeader
        eyebrow="Automation"
        title={campaign.name}
        description={campaign.preview_text || campaign.subject}
        actions={
          <>
            {isPausable ? (
              <button
                onClick={() => handleStatusAction("pause")}
                className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
              >
                <Pause className="h-4 w-4" /> Pause
              </button>
            ) : null}
            {isPaused ? (
              <button
                onClick={() => handleStatusAction("resume")}
                className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
              >
                <Play className="h-4 w-4" /> Resume
              </button>
            ) : null}
            <Link
              href={isDraft ? `/campaigns/${campaign.id}/editor` : `/campaigns/${campaign.id}/preview`}
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-emerald-800 px-4 text-sm font-medium text-white hover:bg-emerald-900"
            >
              <PencilLine className="h-4 w-4" /> {isDraft ? "Edit email" : "Preview & send"}
            </Link>
          </>
        }
      />

      <div className="mb-5 flex flex-wrap items-center gap-3 text-sm text-slate-500">
        <StatusBadge status={campaign.status} />
        <span className="inline-flex items-center gap-1">
          <MousePointer2 className="h-3.5 w-3.5" /> 1 email step
        </span>
        <span>·</span>
        <span>{formatNumber(recipientsTotal)} contacts in flow</span>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Contacts in flow" value={recipientsTotal} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Emails sent" value={byStatus.submitted ?? 0} icon={<Mail className="h-4 w-4" />} />
        <StatCard label="Opens" value={events.opened ?? 0} icon={<MousePointer2 className="h-4 w-4" />} />
        <StatCard label="Click rate" value={`${rates.click_rate}%`} valueIsNumber={false} icon={<MousePointerClick className="h-4 w-4" />} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Flow</CardTitle>
            <CardDescription>Trigger, email, and delivery status for this automation.</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-0">
              {steps.map((step, index) => {
                const Icon = step.icon;
                return (
                  <div key={`${step.type}-${index}`}>
                    <div className="flex items-start gap-3 rounded-lg border border-slate-200 bg-white p-3.5">
                      <span
                        className={
                          step.type === "trigger"
                            ? "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600"
                            : step.type === "status"
                              ? "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-amber-50 text-amber-600"
                              : "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600"
                        }
                      >
                        <Icon className="h-4 w-4" />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <p className="text-sm font-medium text-slate-900">{step.title}</p>
                          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[10px] uppercase tracking-wide text-slate-500">
                            {step.type}
                          </span>
                        </div>
                        <p className="text-xs text-slate-500">{step.detail}</p>
                      </div>
                      <Link
                        href={index === 1 ? `/campaigns/${campaign.id}/editor` : "/audience/lists"}
                        className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
                      >
                        {index === 1 ? "Edit" : "View"}
                      </Link>
                    </div>
                    {index < steps.length - 1 ? (
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
                <span className="text-right font-medium text-slate-900">{audienceLabel}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Sender</span>
                <span className="text-right font-medium text-slate-900">{campaign.sender_email || "—"}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Reply-to</span>
                <span className="text-right font-medium text-slate-900">{campaign.reply_to || "Same as sender"}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Open tracking</span>
                <span className="text-right font-medium text-slate-900">{campaign.track_opens ? "On" : "Off"}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-slate-500">Click tracking</span>
                <span className="text-right font-medium text-slate-900">{campaign.track_clicks ? "On" : "Off"}</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Top clicked links</CardTitle>
              <CardDescription>Live click activity</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              {report && report.top_links.length > 0 ? (
                report.top_links.slice(0, 5).map((link) => (
                  <div key={link.url} className="flex items-center justify-between gap-3">
                    <span className="truncate text-slate-600">{link.url}</span>
                    <span className="shrink-0 font-medium tabular-nums text-slate-900">{formatNumber(link.clicks)}</span>
                  </div>
                ))
              ) : (
                <p className="text-slate-500">No click activity recorded yet.</p>
              )}
              <Link
                href={`/campaigns/${campaign.id}/report`}
                className="block border-t border-slate-100 pt-3 text-xs font-medium text-indigo-600 hover:text-indigo-500"
              >
                Open full report
              </Link>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
