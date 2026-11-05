"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Download, Mail, MousePointerClick, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { BarChart } from "@/components/charts";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type CampaignReport } from "@/lib/auth";
import { formatDateTime, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

export default function CampaignReportPage() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();

  const reportQuery = useQuery({
    queryKey: ["campaign-report", id],
    queryFn: async () => apiFetch<CampaignReport>(`/analytics/campaigns/${id}`),
  });

  if (reportQuery.isLoading) {
    return <PageHeader eyebrow="Campaigns" title="Campaign report" description="Loading report..." />;
  }
  if (reportQuery.error || !reportQuery.data) {
    return (
      <PageHeader
        eyebrow="Campaigns"
        title="Campaign report"
        description={reportQuery.error?.message ?? "Report not found."}
      />
    );
  }

  const report = reportQuery.data;
  const campaign = report.campaign;
  const recipientTotal = report.recipients.total;
  const byStatus = report.recipients.by_status;
  const events = report.events.by_type;
  const rates = report.rates;

  const funnel = [
    { label: "Recipients", value: recipientTotal },
    { label: "Submitted", value: byStatus.submitted ?? 0 },
    { label: "Opened", value: events.opened ?? 0 },
    { label: "Clicked", value: events.clicked ?? 0 },
  ].map((row) => ({
    ...row,
    percent: recipientTotal ? Math.min((row.value / recipientTotal) * 100, 100) : 0,
  }));

  const handleExport = () => {
    const rows = [
      ["metric", "value"],
      ["campaign", campaign?.name ?? id],
      ["status", campaign?.status ?? ""],
      ["recipients", String(recipientTotal)],
      ["submitted", String(byStatus.submitted ?? 0)],
      ["opened", String(events.opened ?? 0)],
      ["clicked", String(events.clicked ?? 0)],
      ["bounced", String(events.bounced ?? 0)],
      ["open_rate", String(rates.open_rate)],
      ["click_rate", String(rates.click_rate)],
      ["bounce_rate", String(rates.bounce_rate)],
      [],
      ["top_link", "clicks"],
      ...report.top_links.map((link) => [link.url, String(link.clicks)]),
    ];
    const csv = rows.map((row) => row.map((cell) => `"${cell.replaceAll('"', '""')}"`).join(",")).join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `campaign-report-${id}.csv`;
    anchor.click();
    URL.revokeObjectURL(url);
    toast({ title: "Report exported", description: "Your CSV download has started." });
  };

  return (
    <div>
      <Link href="/campaigns" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to campaigns
      </Link>

      <PageHeader
        eyebrow={campaign?.name ?? "Campaign"}
        title="Campaign report"
        description={
          campaign
            ? campaign.completed_at
              ? `Sent ${formatDateTime(campaign.completed_at)} to ${formatNumber(recipientTotal)} recipients.`
              : `Last updated ${formatDateTime(campaign.updated_at)}.`
            : undefined
        }
        actions={
          <button
            onClick={handleExport}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
          >
            <Download className="h-4 w-4" /> Export CSV
          </button>
        }
      />

      <div className="mb-5 flex flex-wrap items-center gap-3 text-sm text-slate-500">
        {campaign ? <StatusBadge status={campaign.status} /> : null}
        {campaign?.started_at ? <span>Started {formatDateTime(campaign.started_at)}</span> : null}
        <span>{campaign?.subject}</span>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Recipients" value={recipientTotal} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Submitted" value={byStatus.submitted ?? 0} icon={<Send className="h-4 w-4" />} />
        <StatCard label="Open rate" value={`${rates.open_rate}%`} valueIsNumber={false} icon={<Mail className="h-4 w-4" />} />
        <StatCard label="Click rate" value={`${rates.click_rate}%`} valueIsNumber={false} icon={<MousePointerClick className="h-4 w-4" />} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Engagement</CardTitle>
            <CardDescription>Event totals recorded for this campaign</CardDescription>
          </CardHeader>
          <CardContent>
            <BarChart
              data={[
                { label: "Opened", value: events.opened ?? 0 },
                { label: "Clicked", value: events.clicked ?? 0 },
                { label: "Bounced", value: events.bounced ?? 0 },
                { label: "Complained", value: events.complained ?? 0 },
              ]}
            />
            <div className="mt-4 grid grid-cols-2 gap-4 border-t border-slate-100 pt-4 text-sm sm:grid-cols-4">
              {[
                { label: "Total opens", value: events.opened ?? 0 },
                { label: "Total clicks", value: events.clicked ?? 0 },
                { label: "Bounces", value: events.bounced ?? 0 },
                { label: "Bounce rate", value: `${rates.bounce_rate}%` },
              ].map((item) => (
                <div key={item.label}>
                  <p className="text-xs text-slate-500">{item.label}</p>
                  <p className="font-semibold tabular-nums text-slate-900">
                    {typeof item.value === "number" ? formatNumber(item.value) : item.value}
                  </p>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Delivery funnel</CardTitle>
            <CardDescription>From recipients to clicks</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {funnel.map((row, index) => (
              <div key={row.label} className="flex items-center gap-3 text-sm">
                <span className="w-20 shrink-0 text-slate-600">{row.label}</span>
                <div className="h-7 flex-1 overflow-hidden rounded bg-slate-100">
                  <div
                    className="flex h-full items-center justify-end rounded bg-indigo-500 px-2 text-[10px] font-medium text-white"
                    style={{ width: `${Math.max(row.percent, 6)}%`, opacity: 1 - index * 0.14 }}
                  >
                    {row.percent.toFixed(1)}%
                  </div>
                </div>
                <span className="w-16 text-right tabular-nums text-slate-900">{formatNumber(row.value)}</span>
              </div>
            ))}
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Top clicked links</CardTitle>
            <CardDescription>Unique click events grouped by URL</CardDescription>
          </CardHeader>
          <CardContent className="px-0 py-0">
            {report.top_links.length === 0 ? (
              <div className="px-6 py-8 text-center text-sm text-slate-500">
                No clicks recorded yet. Links appear here once recipients start clicking.
              </div>
            ) : (
              <Table>
                <Thead>
                  <Tr>
                    <Th>URL</Th>
                    <Th className="text-right">Clicks</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {report.top_links.map((link) => (
                    <Tr key={link.url}>
                      <Td className="max-w-[320px] truncate font-mono text-xs text-slate-700">{link.url}</Td>
                      <Td className="text-right tabular-nums">{formatNumber(link.clicks)}</Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recipient status</CardTitle>
            <CardDescription>Snapshot at queue time</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2.5 text-sm">
            {Object.keys(byStatus).length === 0 ? (
              <p className="text-slate-500">No recipients have been queued yet.</p>
            ) : (
              Object.entries(byStatus)
                .sort((a, b) => b[1] - a[1])
                .map(([status, count]) => (
                  <div key={status} className="flex items-center justify-between gap-4">
                    <StatusBadge status={status} />
                    <span className="font-medium tabular-nums text-slate-900">{formatNumber(count)}</span>
                  </div>
                ))
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
