import Link from "next/link";
import type { Metadata } from "next";
import { ArrowLeft, Download } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { BarChart, DonutChart } from "@/components/charts";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { formatDateTime, formatNumber } from "@/lib/utils";

const campaigns = [
  { id: "cmp_01", name: "Spring launch", subject: "Launch day is here", audience: "Newsletter subscribers" },
  { id: "cmp_02", name: "Product digest", subject: "This week’s product updates", audience: "Product users" },
  { id: "cmp_03", name: "Lifecycle refresh", subject: "Your onboarding checklist", audience: "Trial accounts" },
];

const campaignReport = {
  recipients: 18420,
  submitted: 18420,
  delivered: 18280,
  uniqueOpens: 7836,
  uniqueClicks: 1123,
  opens: 11340,
  clicks: 1624,
  bounces: 201,
  unsubscribes: 78,
  complaints: 7,
  sentAt: "2026-04-28T09:00:00Z",
  openRate: 42.6,
  clickRate: 6.1,
  bounceRate: 1.1,
  topLinks: [
    { url: "https://acme.studio/launch", clicks: 620 },
    { url: "https://acme.studio/product", clicks: 390 },
    { url: "https://acme.studio/pricing", clicks: 220 },
  ],
};

const engagementSeries = [
  { label: "Mon", opens: 1500 }, { label: "Tue", opens: 1800 }, { label: "Wed", opens: 2100 }, { label: "Thu", opens: 2600 }, { label: "Fri", opens: 2300 }, { label: "Sat", opens: 1900 }, { label: "Sun", opens: 2200 },
];

export const metadata: Metadata = { title: "Campaign report" };

export default async function CampaignReportPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const campaign = campaigns.find((item) => item.id === id) ?? campaigns[0];

  const funnel = [
    { label: "Recipients", value: campaignReport.recipients },
    { label: "Submitted", value: campaignReport.submitted },
    { label: "Delivered", value: campaignReport.delivered },
    { label: "Opened", value: campaignReport.uniqueOpens },
    { label: "Clicked", value: campaignReport.uniqueClicks },
  ];

  return (
    <div>
      <Link href={`/campaigns/${campaign.id}`} className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to campaign
      </Link>

      <PageHeader
        eyebrow={campaign.name}
        title="Campaign report"
        description={`Sent ${formatDateTime(campaignReport.sentAt)} to ${formatNumber(campaignReport.recipients)} recipients.`}
        actions={
          <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
            <Download className="h-4 w-4" /> Export CSV
          </button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Delivered" value={campaignReport.delivered} change={1.2} changeLabel="vs previous send" valueIsNumber />
        <StatCard label="Open rate" value={`${campaignReport.openRate}%`} change={2.3} valueIsNumber={false} />
        <StatCard label="Click rate" value={`${campaignReport.clickRate}%`} change={0.8} valueIsNumber={false} />
        <StatCard label="Bounce rate" value={`${campaignReport.bounceRate}%`} change={-0.2} valueIsNumber={false} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Engagement over time</CardTitle>
            <CardDescription>Opens and clicks after delivery</CardDescription>
          </CardHeader>
          <CardContent>
            <BarChart
              data={engagementSeries.map((point) => ({ label: point.label, value: point.opens }))}
              color="bg-indigo-500"
            />
            <div className="mt-4 grid grid-cols-2 gap-4 border-t border-slate-100 pt-4 text-sm sm:grid-cols-4">
              <div>
                <p className="text-xs text-slate-500">Total opens</p>
                <p className="font-semibold tabular-nums text-slate-900">{formatNumber(campaignReport.opens)}</p>
              </div>
              <div>
                <p className="text-xs text-slate-500">Unique opens</p>
                <p className="font-semibold tabular-nums text-slate-900">{formatNumber(campaignReport.uniqueOpens)}</p>
              </div>
              <div>
                <p className="text-xs text-slate-500">Total clicks</p>
                <p className="font-semibold tabular-nums text-slate-900">{formatNumber(campaignReport.clicks)}</p>
              </div>
              <div>
                <p className="text-xs text-slate-500">Unique clicks</p>
                <p className="font-semibold tabular-nums text-slate-900">{formatNumber(campaignReport.uniqueClicks)}</p>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Audience engagement</CardTitle>
            <CardDescription>Share of delivered mail</CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <DonutChart value={48} label="open" sublabel="8,728 of 18,102 delivered" />
            <div className="grid grid-cols-2 gap-3 border-t border-slate-100 pt-4 text-sm">
              <div>
                <p className="text-xs text-slate-500">Bounces</p>
                <p className="font-semibold text-slate-900">{formatNumber(campaignReport.bounces)}</p>
              </div>
              <div>
                <p className="text-xs text-slate-500">Unsubscribes</p>
                <p className="font-semibold text-slate-900">{formatNumber(campaignReport.unsubscribes)}</p>
              </div>
              <div>
                <p className="text-xs text-slate-500">Complaints</p>
                <p className="font-semibold text-slate-900">{formatNumber(campaignReport.complaints)}</p>
              </div>
              <div>
                <p className="text-xs text-slate-500">Delivered</p>
                <p className="font-semibold text-slate-900">{formatNumber(campaignReport.delivered)}</p>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Delivery funnel</CardTitle>
            <CardDescription>Recipients to unique clicks</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {funnel.map((row, index) => {
              const percent = (row.value / funnel[0].value) * 100;
              return (
                <div key={row.label} className="flex items-center gap-3 text-sm">
                  <span className="w-24 shrink-0 text-slate-500">{row.label}</span>
                  <div className="h-7 flex-1 overflow-hidden rounded bg-slate-100">
                    <div
                      className="flex h-full items-center justify-end rounded bg-indigo-500 pr-2 text-[10px] font-medium text-white"
                      style={{ width: `${Math.max(percent, 6)}%`, opacity: 1 - index * 0.14 }}
                    >
                      {percent.toFixed(1)}%
                    </div>
                  </div>
                  <span className="w-16 shrink-0 text-right tabular-nums text-slate-900">{formatNumber(row.value)}</span>
                </div>
              );
            })}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Top links</CardTitle>
            <CardDescription>Unique clicks by URL</CardDescription>
          </CardHeader>
          <CardContent className="px-0 py-0">
            <Table>
              <Thead>
                <Tr>
                  <Th>URL</Th>
                  <Th className="text-right">Clicks</Th>
                </Tr>
              </Thead>
              <Tbody>
                {campaignReport.topLinks.map((link) => (
                  <Tr key={link.url}>
                    <Td className="max-w-[260px] truncate text-xs text-slate-600">{link.url}</Td>
                    <Td className="text-right tabular-nums">{formatNumber(link.clicks)}</Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
