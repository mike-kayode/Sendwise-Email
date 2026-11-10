"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Monitor, Send, Smartphone, Calendar } from "lucide-react";
import { useState } from "react";
import { EmailPreview } from "@/components/email-preview";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { apiFetch, type Campaign, type CampaignValidation } from "@/lib/auth";
import { formatDateTime } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type ContentBlock = { id: string; type: string; content: string; url?: string; src?: string };
type ContactList = { id: string; name: string };

export default function CampaignPreviewPage() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [device, setDevice] = useState<"desktop" | "mobile">("desktop");
  const [scheduleAt, setScheduleAt] = useState("");
  const [sending, setSending] = useState(false);
  const [scheduling, setScheduling] = useState(false);

  const campaignQuery = useQuery({
    queryKey: ["campaign", id],
    queryFn: async () => apiFetch<Campaign>(`/campaigns/${id}`),
  });
  const validationQuery = useQuery({
    queryKey: ["campaign-validation", id],
    queryFn: async () => apiFetch<CampaignValidation>(`/campaigns/${id}/validate`, { method: "POST" }),
  });
  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: async () => {
      const res = await apiFetch<{ data: ContactList[] | null }>("/lists");
      return res.data ?? [];
    },
  });

  const campaign = campaignQuery.data;
  const validation = validationQuery.data;

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["campaign", id] });
    void queryClient.invalidateQueries({ queryKey: ["campaign-validation", id] });
  };

  const handleSendNow = async () => {
    if (!campaign) return;
    if (!window.confirm(`Send "${campaign.name}" to all of its recipients now? This cannot be undone.`)) return;
    setSending(true);
    try {
      const plan = await apiFetch<{ send_today: number; queued_later: number }>(`/campaigns/${campaign.id}/send`, { method: "POST" });
      toast({
        title: "Campaign sending",
        description: `${plan.send_today.toLocaleString()} will go out today; ${plan.queued_later.toLocaleString()} are queued for later warm-up days.`,
      });
      refresh();
    } catch (err: any) {
      toast({ title: "Could not send campaign", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setSending(false);
    }
  };

  const handleSchedule = async () => {
    if (!campaign || !scheduleAt) return;
    const when = new Date(scheduleAt);
    if (when.getTime() <= Date.now()) {
      toast({ title: "Invalid schedule time", description: "Pick a time in the future.", variant: "destructive" });
      return;
    }
    setScheduling(true);
    try {
      await apiFetch(`/campaigns/${campaign.id}/schedule`, {
        method: "POST",
        body: JSON.stringify({ scheduled_at: when.toISOString() }),
      });
      toast({ title: "Campaign scheduled", description: `It will send at ${when.toLocaleString()}.` });
      refresh();
    } catch (err: any) {
      toast({ title: "Could not schedule campaign", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setScheduling(false);
    }
  };

  if (campaignQuery.isLoading) {
    return <PageHeader eyebrow="Campaigns" title="Preview & test" description="Loading campaign..." />;
  }
  if (campaignQuery.error || !campaign) {
    return (
      <PageHeader
        eyebrow="Campaigns"
        title="Preview & test"
        description={campaignQuery.error?.message ?? "Campaign not found."}
      />
    );
  }

  const content = typeof campaign.content === "string" ? JSON.parse(campaign.content) : campaign.content ?? {};
  const blocks: ContentBlock[] = Array.isArray(content.blocks) ? content.blocks : [];
  const designHTML = typeof content.html === "string" ? content.html : "";
  const plainText = content.mode === "plain" && typeof content.text_content === "string"
    ? content.text_content
    : undefined;
  const audienceLabel = campaign.list_id
    ? (listsQuery.data ?? []).find((l) => l.id === campaign.list_id)?.name ?? "Selected list"
    : campaign.segment_id
      ? "Saved segment"
      : "All subscribed contacts";

  const isSendable = ["draft", "ready", "scheduled"].includes(campaign.status);
  const checks = validation?.checks ?? [];
  const errorCount = checks.filter((c) => c.status === "error").length;
  const warningCount = checks.filter((c) => c.status === "warning").length;

  return (
    <div>
      <Link href={`/campaigns/${campaign.id}/editor`} className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to editor
      </Link>

      <PageHeader
        eyebrow={campaign.name}
        title="Preview & test"
        description="Check how the email renders, run the pre-send checks, then schedule or send."
        actions={
          <Link
            href={`/campaigns/${campaign.id}`}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
          >
            Campaign details
          </Link>
        }
      />

      <div className="mb-5 flex flex-wrap items-center gap-3 text-sm text-slate-500">
        <StatusBadge status={campaign.status} />
        <span>To: {audienceLabel}</span>
        {campaign.scheduled_at ? <span>Scheduled {formatDateTime(campaign.scheduled_at)}</span> : null}
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex rounded-lg bg-white p-1 ring-1 ring-inset ring-slate-200">
              <button
                onClick={() => setDevice("desktop")}
                className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium ${
                  device === "desktop" ? "bg-slate-900 text-white" : "text-slate-600"
                }`}
              >
                <Monitor className="h-3.5 w-3.5" /> Desktop
              </button>
              <button
                onClick={() => setDevice("mobile")}
                className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium ${
                  device === "mobile" ? "bg-slate-900 text-white" : "text-slate-600"
                }`}
              >
                <Smartphone className="h-3.5 w-3.5" /> Mobile
              </button>
            </div>
            <span className="text-xs text-slate-500">{audienceLabel}</span>
          </div>
          <div className="rounded-xl bg-slate-100 p-4 sm:p-8">
            <div className={device === "mobile" ? "mx-auto max-w-sm" : "mx-auto max-w-2xl"}>
              {designHTML || blocks.length > 0 || plainText !== undefined ? (
                <EmailPreview
                  subject={campaign.subject || "(no subject)"}
                  preheader={campaign.preview_text || undefined}
                  blocks={blocks}
                  html={plainText === undefined ? designHTML || undefined : undefined}
                  textContent={plainText}
                />
              ) : (
                <div className="rounded-xl border border-dashed border-slate-300 bg-white p-10 text-center">
                  <p className="text-sm font-medium text-slate-700">This campaign has no content yet.</p>
                  <p className="mt-1 text-xs text-slate-500">Add headings, paragraphs, and buttons in the editor.</p>
                  <Link
                    href={`/campaigns/${campaign.id}/editor`}
                    className="mt-4 inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white hover:bg-indigo-500"
                  >
                    Open the editor
                  </Link>
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="space-y-5">
          <Card>
            <CardHeader className="flex items-start justify-between">
              <div>
                <CardTitle>Pre-send checklist</CardTitle>
                <CardDescription>Live checks against your campaign configuration.</CardDescription>
              </div>
              <button
                onClick={() => void validationQuery.refetch()}
                className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
              >
                Re-run
              </button>
            </CardHeader>
            <CardContent className="space-y-2.5 text-sm">
              {validationQuery.isLoading ? (
                <p className="text-slate-500">Running checks...</p>
              ) : validationQuery.error ? (
                <p className="text-slate-500">Could not run checks: {(validationQuery.error as Error).message}</p>
              ) : (
                <>
                  {checks.map((check) => (
                    <div key={check.name} className="flex items-start gap-2">
                      <span
                        className={
                          check.status === "pass"
                            ? "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-emerald-500 text-[9px] text-white"
                            : check.status === "warning"
                              ? "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-amber-400 text-[9px] text-white"
                              : "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-red-500 text-[9px] text-white"
                        }
                      >
                        {check.status === "pass" ? "✓" : check.status === "warning" ? "!" : "✕"}
                      </span>
                      <span className={check.status === "pass" ? "text-slate-600" : check.status === "warning" ? "text-amber-700" : "text-red-700"}>
                        {check.message}
                      </span>
                    </div>
                  ))}
                  <p className="border-t border-slate-100 pt-2.5 text-xs font-medium">
                    {validation?.valid ? (
                      <span className="text-emerald-600">All required checks passed.</span>
                    ) : (
                      <span className="text-red-600">{errorCount} issue(s) must be fixed before sending.</span>
                    )}
                    {warningCount > 0 ? <span className="text-amber-600"> {warningCount} warning(s).</span> : null}
                  </p>
                </>
              )}
              <Link href="/domains" className="block pt-1 text-xs font-medium text-indigo-600 hover:text-indigo-500">
                Review sending domains
              </Link>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Send</CardTitle>
              <CardDescription>
                {campaign.status === "completed"
                  ? "This campaign has already been sent."
                  : campaign.status === "sending" || campaign.status === "processing"
                    ? "Delivery is in progress."
                    : "Schedule it or send it to the audience now."}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {isSendable ? (
                <>
                  <div className="space-y-2">
                    <label htmlFor="schedule-at" className="block text-xs font-medium text-slate-600">
                      Schedule for later
                    </label>
                    <Input
                      id="schedule-at"
                      type="datetime-local"
                      value={scheduleAt}
                      onChange={(e) => setScheduleAt(e.target.value)}
                    />
                    <Button
                      variant="secondary"
                      className="w-full"
                      onClick={handleSchedule}
                      disabled={scheduling || !scheduleAt}
                    >
                      <Calendar className="h-4 w-4" /> {scheduling ? "Scheduling..." : "Schedule"}
                    </Button>
                  </div>
                  <Button
                    className="w-full"
                    onClick={handleSendNow}
                    disabled={sending || (validation !== undefined && !validation.valid)}
                  >
                    <Send className="h-4 w-4" /> {sending ? "Sending..." : "Send now"}
                  </Button>
                  {validation !== undefined && !validation.valid ? (
                    <p className="text-xs text-red-600">Fix the checklist errors above to enable sending.</p>
                  ) : null}
                </>
              ) : campaign.status === "sending" || campaign.status === "processing" || campaign.status === "scheduled" ? (
                <Link
                  href={`/campaigns/${campaign.id}/report`}
                  className="inline-flex h-9 w-full items-center justify-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white hover:bg-indigo-500"
                >
                  View live report
                </Link>
              ) : (
                <Link
                  href={`/campaigns/${campaign.id}/report`}
                  className="inline-flex h-9 w-full items-center justify-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
                >
                  View report
                </Link>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
