import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import type { Metadata } from "next";
import { Plus, Workflow, Zap, ArrowRight, Play } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiFetch, type Campaign } from "@/lib/auth";
import { formatDate, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

export const metadata: Metadata = { title: "Automations" };

export default function AutomationsPage() {
  const { toast } = useToast();
  const automationsQuery = useQuery({
    queryKey: ["campaigns", "automations"],
    queryFn: async () => {
      const res = await apiFetch<{ data: Campaign[] }>("/campaigns?status=all");
      return res.data ?? [];
    },
  });

  const automations = automationsQuery.data ?? [];
  const activeCount = automations.filter((a) => a.status === "sending" || a.status === "processing").length;
  const totalContacts = automations.reduce((sum, a) => sum + (a.recipient_count ?? 0), 0);

  if (automationsQuery.isLoading) {
    return (
      <div>
        <PageHeader eyebrow="Marketing" title="Automations" description="Loading your automation flows..." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Automations"
        description="Send the right message automatically when contacts take an action."
        actions={
          <Link href="/campaigns/new" className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
            <Plus className="h-4 w-4" /> New automation
          </Link>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Active automations" value={activeCount} icon={<Zap className="h-4 w-4" />} changeLabel="running" />
        <StatCard label="Contacts in flows" value={totalContacts} icon={<Play className="h-4 w-4" />} />
        <StatCard label="Total automations" value={automations.length} icon={<Workflow className="h-4 w-4" />} />
      </div>

      {automations.length === 0 ? (
        <Card className="mt-5 border-dashed">
          <CardContent className="p-8 text-center">
            <Zap className="h-10 w-10 mx-auto text-slate-400 mb-3" />
            <h3 className="text-sm font-semibold text-slate-900 mb-1">No automations yet</h3>
            <p className="text-xs text-slate-500 max-w-xs mx-auto mb-4">
              Create your first automation by building a campaign with triggers and delays.
            </p>
            <Link href="/campaigns/new" className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
              <Plus className="h-4 w-4" /> Create automation
            </Link>
          </CardContent>
        </Card>
      ) : (
        <div className="mt-5 grid gap-4 md:grid-cols-2">
          {automations.map((automation) => (
            <Card key={automation.id}>
              <CardHeader className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-2">
                    <CardTitle>{automation.name}</CardTitle>
                    <StatusBadge status={automation.status} />
                  </div>
                  <CardDescription>{automation.subject}</CardDescription>
                </div>
                <Link
                  href={`/campaigns/${automation.id}/editor`}
                  className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                  aria-label={`Open ${automation.name}`}
                >
                  <ArrowRight className="h-4 w-4" />
                </Link>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex items-center justify-between text-sm">
                  <span className="text-slate-500">Contacts in flow</span>
                  <span className="font-medium tabular-nums text-slate-900">{formatNumber(automation.recipient_count ?? 0)}</span>
                </div>
                <div className="flex items-center justify-between text-sm">
                  <span className="text-slate-500">Track opens</span>
                  <span className="font-medium text-slate-900">{automation.track_opens ? "Yes" : "No"}</span>
                </div>
                <div className="flex items-center justify-between border-t border-slate-100 pt-3 text-xs text-slate-500">
                  <span>Updated {formatDate(automation.updated_at)}</span>
                  <Link href={`/campaigns/${automation.id}/editor`} className="font-medium text-indigo-600 hover:text-indigo-500">
                    Open flow
                  </Link>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
