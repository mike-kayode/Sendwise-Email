"use client";

import { useQuery } from "@tanstack/react-query";
import { Mail, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { apiFetch, type AnalyticsOverview } from "@/lib/auth";
import { formatNumber } from "@/lib/utils";

export default function BillingView() {
  const usageQuery = useQuery({
    queryKey: ["analytics", "overview"],
    queryFn: () => apiFetch<AnalyticsOverview>("/analytics/overview"),
  });

  if (usageQuery.isLoading) {
    return <PageHeader title="Usage" description="Loading organization usage..." />;
  }
  if (usageQuery.error || !usageQuery.data) {
    return (
      <PageHeader
        title="Usage"
        description={usageQuery.error?.message ?? "Could not load organization usage."}
      />
    );
  }

  const usage = usageQuery.data;
  const metrics = [
    {
      label: "Contacts",
      value: usage.contacts,
      detail: `${formatNumber(usage.subscribed)} subscribed`,
      icon: <Users className="h-5 w-5" />,
    },
    {
      label: "Emails submitted (30 days)",
      value: usage.emails_submitted,
      detail: "SMTP submissions recorded in the last 30 days",
      icon: <Send className="h-5 w-5" />,
    },
    {
      label: "Campaigns sent",
      value: usage.campaigns_sent,
      detail: "Campaigns currently sending or completed",
      icon: <Mail className="h-5 w-5" />,
    },
  ];

  return (
    <div>
      <PageHeader
        title="Usage"
        description="Live counters for your organization. Contact count is current; email submissions cover the last 30 days."
      />
      <div className="grid gap-4 md:grid-cols-3">
        {metrics.map((metric) => (
          <Card key={metric.label}>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{metric.label}</CardTitle>
              <span className="text-slate-400">{metric.icon}</span>
            </CardHeader>
            <CardContent>
              <p className="text-3xl font-semibold tabular-nums text-slate-900">{formatNumber(metric.value)}</p>
              <CardDescription className="mt-1">{metric.detail}</CardDescription>
            </CardContent>
          </Card>
        ))}
      </div>
      <p className="mt-5 rounded-lg border border-slate-200 bg-white p-4 text-sm text-slate-600">
        Usage reporting is available, but this workspace has no billing provider or configured plan limits.
        No payment method, invoices, or paid-plan management are represented here.
      </p>
    </div>
  );
}
