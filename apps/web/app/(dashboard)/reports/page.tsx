import Link from "next/link";
import type { Metadata } from "next";
import { Download, Mail, MousePointerClick, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { BarChart, DonutChart } from "@/components/charts";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { formatDate, formatNumber } from "@/lib/utils";

const campaigns = [
  { id: "cmp_01", name: "Spring launch", status: "completed", recipients: 18420, openRate: 42.1, clickRate: 6.1, date: "2026-04-28T00:00:00Z" },
  { id: "cmp_02", name: "Product digest", status: "sending", recipients: 12840, openRate: 39.4, clickRate: 5.5, date: "2026-05-02T00:00:00Z" },
  { id: "cmp_03", name: "Lifecycle refresh", status: "scheduled", recipients: 9600, openRate: 0, clickRate: 0, date: "2026-05-09T00:00:00Z" },
  { id: "cmp_04", name: "Referral push", status: "draft", recipients: 0, openRate: 0, clickRate: 0, date: "2026-05-12T00:00:00Z" },
];

const growthSeries = [
  { label: "Jan", value: 3200 }, { label: "Feb", value: 4200 }, { label: "Mar", value: 5400 }, { label: "Apr", value: 6100 }, { label: "May", value: 7600 }, { label: "Jun", value: 8900 }, { label: "Jul", value: 10300 },
];

const engagementSeries = [
  { label: "W1", opens: 2100 }, { label: "W2", opens: 2400 }, { label: "W3", opens: 2700 }, { label: "W4", opens: 2900 }, { label: "W5", opens: 3100 }, { label: "W6", opens: 3400 }, { label: "W7", opens: 3600 },
];

export const metadata: Metadata = { title: "Reports" };

export default function ReportsPage() {
  return (
    <div>
      <PageHeader
        title="Reports"
        description="Understand what is working across your audience, campaigns, and automations."
        actions={
          <>
            <select className="h-9 rounded-lg border-0 bg-white px-3 text-sm text-slate-700 ring-1 ring-inset ring-slate-200">
              <option>Last 30 days</option>
              <option>Last 7 days</option>
              <option>Last 90 days</option>
              <option>This year</option>
            </select>
            <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
              <Download className="h-4 w-4" /> Export
            </button>
          </>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Emails sent" value={184320} change={8.1} icon={<Send className="h-4 w-4" />} />
        <StatCard label="Unique opens" value={78931} change={3.6} icon={<Mail className="h-4 w-4" />} />
        <StatCard label="Unique clicks" value={11428} change={1.8} icon={<MousePointerClick className="h-4 w-4" />} />
        <StatCard label="New contacts" value={2840} change={12.4} icon={<Users className="h-4 w-4" />} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Audience growth</CardTitle>
            <CardDescription>Subscribed contacts over the last 7 months</CardDescription>
          </CardHeader>
          <CardContent>
            <BarChart data={growthSeries} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Engagement mix</CardTitle>
            <CardDescription>Average across all campaigns</CardDescription>
          </CardHeader>
          <CardContent>
            <DonutChart value={43} label="open" sublabel="42.8% open · 6.2% click" />
          </CardContent>
        </Card>
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Campaign performance</CardTitle>
            <CardDescription>Completed and scheduled campaigns</CardDescription>
          </CardHeader>
          <CardContent className="px-0 py-0">
            <Table>
              <Thead>
                <Tr>
                  <Th>Campaign</Th>
                  <Th>Status</Th>
                  <Th className="text-right">Recipients</Th>
                  <Th className="text-right">Opens</Th>
                  <Th className="text-right">Clicks</Th>
                  <Th>Sent</Th>
                </Tr>
              </Thead>
              <Tbody>
                {campaigns.slice(0, 5).map((campaign) => (
                  <Tr key={campaign.id}>
                    <Td>
                      <Link href={`/campaigns/${campaign.id}/report`} className="font-medium text-slate-900 hover:text-indigo-600">
                        {campaign.name}
                      </Link>
                    </Td>
                    <Td>
                      <StatusBadge status={campaign.status} />
                    </Td>
                    <Td className="text-right tabular-nums">{formatNumber(campaign.recipients)}</Td>
                    <Td className="text-right tabular-nums">{campaign.openRate ? `${campaign.openRate}%` : "—"}</Td>
                    <Td className="text-right tabular-nums">{campaign.clickRate ? `${campaign.clickRate}%` : "—"}</Td>
                    <Td className="text-xs text-slate-500">{formatDate(campaign.date)}</Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Deliverability</CardTitle>
            <CardDescription>Last 30 days</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <BarChart
              data={engagementSeries.map((point) => ({ label: point.label, value: point.opens }))}
              height={132}
              color="bg-emerald-500"
            />
            <div className="space-y-2 border-t border-slate-100 pt-3 text-sm">
              {[
                { label: "Delivered", value: "98.9%" },
                { label: "Bounced", value: "1.1%" },
                { label: "Complaints", value: "0.02%" },
                { label: "Unsubscribes", value: "0.3%" },
              ].map((item) => (
                <div key={item.label} className="flex justify-between">
                  <span className="text-slate-500">{item.label}</span>
                  <span className="font-medium text-slate-900">{item.value}</span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
