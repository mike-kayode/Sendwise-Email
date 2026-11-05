"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { Download, Mail, MousePointerClick, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { BarChart, DonutChart } from "@/components/charts";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type AnalyticsOverview, type Campaign, type CampaignReport, type ListResponse } from "@/lib/auth";
import { formatDate, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type CampaignRow = { campaign: Campaign; report: CampaignReport | null };

export default function ReportsPage() {
  const { toast } = useToast();

  const overviewQuery = useQuery({
    queryKey: ["analytics", "overview"],
    queryFn: async () => apiFetch<AnalyticsOverview>("/analytics/overview"),
  });

  const campaignsQuery = useQuery({
    queryKey: ["reports", "campaigns"],
    queryFn: async () => {
      const list = await apiFetch<ListResponse<Campaign>>("/campaigns?limit=8");
      const top = (list.data ?? []).slice(0, 6);
      const reports = await Promise.all(
        top.map((campaign) =>
          apiFetch<CampaignReport>(`/analytics/campaigns/${campaign.id}`).catch(() => null),
        ),
      );
      return top.map((campaign, index) => ({ campaign, report: reports[index] })) satisfies CampaignRow[];
    },
  });

  const overview = overviewQuery.data;
  const rows = campaignsQuery.data ?? [];

  if (overviewQuery.isLoading || campaignsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader eyebrow="Analytics" title="Reports" description="Loading your reports..." />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <StatCard key={i} label="Loading" value="—" />
          ))}
        </div>
      </div>
    );
  }

  if (overviewQuery.error || !overview) {
    return (
      <PageHeader
        eyebrow="Analytics"
        title="Reports"
        description={overviewQuery.error?.message ?? "Could not load reports."}
      />
    );
  }

  const submitted = overview.emails_submitted;
  const rateOf = (count: number) => (submitted > 0 ? Math.round((count / submitted) * 10000) / 100 : 0);
  const deliveredRate = submitted > 0 ? Math.round((100 - overview.bounce_rate) * 100) / 100 : 0;

  const handleExport = () => {
    const csvRows = [
      ["campaign", "status", "recipients", "open_rate", "click_rate", "sent_at"],
      ...rows.map(({ campaign, report }) => [
        campaign.name,
        campaign.status,
        String(report?.recipients.total ?? campaign.recipient_count),
        report ? String(report.rates.open_rate) : "",
        report ? String(report.rates.click_rate) : "",
        campaign.completed_at ?? campaign.started_at ?? "",
      ]),
    ];
    const csv = csvRows.map((row) => row.map((cell) => `"${cell.replaceAll('"', '""')}"`).join(",")).join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "campaign-reports.csv";
    anchor.click();
    URL.revokeObjectURL(url);
    toast({ title: "Export ready", description: "Your campaign report CSV has downloaded." });
  };

  return (
    <div>
      <PageHeader
        eyebrow="Analytics"
        title="Reports"
        description="Understand what is working across your audience and campaigns."
        actions={
          <button
            onClick={handleExport}
            disabled={rows.length === 0}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50 disabled:opacity-50"
          >
            <Download className="h-4 w-4" /> Export CSV
          </button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Emails sent" value={overview.emails_submitted} icon={<Send className="h-4 w-4" />} />
        <StatCard label="Opens (30 days)" value={overview.opens} icon={<Mail className="h-4 w-4" />} />
        <StatCard label="Clicks (30 days)" value={overview.clicks} icon={<MousePointerClick className="h-4 w-4" />} />
        <StatCard label="Contacts" value={overview.contacts} icon={<Users className="h-4 w-4" />} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Campaign performance</CardTitle>
            <CardDescription>Your six most recent campaigns with live engagement rates</CardDescription>
          </CardHeader>
          <CardContent className="px-0 py-0">
            {rows.length === 0 ? (
              <div className="px-6 py-10 text-center text-sm text-slate-500">
                No campaigns yet.{" "}
                <Link href="/campaigns/new" className="font-medium text-indigo-600 hover:text-indigo-500">
                  Create your first campaign
                </Link>{" "}
                to see its report here.
              </div>
            ) : (
              <Table>
                <Thead>
                  <Tr>
                    <Th>Campaign</Th>
                    <Th>Status</Th>
                    <Th className="text-right">Recipients</Th>
                    <Th className="text-right">Opens</Th>
                    <Th className="text-right">Clicks</Th>
                    <Th>Updated</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {rows.map(({ campaign, report }) => (
                    <Tr key={campaign.id}>
                      <Td>
                        <Link
                          href={`/campaigns/${campaign.id}/report`}
                          className="font-medium text-slate-900 hover:text-indigo-600"
                        >
                          {campaign.name}
                        </Link>
                      </Td>
                      <Td>
                        <StatusBadge status={campaign.status} />
                      </Td>
                      <Td className="text-right tabular-nums">
                        {formatNumber(report?.recipients.total ?? campaign.recipient_count)}
                      </Td>
                      <Td className="text-right tabular-nums">
                        {report && report.recipients.total > 0 ? `${report.rates.open_rate}%` : "—"}
                      </Td>
                      <Td className="text-right tabular-nums">
                        {report && report.recipients.total > 0 ? `${report.rates.click_rate}%` : "—"}
                      </Td>
                      <Td className="text-xs text-slate-500">{formatDate(campaign.updated_at)}</Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Engagement mix</CardTitle>
            <CardDescription>Across all submitted email (30-day window)</CardDescription>
          </CardHeader>
          <CardContent>
            <DonutChart
              value={Math.round(overview.open_rate)}
              label="open"
              sublabel={`${overview.open_rate}% open · ${overview.click_rate}% click`}
            />
          </CardContent>
        </Card>
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Email activity</CardTitle>
            <CardDescription>
              {submitted > 0 ? `${formatNumber(submitted)} emails submitted in total` : "No email has been submitted yet"}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <BarChart
              data={[
                { label: "Sent", value: overview.emails_submitted },
                { label: "Opens", value: overview.opens },
                { label: "Clicks", value: overview.clicks },
                { label: "Bounces", value: overview.bounces },
              ]}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Deliverability</CardTitle>
            <CardDescription>Lifetime submission quality</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2 border-b border-slate-100 pb-3 text-sm">
              {[
                { label: "Delivered", value: submitted > 0 ? `${deliveredRate}%` : "—" },
                { label: "Bounced", value: submitted > 0 ? `${overview.bounce_rate}%` : "—" },
                { label: "Complaints", value: `${formatNumber(overview.complaints)} (${rateOf(overview.complaints)}%)` },
                { label: "Unsubscribes", value: `${formatNumber(overview.unsubscribes)} (${rateOf(overview.unsubscribes)}%)` },
              ].map((item) => (
                <div key={item.label} className="flex justify-between">
                  <span className="text-slate-500">{item.label}</span>
                  <span className="font-medium text-slate-900">{item.value}</span>
                </div>
              ))}
            </div>
            <div className="space-y-2 text-sm">
              <div className="flex justify-between">
                <span className="text-slate-500">Subscribed contacts</span>
                <span className="font-medium text-slate-900">{formatNumber(overview.subscribed)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-500">Campaigns sent</span>
                <span className="font-medium text-slate-900">{formatNumber(overview.campaigns_sent)}</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
