"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Clock, Mail, Plus, Search, Send, TrendingUp } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type Campaign, type AnalyticsOverview, type ListResponse } from "@/lib/auth";
import { formatDate, formatNumber } from "@/lib/utils";

const tabs = ["All", "Draft", "Scheduled", "Sending", "Completed"];

export default function CampaignsPage() {
  const [tab, setTab] = useState<string>("All");
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);

  const campaignsQuery = useQuery({
    queryKey: ["campaigns", tab, search, offset],
    queryFn: async () => {
      const params = new URLSearchParams();
      if (tab !== "All") params.set("status", tab.toLowerCase());
      if (search) params.set("search", search);
      params.set("limit", "20");
      params.set("offset", String(offset));
      return apiFetch<ListResponse<Campaign>>(`/campaigns?${params.toString()}`);
    },
  });

  const overview = useQuery({
    queryKey: ["analytics", "overview"],
    queryFn: async () => apiFetch<AnalyticsOverview>("/analytics/overview"),
  });

  const campaigns = campaignsQuery.data?.data ?? [];
  const total = campaignsQuery.data?.pagination.total ?? 0;

  if (campaignsQuery.isLoading || overview.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader title="Campaigns" description="Loading your campaigns..." />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <StatCard key={i} label="Loading" value="—" />
          ))}
        </div>
      </div>
    );
  }

  if (campaignsQuery.error || overview.error) {
    return (
      <div className="space-y-6">
        <PageHeader title="Campaigns" description="Unable to load campaigns." />
        <Card>
          <CardContent className="py-10 text-center">
            <p className="text-slate-500">
              {overview.error ? overview.error.message : campaignsQuery.error?.message}
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  const data = overview.data;
  if (!data) {
    return (
      <div className="space-y-6">
        <PageHeader title="Campaigns" description="No analytics data available." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Campaigns"
        description="Create, schedule, and measure one-off email campaigns."
        actions={
          <Link
            href="/campaigns/new"
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
          >
            <Plus className="h-4 w-4" /> New campaign
          </Link>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Campaigns sent" value={data.emails_submitted} change={8.1} icon={<Send className="h-4 w-4" />} />
        <StatCard
          label="Average open rate"
          value={data.open_rate.toFixed(1) + "%"}
          change={2.3}
          valueIsNumber={false}
          icon={<Mail className="h-4 w-4" />}
        />
        <StatCard
          label="Average click rate"
          value={data.click_rate.toFixed(1) + "%"}
          change={-0.4}
          valueIsNumber={false}
          icon={<TrendingUp className="h-4 w-4" />}
        />
        <StatCard label="Scheduled" value={campaigns.filter((c) => c.status === "scheduled").length} icon={<Clock className="h-4 w-4" />} changeLabel="in this view" />
      </div>

      <Card className="mt-5">
        <div className="flex flex-col gap-3 border-b border-slate-100 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap gap-1">
            {tabs.map((tabLabel) => {
              const active = tab === tabLabel;
              return (
                <button
                  key={tabLabel}
                  onClick={() => setTab(tabLabel)}
                  className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${active ? "bg-slate-900 text-white" : "text-slate-600 hover:bg-slate-100"}`}
                >
                  {tabLabel}
                </button>
              );
            })}
          </div>
          <div className="relative mt-2 sm:mt-0">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              placeholder="Search campaigns"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="h-9 w-full rounded-lg border-0 bg-slate-50 pl-9 pr-3 text-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:bg-white focus:ring-2 focus:ring-inset focus:ring-indigo-500 sm:w-64"
            />
          </div>
        </div>

        <Table>
          <Thead>
            <Tr>
              <Th>Campaign</Th>
              <Th>Status</Th>
              <Th>Audience</Th>
              <Th className="text-right">Recipients</Th>
              <Th className="text-right">Opens</Th>
              <Th className="text-right">Clicks</Th>
              <Th>Date</Th>
              <Th />
            </Tr>
          </Thead>
          <Tbody>
            {campaigns.length === 0 ? (
              <Tr>
                <Td colSpan={8} className="text-center py-8 text-slate-500">
                  No campaigns found. Create your first campaign.
                </Td>
              </Tr>
            ) : (
              campaigns.map((campaign) => (
                <Tr key={campaign.id}>
                  <Td>
                    <Link href={`/campaigns/${campaign.id}`} className="font-medium text-slate-900 hover:text-indigo-600">
                      {campaign.name}
                    </Link>
                    <p className="max-w-xs truncate text-xs text-slate-500">{campaign.subject}</p>
                  </Td>
                  <Td>
                    <StatusBadge status={campaign.status} />
                  </Td>
                  <Td className="text-xs text-slate-600">{campaign.list_id || campaign.segment_id || "—"}</Td>
                  <Td className="text-right tabular-nums">{formatNumber(campaign.recipient_count)}</Td>
                  <Td className="text-right tabular-nums">—</Td>
                  <Td className="text-right tabular-nums">—</Td>
                  <Td className="text-xs text-slate-500">{formatDate(campaign.created_at)}</Td>
                  <Td>
                    <Link href={`/campaigns/${campaign.id}`} className="text-xs font-medium text-indigo-600 hover:text-indigo-500">
                      Open
                    </Link>
                  </Td>
                </Tr>
              ))
            )}
          </Tbody>
        </Table>

        <div className="flex items-center justify-between border-t border-slate-100 px-4 py-3 text-xs text-slate-500">
          <span>Showing {Math.min(offset + campaigns.length, total)} of {total}</span>
          <div className="flex gap-1">
            <button
              className={`rounded-md px-2.5 py-1 ring-1 ring-inset ${offset === 0 ? "disabled:opacity-40" : "hover:bg-slate-50"}`}
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - 20))}
            >
              Previous
            </button>
            <button
              className={`rounded-md px-2.5 py-1 ring-1 ring-inset ${offset + 20 >= total ? "disabled:opacity-40" : "hover:bg-slate-50"}`}
              disabled={offset + 20 >= total}
              onClick={() => setOffset(offset + 20)}
            >
              Next
            </button>
          </div>
        </div>
      </Card>
    </div>
  );
}
