"use client";

import { useQuery } from "@tanstack/react-query";
import { CreditCard, Check } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiFetch, type AnalyticsOverview } from "@/lib/auth";
import { formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

export default function BillingView() {
  const { toast } = useToast();

  const analyticsQuery = useQuery({
    queryKey: ["analytics", "overview"],
    queryFn: async () => {
      const res = await apiFetch<AnalyticsOverview>("/analytics/overview");
      return res;
    },
  });

  const analytics = analyticsQuery.data;

  if (analyticsQuery.isLoading) {
    return (
      <div>
        <PageHeader title="Billing & usage" description="Loading your usage data..." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Billing & usage"
        description="Manage your plan, monitor sending limits, and download invoices."
        actions={
          <Button variant="secondary">
            <CreditCard className="h-4 w-4" /> Manage payment method
          </Button>
        }
      />

      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <CardTitle>Current plan</CardTitle>
              <CardDescription>Free tier - no payment required</CardDescription>
            </div>
            <Badge variant="indigo">Free</Badge>
          </CardHeader>
          <CardContent className="space-y-5">
            {analytics && (
              <>
                <div>
                  <div className="mb-1.5 flex items-center justify-between text-sm">
                    <span className="text-slate-600">Contacts</span>
                    <span className="tabular-nums text-slate-500">
                      {formatNumber(analytics.contacts)} <span className="text-slate-400">/ 50,000</span>
                    </span>
                  </div>
                  <div className="h-2 rounded-full bg-slate-100">
                    <div className="h-2 rounded-full bg-indigo-500" style={{ width: Math.min(100, (analytics.contacts / 50000) * 100) + "%" }} />
                  </div>
                </div>
                <div>
                  <div className="mb-1.5 flex items-center justify-between text-sm">
                    <span className="text-slate-600">Emails sent</span>
                    <span className="tabular-nums text-slate-500">
                      {formatNumber(analytics.emails_submitted)} <span className="text-slate-400">/ 250,000</span>
                    </span>
                  </div>
                  <div className="h-2 rounded-full bg-slate-100">
                    <div className="h-2 rounded-full bg-indigo-500" style={{ width: Math.min(100, (analytics.emails_submitted / 250000) * 100) + "%" }} />
                  </div>
                </div>
                <div>
                  <div className="mb-1.5 flex items-center justify-between text-sm">
                    <span className="text-slate-600">Campaigns sent</span>
                    <span className="tabular-nums text-slate-500">{analytics.campaigns_sent}</span>
                  </div>
                </div>
              </>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Payment method</CardTitle>
            <CardDescription>No payment required on free tier</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="rounded-lg bg-slate-50 p-3">
              <p className="text-xs text-slate-500">Billing email</p>
              <p className="font-medium text-slate-900">No billing configured</p>
            </div>
            <p className="text-xs text-slate-500">
              Sendwise is currently in free mode. Upgrade to a paid plan for higher limits
              and premium features.
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
