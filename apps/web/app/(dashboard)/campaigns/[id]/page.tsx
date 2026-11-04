"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Copy, Eye, Mail, MousePointerClick, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { ProgressBar } from "@/components/charts";
import { apiFetch, type Campaign } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";
import { formatDate, formatNumber } from "@/lib/utils";

type CampaignReport = {
  recipients: { total: number; by_status: Record<string, number> };
  events: { by_type: Record<string, number> };
  rates: { open_rate: number; click_rate: number; bounce_rate: number };
  top_links: { url: string; clicks: number }[];
};

export default function CampaignDetailPage() {
  const { id } = useParams<{ id: string }>();
  const campaignQuery = useQuery({
    queryKey: ["campaign", id],
    queryFn: async () => apiFetch<Campaign>(`/campaigns/${id}`),
  });
  const reportQuery = useQuery({
    queryKey: ["campaign-report", id],
    queryFn: async () => apiFetch<CampaignReport>(`/analytics/campaigns/${id}`),
  });

  if (campaignQuery.isLoading || reportQuery.isLoading) {
    return <PageHeader eyebrow="Campaigns" title="Campaign" description="Loading campaign..." />;
  }

  if (campaignQuery.error || reportQuery.error) {
    const error = campaignQuery.error ?? reportQuery.error;
    return <PageHeader eyebrow="Campaigns" title="Campaign" description={error?.message ?? "Could not load campaign."} />;
  }

  const campaign = campaignQuery.data;
  const report = reportQuery.data;
  if (!campaign || !report) {
    return <PageHeader eyebrow="Campaigns" title="Campaign" description="Campaign not found." />;
  }

  const recipientTotal = report.recipients.total;
  const recipientStatuses = report.recipients.by_status;
  const events = report.events.by_type;
  const isDraft = campaign.status === "draft";
  const deliveryRows = [
    { label: "Recipients", value: recipientTotal },
    { label: "Submitted", value: recipientStatuses.submitted ?? 0 },
    { label: "Accepted", value: recipientStatuses.accepted ?? 0 },
    { label: "Open events", value: events.opened ?? 0 },
    { label: "Click events", value: events.clicked ?? 0 },
  ].map((row) => ({
    ...row,
    percent: recipientTotal ? Math.min((row.value / recipientTotal) * 100, 100) : 0,
  }));

  return (
    <div>
      <Link href="/campaigns" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to campaigns
      </Link>
      <PageHeader
        eyebrow={campaign.list_id ? "Selected list" : campaign.segment_id ? "Saved segment" : "All eligible contacts"}
        title={campaign.name}
        description={campaign.preview_text || campaign.subject}
        actions={
          <>
            <Link href={`/campaigns/${campaign.id}/preview`} className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
              <Eye className="h-4 w-4" /> Preview
            </Link>
            <Link href={isDraft ? `/campaigns/${campaign.id}/editor` : `/campaigns/${campaign.id}/report`} className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-emerald-800 px-4 text-sm font-medium text-white hover:bg-emerald-900">
              <Send className="h-4 w-4" /> {isDraft ? "Edit campaign" : "View report"}
            </Link>
          </>
        }
      />

      <div className="mb-5 flex flex-wrap items-center gap-3 text-sm text-slate-500">
        <StatusBadge status={campaign.status} />
        <span>Created {formatDate(campaign.created_at)}</span>
        <span>{formatNumber(campaign.recipient_count)} recipients</span>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Recipients" value={recipientTotal} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Accepted" value={recipientStatuses.accepted ?? 0} icon={<Mail className="h-4 w-4" />} />
        <StatCard label="Open rate" value={`${report.rates.open_rate}%`} valueIsNumber={false} icon={<Eye className="h-4 w-4" />} />
        <StatCard label="Click rate" value={`${report.rates.click_rate}%`} valueIsNumber={false} icon={<MousePointerClick className="h-4 w-4" />} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Delivery activity</CardTitle>
            <CardDescription>Recipient and engagement events</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {deliveryRows.map((row) => (
              <div key={row.label}>
                <div className="mb-1.5 flex items-center justify-between text-sm">
                  <span className="text-slate-600">{row.label}</span>
                  <span className="font-medium tabular-nums text-slate-900">{formatNumber(row.value)} · {row.percent.toFixed(1)}%</span>
                </div>
                <ProgressBar value={row.percent} />
              </div>
            ))}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Campaign settings</CardTitle>
            <CardDescription>Configured sender and tracking</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="flex justify-between gap-4"><span className="text-slate-500">Sender</span><span className="text-right font-medium text-slate-900">{campaign.sender_email}</span></div>
            <div className="flex justify-between gap-4"><span className="text-slate-500">Reply-to</span><span className="text-right font-medium text-slate-900">{campaign.reply_to || "Same as sender"}</span></div>
            <div className="flex justify-between gap-4"><span className="text-slate-500">Open tracking</span><span className="text-right font-medium text-slate-900">{campaign.track_opens ? "On" : "Off"}</span></div>
            <div className="flex justify-between gap-4"><span className="text-slate-500">Click tracking</span><span className="text-right font-medium text-slate-900">{campaign.track_clicks ? "On" : "Off"}</span></div>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Top clicked links</CardTitle>
          <CardDescription>Click events grouped by URL</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {report.top_links.length ? report.top_links.map((link) => (
            <div key={link.url} className="flex items-center justify-between gap-4 text-sm">
              <span className="truncate text-slate-600">{link.url}</span>
              <span className="shrink-0 font-medium tabular-nums text-slate-900">{formatNumber(link.clicks)}</span>
            </div>
          )) : <p className="text-sm text-slate-500">No click activity recorded yet.</p>}
        </CardContent>
      </Card>
    </div>
  );
}
