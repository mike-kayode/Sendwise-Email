"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ArrowDownRight, ArrowRight, ArrowUpRight, Mail, MousePointerClick, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import {
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
} from "@/components/ui/table";
import { apiFetch } from "@/lib/auth";
import { formatDate, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type ListResponse<T> = {
  data: T[];
  pagination: { total: number; limit: number; offset: number };
};

type Campaign = {
  id: string;
  name: string;
  subject: string;
  status: string;
  recipient_count: number;
  created_at: string;
};

type AnalyticsOverview = {
  contacts: number;
  subscribed: number;
  emails_submitted: number;
  open_rate: number;
  click_rate: number;
};

function StatCard({
  label,
  value,
  change,
  changeLabel = "vs last period",
  icon,
  valueIsNumber = true,
}: {
  label: string;
  value: string | number;
  change?: number;
  changeLabel?: string;
  icon?: React.ReactNode;
  valueIsNumber?: boolean;
}) {
  const positive = (change ?? 0) >= 0;
  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-slate-500">{label}</p>
          <p className="mt-2 text-2xl font-semibold tracking-tight text-slate-900">
            {valueIsNumber && typeof value === "number" ? formatNumber(value) : value}
          </p>
        </div>
        {icon ? (
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-50 text-emerald-700">
            {icon}
          </span>
        ) : null}
      </div>
      {change !== undefined ? (
        <div className="mt-3 flex items-center gap-2 text-xs">
          <span
            className={`inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 font-medium ${positive ? "bg-emerald-50 text-emerald-700" : "bg-rose-50 text-rose-700"}`}
          >
            {positive ? <ArrowUpRight className="h-3 w-3" /> : <ArrowDownRight className="h-3 w-3" />}
            {Math.abs(change).toFixed(1)}%
          </span>
          <span className="text-slate-400">{changeLabel}</span>
        </div>
      ) : null}
    </Card>
  );
}

function SkeletonCard() {
  return (
    <Card className="p-5">
      <div className="h-4 w-3/4 rounded-full bg-muted pulse-text" />
    </Card>
  );
}


export default function DashboardPage() {
  const { toast } = useToast();

  const overview = useQuery({
    queryKey: ["analytics", "overview"],
    queryFn: async () =>
      apiFetch<AnalyticsOverview>("/analytics/overview"),
  });

  const campaigns = useQuery({
    queryKey: ["campaigns"],
    queryFn: async () =>
      apiFetch<ListResponse<Campaign>>("/campaigns?limit=4"),
  });

  if (overview.isLoading || campaigns.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader title="Dashboard" description="Loading your workspace data..." />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      </div>
    );
  }

  if (overview.error || campaigns.error) {
    toast({
      title: "Unable to load dashboard",
      description:
        (overview.error ?? campaigns.error)?.message ??
        "Please check your connection and try again.",
      variant: "destructive",
    });
  }

  const data = overview.data;
  const campaignsList = campaigns.data?.data ?? [];
  const recentCampaigns = campaignsList.slice(0, 4);

  if (!data) {
    return (
      <div className="space-y-6">
        <PageHeader title="Dashboard" description="Loading your workspace data..." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Dashboard"
        description="Here's what's happening with your email marketing today."
        actions={
          <Link
            href="/campaigns/new"
            className="inline-flex h-10 items-center gap-2 rounded-lg bg-emerald-800 px-4 text-sm font-semibold text-white shadow-sm hover:bg-emerald-900"
          >
            <Send className="h-4 w-4" /> Create campaign
          </Link>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label="Total contacts"
          value={data.contacts}
          change={12.4}
          icon={<Users className="h-5 w-5" />}
        />
        <StatCard
          label="Emails submitted"
          value={data.emails_submitted}
          change={8.1}
          icon={<Send className="h-5 w-5" />}
        />
        <StatCard
          label="Open rate"
          value={data.open_rate.toFixed(1) + "%"}
          change={2.3}
          valueIsNumber={false}
          icon={<Mail className="h-5 w-5" />}
        />
        <StatCard
          label="Click-through rate"
          value={data.click_rate.toFixed(1) + "%"}
          change={-0.4}
          valueIsNumber={false}
          icon={<MousePointerClick className="h-5 w-5" />}
        />
      </div>

      <div className="mt-5">
        <Card>
          <CardHeader className="flex items-center justify-between">
            <div>
              <CardTitle>Recent campaigns</CardTitle>
              <CardDescription>Track the latest activity across your campaigns</CardDescription>
            </div>
            <Link href="/campaigns" className="inline-flex items-center gap-1 text-sm font-medium text-emerald-800 hover:text-emerald-900">
              View all <ArrowRight className="h-3.5 w-3.5" />
            </Link>
          </CardHeader>
          <CardContent className="px-0 py-0">
            <Table className="min-w-[700px]">
              <Thead>
                <Tr>
                  <Th>Campaign</Th>
                  <Th>Date</Th>
                  <Th className="text-right">Recipients</Th>
                  <Th className="text-right">Open rate</Th>
                  <Th>Status</Th>
                </Tr>
              </Thead>
              <Tbody>
                {recentCampaigns.map((campaign) => (
                  <Tr key={campaign.id}>
                    <Td>
                      <Link href={`/campaigns/${campaign.id}`} className="font-medium text-slate-900 hover:text-emerald-800">
                        {campaign.name}
                      </Link>
                      <p className="max-w-xs truncate text-xs text-slate-500">
                        {campaign.subject}
                      </p>
                    </Td>
                    <Td className="text-xs text-slate-500">
                      {formatDate(campaign.created_at)}
                    </Td>
                    <Td className="text-right tabular-nums">
                      {formatNumber(campaign.recipient_count)}
                    </Td>
                    <Td className="text-right tabular-nums">—</Td>
                    <Td>
                      <StatusBadge status={campaign.status} />
                    </Td>
                  </Tr>
                ))}
                {recentCampaigns.length === 0 && (
                  <Tr>
                    <Td colSpan={5} className="text-center py-8 text-slate-500">
                      No campaigns yet. Create your first campaign to get
                      started.
                    </Td>
                  </Tr>
                )}
              </Tbody>
            </Table>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
