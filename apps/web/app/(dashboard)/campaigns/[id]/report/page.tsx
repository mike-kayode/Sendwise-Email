"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Download, Mail, MousePointerClick, Send, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { BarChart } from "@/components/charts";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, getAccessToken, type CampaignReport } from "@/lib/auth";
import { apiUrl } from "@/lib/api-url";
import { formatDateTime, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";

type RecipientRow = {
  recipient_id: string;
  name: string;
  email: string;
  delivery_status: string;
  accepted_at: string | null;
  delivered_at: string | null;
  open_count: number;
  first_opened_at: string | null;
  last_opened_at: string | null;
  click_count: number;
  last_clicked_at: string | null;
  last_activity_at: string | null;
  engagement_classification: string;
  journey_status: string;
  journey_reason?: string;
  is_delayed?: boolean;
  delayed_reason?: string;
};

type JourneyEvent = {
  stage: string;
  source?: string;
  source_event_id?: string;
  timestamp: string;
  remote_mx_host?: string;
  smtp_code?: number;
  enhanced_code?: string;
  reason?: string;
  attempt_number?: number;
  next_retry_at?: string;
  metadata?: Record<string, unknown>;
};

type RecipientActivity = {
  event_type: string;
  timestamp: string;
  ip_address: string;
  user_agent: string;
  device_type: string;
  browser: string;
  operating_system: string;
  country: string;
  region: string;
  city: string;
  destination_url: string;
  metadata: Record<string, unknown>;
  is_automated: boolean;
  automation_type: string;
};

const pageSize = 25;

export default function CampaignReportPage() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [opened, setOpened] = useState("");
  const [clicked, setClicked] = useState("");
  const [classification, setClassification] = useState("");
  const [stage, setStage] = useState("");
  const [sort, setSort] = useState("last_activity");
  const [offset, setOffset] = useState(0);
  const [pageInput, setPageInput] = useState("1");
  const [selectedRecipient, setSelectedRecipient] = useState<RecipientRow | null>(null);

  const reportQuery = useQuery({
    queryKey: ["campaign-report", id],
    queryFn: async () => apiFetch<CampaignReport>(`/analytics/campaigns/${id}`),
    refetchInterval: 30_000,
  });
  const recipientsQuery = useQuery({
    queryKey: ["campaign-recipients", id, search, status, opened, clicked, classification, stage, sort, offset],
    queryFn: () => {
      const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset), sort });
      if (search) params.set("search", search);
      if (status) params.set("status", status);
      if (opened) params.set("opened", opened);
      if (clicked) params.set("clicked", clicked);
      if (classification) params.set("classification", classification);
      if (stage) params.set("stage", stage);
      return apiFetch<{ data: RecipientRow[]; pagination: { total: number; limit: number; offset: number } }>(
        `/campaigns/${id}/recipients?${params}`,
      );
    },
    refetchInterval: 30_000,
  });
  const journeyQuery = useQuery({
    queryKey: ["recipient-journey", id, selectedRecipient?.recipient_id],
    queryFn: () => apiFetch<{ status: string; events: JourneyEvent[] }>(
      `/campaigns/${id}/recipients/${selectedRecipient!.recipient_id}/journey`,
    ),
    enabled: Boolean(selectedRecipient),
  });
  const activityQuery = useQuery({
    queryKey: ["recipient-activity", id, selectedRecipient?.recipient_id],
    queryFn: () => apiFetch<{ data: RecipientActivity[] }>(
      `/campaigns/${id}/recipients/${selectedRecipient!.recipient_id}/activity`,
    ),
    enabled: Boolean(selectedRecipient),
  });

  const totalPages = Math.max(1, Math.ceil((recipientsQuery.data?.pagination.total ?? 0) / pageSize));

  useEffect(() => {
    setPageInput(String(Math.floor(offset / pageSize) + 1));
  }, [offset]);

  const goToPage = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const page = Number(pageInput);
    if (Number.isInteger(page) && page >= 1 && page <= totalPages) {
      setOffset((page - 1) * pageSize);
    }
  };

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
  const journeyFunnel = report.journey?.funnel ?? {};

  const deferredCount = (journeyFunnel.deferred_by_sendwise ?? 0) + (journeyFunnel.deferred_by_recipient ?? 0);
  const funnel = [
    { label: "Queued", stage: "queued", value: journeyFunnel.queued ?? 0 },
    { label: "Submitted", stage: "submitted_to_mail_server", value: journeyFunnel.submitted_to_mail_server ?? 0 },
    { label: "Relaying", stage: "relaying", value: journeyFunnel.relaying ?? 0 },
    { label: "Deferred", stage: "deferred", value: deferredCount },
    { label: "Accepted by recipient server", stage: "accepted_by_recipient_server", value: journeyFunnel.accepted_by_recipient_server ?? 0 },
    { label: "Bounced", stage: "bounced", value: journeyFunnel.bounced ?? 0 },
    { label: "Expired", stage: "expired", value: journeyFunnel.expired ?? 0 },
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

  const handleRecipientExport = async () => {
    const token = getAccessToken();
    const headers = new Headers();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const response = await fetch(apiUrl(`/api/v1/campaigns/${id}/recipients/export`), {
      credentials: "include",
      cache: "no-store",
      headers,
    });
    if (!response.ok) {
      toast({ title: "Export failed", description: "Recipient tracking export could not be downloaded.", variant: "destructive" });
      return;
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `campaign-${id}-recipient-tracking.csv`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const handleJourneyEventsExport = async () => {
    const token = getAccessToken();
    const headers = new Headers();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const response = await fetch(apiUrl(`/api/v1/campaigns/${id}/journey/events/export`), {
      credentials: "include",
      cache: "no-store",
      headers,
    });
    if (!response.ok) {
      toast({ title: "Export failed", description: "Delivery journey events export could not be downloaded.", variant: "destructive" });
      return;
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `campaign-${id}-delivery-journey-events.csv`;
    anchor.click();
    URL.revokeObjectURL(url);
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
            <CardDescription>Accepted means the recipient’s mail server accepted the message; it does not mean inbox delivery.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {funnel.map((row, index) => (
                <button key={row.label} type="button" onClick={() => { setOffset(0); setStage(stage === row.stage ? "" : row.stage); }} className={`flex w-full items-center gap-3 rounded text-left text-sm ${stage === row.stage ? "bg-indigo-50" : ""}`}>
                <span className="w-36 shrink-0 text-slate-600">{row.label}</span>
                <div className="h-7 flex-1 overflow-hidden rounded bg-slate-100">
                  <div
                    className="flex h-full items-center justify-end rounded bg-indigo-500 px-2 text-[10px] font-medium text-white"
                    style={{ width: `${Math.max(row.percent, 6)}%`, opacity: 1 - index * 0.14 }}
                  >
                    {row.percent.toFixed(1)}%
                  </div>
                </div>
                <span className="w-16 text-right tabular-nums text-slate-900">{formatNumber(row.value)}</span>
              </button>
            ))}
            <p className="pt-2 text-xs text-amber-700">{formatNumber(report.journey?.stuck ?? 0)} messages are still awaiting an update from the mail server.</p>
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

      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Recipient activity</CardTitle>
          <CardDescription>Delivery evidence and engagement for each recipient</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="mb-4 grid gap-3 rounded-lg border border-slate-100 p-3 sm:grid-cols-2 xl:grid-cols-6">
            <input
              value={search}
              onChange={(event) => { setOffset(0); setSearch(event.target.value); }}
              placeholder="Search name or email"
              className="h-9 rounded-lg border border-slate-200 px-3 text-sm"
            />
            <select value={status} onChange={(event) => { setOffset(0); setStatus(event.target.value); }} className="h-9 rounded-lg border border-slate-200 px-3 text-sm">
              <option value="">All delivery statuses</option>
              {["queued", "submitted", "accepted", "delivered", "deferred", "bounced", "complained", "unsubscribed", "failed"].map((item) => <option key={item}>{item}</option>)}
            </select>
            <select value={opened} onChange={(event) => { setOffset(0); setOpened(event.target.value); }} className="h-9 rounded-lg border border-slate-200 px-3 text-sm">
              <option value="">Opened: any</option>
              <option value="yes">Opened</option>
              <option value="no">Not opened</option>
            </select>
            <select value={clicked} onChange={(event) => { setOffset(0); setClicked(event.target.value); }} className="h-9 rounded-lg border border-slate-200 px-3 text-sm">
              <option value="">Clicked: any</option>
              <option value="yes">Clicked</option>
              <option value="no">Not clicked</option>
            </select>
            <select value={classification} onChange={(event) => { setOffset(0); setClassification(event.target.value); }} className="h-9 rounded-lg border border-slate-200 px-3 text-sm">
              <option value="">All classifications</option>
              <option value="human_like">Human-like</option>
              <option value="suspected_automated">Suspected automated</option>
              <option value="privacy_proxy">Privacy proxy</option>
              <option value="unknown">Unknown</option>
            </select>
            <select value={stage} onChange={(event) => { setOffset(0); setStage(event.target.value); }} className="h-9 rounded-lg border border-slate-200 px-3 text-sm">
              <option value="">All journey stages</option>
              {[
                ["queued", "Queued"],
                ["processing", "Processing"],
                ["deferred_by_sendwise", "Waiting for Sendwise limit"],
                ["submitted_to_mail_server", "Submitted to mail server"],
                ["relaying", "Mail server relaying"],
                ["accepted_by_recipient_server", "Accepted by recipient server"],
                ["deferred_by_recipient", "Deferred by recipient server"],
                ["bounced", "Bounced"],
                ["expired", "Expired"],
                ["failed", "Failed"],
                ["complained", "Complaint"],
                ["unsubscribed", "Unsubscribed"],
                ["accepted_final_state_unknown", "Final outcome unknown"],
                ["opened", "Opened"],
                ["clicked", "Clicked"],
              ].map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
            <select value={sort} onChange={(event) => { setOffset(0); setSort(event.target.value); }} className="h-9 rounded-lg border border-slate-200 px-3 text-sm">
              <option value="last_activity">Sort: last activity</option>
              <option value="open_count">Sort: open count</option>
              <option value="click_count">Sort: click count</option>
              <option value="delivery_date">Sort: delivery date</option>
            </select>
          </div>
          <div className="mb-3 flex justify-end">
            <button type="button" onClick={() => void handleRecipientExport()} className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-slate-200 px-3 text-xs font-medium text-slate-700 hover:bg-slate-50">
              <Download className="h-3.5 w-3.5" /> Export recipient CSV
            </button>
          </div>
          {recipientsQuery.isLoading ? <p className="py-8 text-center text-sm text-slate-500">Loading recipient activity...</p> : null}
          {recipientsQuery.error ? <p role="alert" className="py-8 text-center text-sm text-rose-700">Recipient activity could not be loaded: {recipientsQuery.error.message}</p> : null}
          {!recipientsQuery.isLoading && !recipientsQuery.error && recipientsQuery.data ? (
            recipientsQuery.data.data.length === 0 ? (
              <p className="py-10 text-center text-sm text-slate-500">No recipients match these filters.</p>
            ) : (
              <>
                <div className="overflow-x-auto">
                  <Table>
                    <Thead>
                      <Tr>
                        <Th>Recipient</Th>
                        <Th>Delivery</Th>
                        <Th>Delivered at</Th>
                        <Th>Opens</Th>
                        <Th>Clicks</Th>
                        <Th>Last activity</Th>
                        <Th>Classification</Th>
                        <Th className="text-right">Activity</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      {recipientsQuery.data.data.map((recipient) => (
                        <Tr key={recipient.recipient_id}>
                          <Td>
                            <p className="font-medium text-slate-900">{recipient.name || recipient.email}</p>
                            {recipient.name ? <p className="mt-0.5 text-xs text-slate-500">{recipient.email}</p> : null}
                          </Td>
                          <Td>
                            <div className="flex flex-wrap items-center gap-2">
                              <span className="text-sm text-slate-700">{journeyStageLabel(recipient.journey_status)}</span>
                              {recipient.is_delayed ? (
                                <span className="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-800" title={recipient.delayed_reason || "No recent update from the mail server"}>
                                  Delayed
                                </span>
                              ) : null}
                            </div>
                            {recipient.is_delayed && recipient.delayed_reason ? (
                              <p className="mt-1 text-[11px] text-amber-700">{recipient.delayed_reason}</p>
                            ) : null}
                            <p className="mt-1 text-[11px] text-slate-400">Legacy status: {recipient.delivery_status}</p>
                          </Td>
                          <Td className="whitespace-nowrap text-xs">{recipient.delivered_at ? formatDateTime(recipient.delivered_at) : recipient.accepted_at ? `Accepted ${formatDateTime(recipient.accepted_at)}` : "Not confirmed"}</Td>
                          <Td>{formatNumber(recipient.open_count)}</Td>
                          <Td>{formatNumber(recipient.click_count)}</Td>
                          <Td className="whitespace-nowrap text-xs">{recipient.last_activity_at ? formatDateTime(recipient.last_activity_at) : "None"}</Td>
                          <Td className="capitalize text-xs">{recipient.engagement_classification.replaceAll("_", " ")}</Td>
                          <Td className="text-right">
                            <button type="button" onClick={() => setSelectedRecipient(recipient)} className="rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50">View journey</button>
                          </Td>
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </div>
                <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
                  <button type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - pageSize))} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Previous</button>
                  <span className="text-xs text-slate-500">{offset + 1}–{Math.min(offset + pageSize, recipientsQuery.data.pagination.total)} of {recipientsQuery.data.pagination.total}</span>
                  <form onSubmit={goToPage} className="flex items-center gap-2">
                    <label htmlFor="recipient-page" className="text-xs text-slate-500">Go to page</label>
                    <input
                      id="recipient-page"
                      type="number"
                      min={1}
                      max={totalPages}
                      step={1}
                      required
                      value={pageInput}
                      onChange={(event) => setPageInput(event.target.value)}
                      className="h-9 w-16 rounded-lg border border-slate-200 px-2 text-sm"
                    />
                    <button type="submit" disabled={recipientsQuery.isFetching} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Go</button>
                  </form>
                  <button type="button" disabled={offset + pageSize >= recipientsQuery.data.pagination.total} onClick={() => setOffset(offset + pageSize)} className="rounded-lg px-3 py-2 text-sm text-slate-700 ring-1 ring-slate-200 disabled:opacity-40">Next</button>
                </div>
              </>
            )
          ) : null}
        </CardContent>
      </Card>

      <Dialog open={Boolean(selectedRecipient)} onOpenChange={(open) => { if (!open) setSelectedRecipient(null); }}>
        <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
          <DialogTitle>{selectedRecipient?.name || selectedRecipient?.email}</DialogTitle>
          <DialogDescription>
            {selectedRecipient?.email} · {selectedRecipient?.delivery_status}
          </DialogDescription>
          {activityQuery.isLoading ? <p className="text-sm text-slate-500">Loading activity timeline...</p> : null}
          {activityQuery.error ? <p role="alert" className="text-sm text-rose-700">Activity could not be loaded: {activityQuery.error.message}</p> : null}
          {activityQuery.data ? (
            activityQuery.data.data.length === 0 ? (
              <p className="py-6 text-sm text-slate-500">No activity has been recorded for this recipient yet.</p>
            ) : (
              <ol className="mt-2 space-y-4 border-l border-slate-200 pl-4">
                {activityQuery.data.data.map((item, index) => (
                  <li key={`${item.event_type}-${item.timestamp}-${index}`} className="relative">
                    <span className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full bg-indigo-500 ring-4 ring-white" />
                    <div className="rounded-lg border border-slate-100 p-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <p className="text-sm font-semibold capitalize text-slate-900">{item.event_type}</p>
                        <time className="text-xs text-slate-500">{formatDateTime(item.timestamp)}</time>
                      </div>
                      {item.destination_url ? <p className="mt-2 break-all text-xs text-slate-600">Link: {item.destination_url}</p> : null}
                      {item.ip_address ? <p className="mt-1 text-xs text-slate-500">IP: {item.ip_address}</p> : null}
                      {item.user_agent ? <p className="mt-1 break-words text-xs text-slate-500">User agent: {item.user_agent}</p> : null}
                      {item.is_automated ? <p className="mt-1 text-xs font-medium text-amber-700">Automated or privacy-protected: {item.automation_type}</p> : null}
                      {Object.keys(item.metadata).length > 0 ? (
                        <details className="mt-2 text-xs text-slate-600">
                          <summary className="cursor-pointer font-medium">Event metadata</summary>
                          <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap rounded bg-slate-50 p-2">{JSON.stringify(item.metadata, null, 2)}</pre>
                        </details>
                      ) : null}
                    </div>
                  </li>
                ))}
              </ol>
            )
          ) : null}
          <section className="mt-6 border-t border-slate-200 pt-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 className="text-sm font-semibold text-slate-900">Delivery journey</h3>
                <p className="mt-1 text-xs text-slate-500">Full UTC timestamps are shown; hover a timestamp for local time. Acceptance means the recipient’s mail server accepted the message, not that it reached the inbox.</p>
              </div>
              <button type="button" onClick={() => void handleJourneyEventsExport()} className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-slate-200 px-3 text-xs font-medium text-slate-700 hover:bg-slate-50">
                <Download className="h-3.5 w-3.5" /> Export events CSV
              </button>
            </div>
            {journeyQuery.isLoading ? <p className="mt-3 text-sm text-slate-500">Loading delivery updates...</p> : null}
            {journeyQuery.error ? <p role="alert" className="mt-3 text-sm text-rose-700">Delivery updates could not be loaded: {journeyQuery.error.message}</p> : null}
            {journeyQuery.data?.events.length ? (
              <ol className="mt-4 space-y-3 border-l border-slate-200 pl-4">
                {journeyQuery.data.events.map((item, index) => (
                  <li key={`${item.stage}-${item.timestamp}-${index}`} className="relative">
                    <span className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full bg-indigo-500 ring-4 ring-white" />
                    <div className="rounded-lg border border-slate-100 p-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <p className="text-sm font-semibold text-slate-900">{journeyStageLabel(item.stage)}</p>
                        <time className="text-xs text-slate-500" title={formatLocalTimestamp(item.timestamp)}>{formatUtcTimestamp(item.timestamp)}</time>
                      </div>
                      <dl className="mt-2 grid gap-x-4 gap-y-1 text-xs text-slate-600 sm:grid-cols-2">
                        {item.source ? <div><dt className="inline font-medium text-slate-500">Source: </dt><dd className="inline">{formatJourneySource(item.source)}</dd></div> : null}
                        {typeof item.attempt_number === "number" ? <div><dt className="inline font-medium text-slate-500">Attempt: </dt><dd className="inline">{item.attempt_number}</dd></div> : null}
                        {item.remote_mx_host ? <div><dt className="inline font-medium text-slate-500">Remote MX: </dt><dd className="inline break-all">{item.remote_mx_host}</dd></div> : null}
                        {typeof item.smtp_code === "number" ? <div><dt className="inline font-medium text-slate-500">SMTP code: </dt><dd className="inline">{item.smtp_code}{item.enhanced_code ? ` (${item.enhanced_code})` : ""}</dd></div> : null}
                        {item.next_retry_at ? <div className="sm:col-span-2"><dt className="inline font-medium text-slate-500">Next retry: </dt><dd className="inline"><time title={formatLocalTimestamp(item.next_retry_at)}>{formatUtcTimestamp(item.next_retry_at)}</time></dd></div> : null}
                      </dl>
                      {item.reason ? <p className="mt-2 break-words text-xs text-slate-600"><span className="font-medium text-slate-500">Reason: </span>{item.reason}</p> : null}
                    </div>
                  </li>
                ))}
              </ol>
            ) : journeyQuery.data ? <p className="mt-3 text-sm text-slate-500">No delivery updates have been recorded yet.</p> : null}
          </section>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function formatUtcTimestamp(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toISOString().replace("T", " ").replace(/\.\d{3}Z$/, " UTC");
}

function formatLocalTimestamp(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function formatJourneySource(source: string) {
  const labels: Record<string, string> = {
    sendwise: "Sendwise",
    stalwart: "Stalwart",
    stalwart_webhook: "Stalwart webhook",
    reconciliation: "Reconciliation",
    dsn: "DSN",
    backfill: "Backfill",
    tracking: "Tracking",
  };
  return labels[source] ?? source;
}

function journeyStageLabel(stage: string) {
  const labels: Record<string, string> = {
    queued: "Queued",
    picked_up: "Picked up by worker",
    processing: "Processing",
    smtp_attempt: "SMTP delivery attempt",
    deferred_by_sendwise: "Waiting for Sendwise sending limit",
    submitted_to_mail_server: "Submitted to the mail server",
    relaying: "The mail server is attempting delivery",
    accepted_by_recipient_server: "Accepted by the recipient's mail server",
    deferred_by_recipient: "Temporarily deferred by the recipient's mail server",
    bounced: "Bounced by the recipient's mail server",
    expired: "Expired in the mail server queue",
    failed: "Delivery failed",
    complained: "Marked as spam",
    unsubscribed: "Unsubscribed",
    accepted_final_state_unknown: "Final mail-server outcome is unknown",
    opened: "Opened",
    clicked: "Link clicked",
  };
  return labels[stage] ?? "Delivery status updated";
}
