"use client";

import { useCallback, useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle2, Copy, Globe, Plus, RefreshCw, ShieldCheck, Trash2, XCircle, Send } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge, Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type DomainDNSRecord, type SendingDomain } from "@/lib/auth";
import { formatDateTime } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

const recordStatusText = (status?: string) =>
  status === "wrong_value" ? "wrong value" : status ? status.replaceAll("_", " ") : "unknown";

function dnsNameForDisplay(name: string, domain: string, showFullName: boolean): string {
  if (showFullName) return name;
  if (name.toLowerCase() === domain.toLowerCase()) return "@";
  const suffix = `.${domain.toLowerCase()}`;
  return name.toLowerCase().endsWith(suffix) ? name.slice(0, -suffix.length) : name;
}

type DomainQuotaSnapshot = {
  stream: string;
  daily_limit: number | null;
  monthly_limit: number | null;
  per_second_limit: number;
  unlimited: boolean;
  daily_used: number;
  monthly_used: number;
  per_second_used: number;
  daily_reset_at: string;
  monthly_reset_at: string;
  per_second_reset_at: string;
  daily_usage_percent: number;
  monthly_usage_percent: number;
};

type DomainQuotaData = {
  domain_id: string;
  streams: Record<"marketing" | "transactional", DomainQuotaSnapshot>;
  warmup: {
    profile: string;
    successful_days: number;
    curve_cap: number;
    effective_cap_today: number;
    override_active: boolean;
    warmup_complete: boolean;
  };
};

function DomainQuotaPanel({ domainId, domainName }: { domainId: string; domainName: string }) {
  const [requestOpen, setRequestOpen] = useState(false);
  const [stream, setStream] = useState<"marketing" | "transactional">("marketing");
  const [dailyLimit, setDailyLimit] = useState("");
  const [monthlyLimit, setMonthlyLimit] = useState("");
  const [perSecondLimit, setPerSecondLimit] = useState("");
  const [reason, setReason] = useState("");
  const [requestError, setRequestError] = useState<string | null>(null);
  const [requestBusy, setRequestBusy] = useState(false);
  const { toast } = useToast();
  const quotaQuery = useQuery({
    queryKey: ["domain-quotas", domainId],
    queryFn: () => apiFetch<{ data: DomainQuotaData }>(`/domains/${encodeURIComponent(domainId)}/quotas`),
    retry: false,
  });

  const submitRequest = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if ((!dailyLimit && !monthlyLimit && !perSecondLimit) || !reason.trim() || requestBusy) return;
    setRequestBusy(true);
    setRequestError(null);
    try {
      await apiFetch(`/domains/${encodeURIComponent(domainId)}/quota-requests`, {
        method: "POST",
        body: JSON.stringify({
          stream,
          daily_limit: dailyLimit ? Number(dailyLimit) : null,
          monthly_limit: monthlyLimit ? Number(monthlyLimit) : null,
          per_second_limit: perSecondLimit ? Number(perSecondLimit) : null,
          reason: reason.trim(),
        }),
      });
      setDailyLimit("");
      setMonthlyLimit("");
      setPerSecondLimit("");
      setReason("");
      setRequestOpen(false);
      toast({ title: "Quota request recorded", description: `Your request for ${domainName} is visible to the Sendwise admin team.` });
    } catch (error) {
      setRequestError(error instanceof Error ? error.message : "The quota request could not be recorded.");
    } finally {
      setRequestBusy(false);
    }
  };

  if (quotaQuery.isLoading) {
    return <p className="mt-4 border-t border-slate-100 pt-3 text-xs text-slate-500">Loading domain quotas...</p>;
  }
  if (quotaQuery.error || !quotaQuery.data) {
    return (
      <div role="alert" className="mt-4 border-t border-slate-100 pt-3 text-xs text-rose-700">
        Domain quota usage could not be loaded: {quotaQuery.error instanceof Error ? quotaQuery.error.message : "No quota data returned."}
      </div>
    );
  }

  const formatLimit = (value: number | null, unlimited: boolean) =>
    unlimited || value === null ? "Unlimited" : value.toLocaleString();
  const streams = quotaQuery.data.data.streams;

  return (
    <div className="mt-4 border-t border-slate-100 pt-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-semibold text-slate-800">Sending quotas</p>
        <button
          type="button"
          className="text-xs font-medium text-indigo-700 hover:underline"
          onClick={() => setRequestOpen((open) => !open)}
        >
          {requestOpen ? "Close request" : "Request increase"}
        </button>
      </div>
      <section className="mt-3 rounded-lg border border-slate-200 bg-white p-3" aria-label="Warm-up capacity">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-xs font-semibold text-slate-800">Warm-up capacity today</p>
          {quotaQuery.data.data.warmup.warmup_complete ? (
            <span className="rounded-full bg-emerald-100 px-2 py-1 text-[10px] font-semibold text-emerald-900">Warm-up complete</span>
          ) : null}
          {quotaQuery.data.data.warmup.override_active ? (
            <span className="rounded-full bg-amber-100 px-2 py-1 text-[10px] font-semibold text-amber-900">Admin override active</span>
          ) : null}
        </div>
        <p className="mt-1 text-xs text-slate-600">
          {quotaQuery.data.data.warmup.effective_cap_today.toLocaleString()} messages · {quotaQuery.data.data.warmup.profile} profile · {quotaQuery.data.data.warmup.successful_days} successful days
        </p>
      </section>
      <div className="mt-3 grid gap-3">
        {(["marketing", "transactional"] as const).map((key) => {
          const item = streams[key];
          return (
            <section key={key} className="rounded-lg bg-slate-50 p-3" aria-label={`${key} quota usage`}>
              <h4 className="text-xs font-semibold capitalize text-slate-800">{key}</h4>
              <div className="mt-2 grid grid-cols-1 gap-1 text-[11px] text-slate-600 sm:grid-cols-3">
                <span>Today: {item.daily_used.toLocaleString()} / {formatLimit(item.daily_limit, item.unlimited)}</span>
                <span>This month: {item.monthly_used.toLocaleString()} / {formatLimit(item.monthly_limit, item.unlimited)}</span>
                <span>Rate: {item.per_second_used} / {item.per_second_limit.toLocaleString()} per second</span>
              </div>
              <div className="mt-2 flex flex-col gap-1 text-[10px] text-slate-500 sm:flex-row sm:justify-between">
                <span>Daily reset {formatDateTime(item.daily_reset_at)}</span>
                <span>Monthly reset {formatDateTime(item.monthly_reset_at)}</span>
              </div>
            </section>
          );
        })}
      </div>
      {requestOpen ? (
        <form className="mt-3 space-y-3 rounded-lg border border-slate-200 p-3" onSubmit={(event) => void submitRequest(event)}>
          <label className="block text-xs font-medium text-slate-700">
            Stream
            <select className="mt-1 h-9 w-full rounded-md border border-slate-300 bg-white px-2 text-sm" value={stream} onChange={(event) => setStream(event.target.value as typeof stream)}>
              <option value="marketing">Marketing</option>
              <option value="transactional">Transactional</option>
            </select>
          </label>
          <div className="grid gap-2 sm:grid-cols-3">
            <label className="text-xs font-medium text-slate-700">
              Daily limit
              <Input type="number" min="1" value={dailyLimit} onChange={(event) => setDailyLimit(event.target.value)} />
            </label>
            <label className="text-xs font-medium text-slate-700">
              Monthly limit
              <Input type="number" min="1" value={monthlyLimit} onChange={(event) => setMonthlyLimit(event.target.value)} />
            </label>
            <label className="text-xs font-medium text-slate-700">
              Per-second limit
              <Input type="number" min="1" value={perSecondLimit} onChange={(event) => setPerSecondLimit(event.target.value)} />
            </label>
          </div>
          <label className="block text-xs font-medium text-slate-700">
            Reason
            <textarea className="mt-1 min-h-20 w-full rounded-md border border-slate-300 p-2 text-sm" value={reason} onChange={(event) => setReason(event.target.value)} required maxLength={1000} />
          </label>
          {requestError ? <p role="alert" className="text-xs text-rose-700">{requestError}</p> : null}
          <button type="submit" disabled={requestBusy || !reason.trim() || (!dailyLimit && !monthlyLimit && !perSecondLimit)} className="inline-flex h-9 items-center rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50">
            {requestBusy ? "Recording..." : "Submit request"}
          </button>
          <p className="text-[11px] text-slate-500">Requests are recorded for admin review; no email notification is sent.</p>
        </form>
      ) : null}
    </div>
  );
}

export default function DomainsPage() {
  const [newDomain, setNewDomain] = useState("");
  const [selectedDomainId, setSelectedDomainId] = useState<string | null>(null);
  const [verifyingAll, setVerifyingAll] = useState(false);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [updatingTransactionalId, setUpdatingTransactionalId] = useState<string | null>(null);
  const [showFullNames, setShowFullNames] = useState(false);
  const dnsSectionRef = useRef<HTMLDivElement>(null);
  const pollingStartedAt = useRef(new Map<string, number>());
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const domainsQuery = useQuery({
    queryKey: ["domains"],
    queryFn: async () => {
      const res = await apiFetch<{ data: SendingDomain[] | null }>("/domains");
      return res.data ?? [];
    },
  });

  const domains = domainsQuery.data ?? [];
  const pendingDnsDomains = domains.filter(
    (domain) => domain.status !== "verified" || domain.owner_conflict,
  );
  const verifiedDomains = domains.filter(
    (domain) => domain.status === "verified" && !domain.owner_conflict,
  );
  const selectedDomain = domains.find((domain) => domain.id === selectedDomainId) ?? null;
  const domainsForTable = selectedDomain ? [selectedDomain] : pendingDnsDomains;
  const domainIDs = domainsForTable.map((domain) => domain.id);
  const dnsRecordsQuery = useQuery({
    queryKey: ["domain-dns-records", domainIDs],
    enabled: domainIDs.length > 0,
    queryFn: async () => {
      const entries = await Promise.all(
        domainsForTable.map(async (domain) => {
          const result = await apiFetch<{ dns_records: DomainDNSRecord[] }>(
            `/domains/${domain.id}/dns-records`,
          );
          return [domain.id, result.dns_records] as const;
        }),
      );
      return Object.fromEntries(entries);
    },
  });
  const unverified = domains.filter((d) => d.status !== "verified" && !d.owner_conflict);
  const needsAttention = domains.filter((d) => d.status !== "verified" || d.owner_conflict);
  const verifiedCount = verifiedDomains.length;

  const handleAddDomain = async () => {
    if (!newDomain.trim()) return;
    try {
      await apiFetch("/domains", {
        method: "POST",
        body: JSON.stringify({ domain: newDomain }),
      });
      setNewDomain("");
      await queryClient.invalidateQueries({ queryKey: ["domains"] });
      toast({ title: "Domain added", description: "Add the DNS records below, then verify." });
    } catch (err: any) {
      toast({ title: "Failed to add domain", description: err?.message ?? "Unknown error", variant: "destructive" });
    }
  };

  const handleDeleteDomain = async (domain: SendingDomain) => {
    if (deletingId) return;
    const confirmed = window.confirm(
      `Delete ${domain.domain} and its Sendwise sender identities? DNS records at your DNS provider will not be removed.`,
    );
    if (!confirmed) return;

    setDeletingId(domain.id);
    try {
      await apiFetch<void>(`/domains/${domain.id}`, { method: "DELETE" });
      if (selectedDomainId === domain.id) setSelectedDomainId(null);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["domains"] }),
        queryClient.invalidateQueries({ queryKey: ["domain-dns-records"] }),
      ]);
      toast({ title: "Domain deleted", description: `${domain.domain} and its Sendwise sender identities were deleted.` });
    } catch (error) {
      toast({
        title: "Could not delete domain",
        description: error instanceof Error ? error.message : "The sending domain could not be deleted.",
        variant: "destructive",
      });
    } finally {
      setDeletingId(null);
    }
  };

  const handleTransactionalToggle = async (domain: SendingDomain) => {
    if (updatingTransactionalId) return;
    setUpdatingTransactionalId(domain.id);
    try {
      await apiFetch<SendingDomain>(`/domains/${domain.id}/transactional`, {
        method: "PUT",
        body: JSON.stringify({ enabled: !domain.transactional_enabled }),
      });
      await queryClient.invalidateQueries({ queryKey: ["domains"] });
      toast({
        title: domain.transactional_enabled ? "Transactional sending disabled" : "Transactional sending enabled",
        description: domain.transactional_enabled
          ? `${domain.domain} can no longer be used for transactional messages.`
          : `${domain.domain} is available for transactional messages.`,
      });
    } catch (error) {
      toast({
        title: "Could not update transactional domain access",
        description: error instanceof Error ? error.message : "The domain setting could not be updated.",
        variant: "destructive",
      });
    } finally {
      setUpdatingTransactionalId(null);
    }
  };

  const handleVerify = useCallback(async (id: string, domainName?: string, silent = false) => {
    setVerifyingId(id);
    try {
      const result = await apiFetch<{
        domain: SendingDomain;
        dns_records: DomainDNSRecord[];
      }>(`/domains/${id}/verify`, { method: "POST" });
      await queryClient.invalidateQueries({ queryKey: ["domains"] });
      await queryClient.invalidateQueries({ queryKey: ["domain-dns-records"] });
      if (result.domain.status === "verified") setSelectedDomainId(null);
      if (!silent) {
        toast({
          title: result.domain.status === "verified" ? `${domainName ?? "Domain"} is verified` : "DNS check complete",
          description:
            result.domain.status === "verified"
              ? "Ownership, SPF, and DKIM passed. DMARC and MX are advisory."
              : "Review the per-resolver results below for missing or incorrect records.",
          variant: result.domain.status === "verified" ? undefined : "destructive",
        });
      }
    } catch (err: any) {
      if (!silent) {
        toast({ title: "DNS check failed", description: err?.message ?? "Unknown error", variant: "destructive" });
      }
    } finally {
      setVerifyingId(null);
    }
  }, [queryClient, toast]);

  useEffect(() => {
    const now = Date.now();
    const pendingDomains = domains.filter((domain) => domain.status !== "verified" && !domain.owner_conflict);
    const timers = pendingDomains.map((domain) => {
      const startedAt = pollingStartedAt.current.get(domain.id) ?? now;
      pollingStartedAt.current.set(domain.id, startedAt);
      const delay = now - startedAt < 10 * 60 * 1000 ? 15_000 : 60_000;
      return window.setTimeout(() => {
        if (verifyingId !== domain.id) void handleVerify(domain.id, domain.domain, true);
      }, delay);
    });
    const active = new Set(pendingDomains.map((domain) => domain.id));
    for (const id of pollingStartedAt.current.keys()) {
      if (!active.has(id)) pollingStartedAt.current.delete(id);
    }
    return () => timers.forEach(window.clearTimeout);
  }, [domains, handleVerify, verifyingId]);

  const handleVerifyAll = async () => {
    if (unverified.length === 0) return;
    setVerifyingAll(true);
    let passed = 0;
    let failed = 0;
    for (const domain of unverified) {
      try {
        const result = await apiFetch<{ domain: SendingDomain }>(`/domains/${domain.id}/verify`, { method: "POST" });
        if (result.domain?.status === "verified") passed += 1;
      } catch {
        failed += 1;
      }
    }
    await queryClient.invalidateQueries({ queryKey: ["domains"] });
    await queryClient.invalidateQueries({ queryKey: ["domain-dns-records"] });
    setSelectedDomainId(null);
    setVerifyingAll(false);
    toast({
      title: "Verification run finished",
      description: `${passed} of ${unverified.length} domain(s) verified; ${failed} check(s) failed. See each domain for details.`,
      variant: passed === unverified.length ? undefined : "destructive",
    });
  };

  const handleReviewRecords = () => {
    const target = pendingDnsDomains[0];
    if (target) setSelectedDomainId(target.id);
    dnsSectionRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const handleViewRecords = (domainId: string) => {
    setSelectedDomainId(domainId);
    dnsSectionRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const copyText = async (text: string, label: string) => {
    let copied = false;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        copied = true;
      }
    } catch {
      // Permission denials and insecure origins fall through to the fallback.
    }

    if (!copied) {
      // navigator.clipboard is undefined on insecure (non-HTTPS) origins, so
      // copy through a hidden textarea and execCommand instead.
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.setAttribute("readonly", "");
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      try {
        document.body.appendChild(textarea);
        textarea.select();
        // iOS Safari does not select from .select() alone.
        textarea.setSelectionRange(0, textarea.value.length);
        copied = document.execCommand("copy");
      } catch {
        copied = false;
      } finally {
        textarea.remove();
      }
    }

    if (!copied) {
      toast({ title: "Copy failed", description: "Select the value and copy it manually.", variant: "destructive" });
      return;
    }
    toast({ title: "Copied", description: `${label} copied. Paste it at your DNS provider.` });
  };

  if (domainsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Deliverability"
          title="Sending domains"
          description="Loading your sending domains..."
        />
      </div>
    );
  }

  if (domainsQuery.error) {
    return (
      <PageHeader
        eyebrow="Deliverability"
        title="Sending domains"
        description={`Unable to load domains: ${domainsQuery.error.message}`}
      />
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Deliverability"
        title="Sending domains"
        description="Authenticate your domains with SPF, DKIM, and DMARC so inbox providers trust your mail."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50 disabled:opacity-50"
            onClick={handleVerifyAll}
            disabled={verifyingAll || unverified.length === 0}
          >
            <RefreshCw className={`h-4 w-4 ${verifyingAll ? "animate-spin" : ""}`} />
            {verifyingAll
              ? "Verifying..."
              : unverified.length > 0
                ? `Verify all (${unverified.length})`
                : "All verified"}
          </button>
        }
      />

      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Add a domain</CardTitle>
          <CardDescription>
            Use a subdomain for sending to protect your root domain. Add the generated DNS records before verifying it.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-col gap-3 sm:flex-row">
            <Input
              placeholder="mail.yourdomain.com"
              value={newDomain}
              onChange={(event) => setNewDomain(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void handleAddDomain();
              }}
              aria-label="Sending domain"
            />
            <button
              onClick={() => void handleAddDomain()}
              className="inline-flex h-10 shrink-0 items-center justify-center gap-1.5 rounded-lg bg-indigo-600 px-5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
              disabled={!newDomain.trim()}
            >
              <Plus className="h-4 w-4" /> Add domain
            </button>
          </div>
          <p className="text-xs text-slate-500">
            Sending also requires SPF to authorize the Stalwart sending host. If this domain&apos;s MX does not point to Stalwart, configure the platform SPF include before verifying.
          </p>
        </CardContent>
      </Card>

      {domains.length === 0 ? null : needsAttention.length === 0 ? (
        <div className="mb-5 flex items-center gap-2 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-900">
          <Send className="h-4 w-4 shrink-0" />
          <span>
            <span className="font-medium">All {domains.length} domain(s) are ready to send.</span>{" "}
            Ownership, SPF, and DKIM are passing for every domain. DMARC and MX remain advisory.
          </span>
        </div>
      ) : (
        <div className="mb-5 flex flex-col gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-2 text-sm text-amber-900">
            <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" />
            <span>
              <span className="font-medium">
                {needsAttention.length} of {domains.length} domain(s) need attention.
              </span>{" "}
              Unverified domains hurt deliverability and may block sending.
            </span>
          </div>
          <button
            onClick={handleReviewRecords}
            className="shrink-0 text-sm font-medium text-amber-900 underline-offset-2 hover:underline"
          >
            Review records →
          </button>
        </div>
      )}

      <div className="mb-5 grid gap-4 md:grid-cols-3">
        {domains.length === 0 ? (
          <div className="col-span-3 flex flex-col items-center justify-center py-12 text-center">
            <Globe className="mb-3 h-8 w-8 text-slate-300" />
            <p className="text-sm text-slate-500">No sending domains yet.</p>
            <p className="mt-1 text-xs text-slate-400">Add one to get your DNS records and start sending.</p>
          </div>
        ) : (
          domains.map((domain) => {
            const isReady = domain.status === "verified" && !domain.owner_conflict;
            return (
              <Card
                key={domain.id}
              >
                <CardContent className="p-5">
                  <div className="flex items-start justify-between gap-2">
                    <span
                      className={`flex h-9 w-9 items-center justify-center rounded-lg ${
                        isReady ? "bg-emerald-50 text-emerald-600" : "bg-indigo-50 text-indigo-600"
                      }`}
                    >
                      <Globe className="h-4 w-4" />
                    </span>
                    <div className="flex items-center gap-2">
                      {domain.owner_conflict ? (
                        <Badge variant="danger">Ownership conflict</Badge>
                      ) : isReady ? (
                        <Badge variant="success">
                          <CheckCircle2 className="mr-1 h-3 w-3" /> Ready to send
                        </Badge>
                      ) : (
                        <Badge variant="warning">
                          <XCircle className="mr-1 h-3 w-3" /> Verification needed
                        </Badge>
                      )}
                      <button
                        type="button"
                        aria-label={`Delete ${domain.domain}`}
                        title={`Delete ${domain.domain}`}
                        disabled={deletingId !== null || verifyingId === domain.id}
                        onClick={(event) => {
                          event.stopPropagation();
                          void handleDeleteDomain(domain);
                        }}
                        className="rounded-md p-1.5 text-slate-400 hover:bg-rose-50 hover:text-rose-700 disabled:cursor-not-allowed disabled:opacity-40"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  </div>
                  <p className="mt-3 text-sm font-semibold text-slate-900">{domain.domain}</p>
                  <p className="text-xs text-slate-500">
                    {domain.last_checked_at
                      ? `Checked ${formatDateTime(domain.last_checked_at)}`
                      : "Never checked"}{" "}
                    · {domain.owner_conflict ? (
                      <Badge variant="danger">Domain already owned by another workspace</Badge>
                    ) : (
                      <StatusBadge status={domain.status} />
                    )}
                  </p>

                  <div className="mt-4 grid grid-cols-3 gap-2 border-t border-slate-100 pt-3 text-xs">
                    {[
                      { label: "SPF", status: domain.spf_status },
                      { label: "DKIM", status: domain.dkim_status },
                      { label: "Ownership", status: domain.verification_status },
                      { label: "DMARC", status: domain.dmarc_status },
                    ].map((record) => {
                      const ok = record.status === "verified";
                      return (
                        <div key={record.label} className="flex flex-col gap-0.5">
                          <span className="flex items-center gap-1 font-medium text-slate-700">
                            {ok ? (
                              <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
                            ) : (
                              <XCircle className="h-3.5 w-3.5 text-amber-400" />
                            )}
                            {record.label}
                          </span>
                          <span className={`pl-4 text-[11px] ${ok ? "text-emerald-600" : "text-amber-600"}`}>
                            {recordStatusText(record.status)}
                          </span>
                        </div>
                      );
                    })}
                  </div>

                  <label
                    className="mt-4 flex items-center gap-2 border-t border-slate-100 pt-3 text-xs text-slate-700"
                    onClick={(event) => event.stopPropagation()}
                  >
                    <input
                      type="checkbox"
                      checked={Boolean(domain.transactional_enabled)}
                      disabled={
                        updatingTransactionalId !== null ||
                        (!isReady || domain.spf_status !== "verified" || domain.dkim_status !== "verified") &&
                          !domain.transactional_enabled
                      }
                      onChange={() => void handleTransactionalToggle(domain)}
                    />
                    <span>
                      Allow transactional email
                      {updatingTransactionalId === domain.id ? " (saving...)" : ""}
                      <span className="ml-1 text-slate-500">Verified SPF and DKIM required; DMARC is advisory.</span>
                    </span>
                  </label>

                  {isReady ? <DomainQuotaPanel domainId={domain.id} domainName={domain.domain} /> : null}

                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      void handleVerify(domain.id, domain.domain);
                    }}
                    disabled={verifyingId === domain.id}
                    className={`mt-4 inline-flex h-9 w-full items-center justify-center gap-1.5 rounded-lg text-sm font-medium disabled:opacity-60 ${
                      isReady
                        ? "bg-white text-slate-700 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
                        : "bg-indigo-600 text-white hover:bg-indigo-500"
                    }`}
                  >
                    <RefreshCw className={`h-4 w-4 ${verifyingId === domain.id ? "animate-spin" : ""}`} />
                    {verifyingId === domain.id
                      ? "Checking DNS..."
                      : isReady
                        ? "Re-check DNS"
                        : "Check now"}
                  </button>
                </CardContent>
              </Card>
            );
          })
        )}
      </div>

      <div ref={dnsSectionRef}>
        <Card className="w-full min-w-0 overflow-hidden">
          <CardHeader className="flex items-start justify-between">
            <div>
              <CardTitle id="dns-records">DNS records</CardTitle>
              <CardDescription>
                Pending domains show the DNS records needed for verification. Verified-domain records stay hidden until you choose View records.
                Ownership, SPF, and DKIM gate verification; DMARC and MX are advisory.
              </CardDescription>
            </div>
            <div className="flex items-center gap-2">
              {selectedDomain ? (
                <button
                  onClick={() => setSelectedDomainId(null)}
                  className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
                >
                  Show pending records
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => setShowFullNames((current) => !current)}
                className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
              >
                {showFullNames ? "Show relative names" : "Show full names"}
              </button>
              {domains.length > 0 ? (
                <Badge variant="success">
                  {verifiedCount} of {domains.length} verified
                </Badge>
              ) : null}
            </div>
          </CardHeader>
          <CardContent className="min-w-0 px-0 py-0">
            {domains.length === 0 ? (
              <div className="p-8 text-center text-slate-500">Add a domain to see its DNS records.</div>
            ) : (
              <>
                {selectedDomain ? (
                  <div className="border-b border-slate-100 px-6 py-2 text-xs font-medium text-slate-600">
                    Showing records for <span className="font-semibold">{selectedDomain.domain}</span>
                  </div>
                ) : pendingDnsDomains.length > 0 ? (
                  <div className="border-b border-slate-100 px-6 py-2 text-xs font-semibold text-slate-700">
                    Pending verification
                  </div>
                ) : null}
                {(pendingDnsDomains.length > 0 || selectedDomain) ? (
                  <Table className="min-w-[1240px] table-fixed">
                    <Thead>
                      <Tr>
                        <Th className="w-[7%]">Type</Th>
                        <Th className="w-[14%]">Domain</Th>
                        <Th className="w-[21%]">Name</Th>
                        <Th className="w-[42%]">Value</Th>
                        <Th className="w-[7%]">TTL</Th>
                        <Th className="w-[9%]">Status</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      {dnsRecordsQuery.error ? (
                        <Tr>
                          <Td colSpan={6} className="p-6 text-center text-sm text-red-600">
                            Could not load DNS records: {dnsRecordsQuery.error.message}
                          </Td>
                        </Tr>
                      ) : dnsRecordsQuery.isLoading ? (
                        <Tr>
                          <Td colSpan={6} className="p-6 text-center text-sm text-slate-500">
                            Loading DNS records...
                          </Td>
                        </Tr>
                      ) : domainsForTable.every((domain) => !dnsRecordsQuery.data?.[domain.id]?.length) ? (
                        <Tr>
                          <Td colSpan={6} className="p-6 text-center text-sm text-slate-500">
                            No DNS records to display.
                          </Td>
                        </Tr>
                      ) : (
                        domainsForTable.flatMap((domain) =>
                          (dnsRecordsQuery.data?.[domain.id] ?? []).map((record, idx) => (
                            <Tr key={`${domain.id}-${record.name}-${idx}`}>
                            <Td>
                              <Badge variant="outline">{record.type}</Badge>
                            </Td>
                            <Td className="break-all text-xs text-slate-700">{domain.domain}</Td>
                            <Td className="font-mono text-xs text-slate-700">
                              <div className="flex items-center gap-1">
                                <span>{dnsNameForDisplay(record.name, domain.domain, showFullNames)}</span>
                                <button
                                  type="button"
                                  onClick={() => void copyText(record.name, "DNS name")}
                                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                                  aria-label={`Copy DNS name ${record.name}`}
                                >
                                  <Copy className="h-3.5 w-3.5" />
                                </button>
                              </div>
                            </Td>
                            <Td className="max-w-[360px] whitespace-normal font-mono text-xs text-slate-500">
                              <div className="flex items-start gap-1">
                                <span className="break-all whitespace-pre-wrap">{record.value || "Not configured"}</span>
                                <button
                                  type="button"
                                  onClick={() => void copyText(record.value, "DNS value")}
                                  disabled={!record.value}
                                  className="shrink-0 rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700 disabled:opacity-40"
                                  aria-label={`Copy DNS value ${record.name}`}
                                >
                                  <Copy className="h-3.5 w-3.5" />
                                </button>
                              </div>
                              {record.update_recommended ? (
                                <p role="status" className="mt-2 rounded-md bg-amber-50 px-2 py-1.5 font-sans text-xs text-amber-900">
                                  Update your DKIM record to the new SPKI format. The currently published key remains valid.
                                </p>
                              ) : null}
                              {record.found_value ? (
                                <p className="mt-1 break-all whitespace-pre-wrap text-[11px] text-slate-600">
                                  Found: {record.found_value}
                                </p>
                              ) : null}
                              {record.resolvers?.length ? (
                                <details className="mt-1 text-[11px]">
                                  <summary className="cursor-pointer">Resolver results</summary>
                                  {record.resolvers.map((resolver) => (
                                    <p key={resolver.resolver} className="mt-1 break-all">
                                      {resolver.resolver}: {recordStatusText(resolver.status)}
                                      {resolver.found_value ? ` — ${resolver.found_value}` : ""}
                                      {resolver.error ? ` — ${resolver.error}` : ""}
                                    </p>
                                  ))}
                                </details>
                              ) : null}
                            </Td>
                            <Td className="text-xs text-slate-600">{record.ttl}</Td>
                            <Td>
                              <div>
                                <StatusBadge status={recordStatusText(record.status)} />
                                {record.last_checked ? (
                                  <p className="mt-1 whitespace-nowrap text-[10px] text-slate-400">
                                    {formatDateTime(record.last_checked)}
                                  </p>
                                ) : null}
                              </div>
                            </Td>
                            </Tr>
                          )),
                        )
                      )}
                    </Tbody>
                  </Table>
                ) : (
                  <div className="p-6 text-center text-sm text-slate-500">
                    All sending domains are verified. Choose a domain below to view its DNS records.
                  </div>
                )}
                {verifiedDomains.length > 0 ? (
                  <section className="border-t border-slate-100 p-4 sm:p-6" aria-labelledby="verified-domain-records">
                    <div className="mb-3">
                      <h3 id="verified-domain-records" className="text-sm font-semibold text-slate-800">
                        Verified domains
                      </h3>
                      <p className="mt-1 text-xs text-slate-500">
                        DNS records are hidden by default. View them only when you need to review or copy a record.
                      </p>
                    </div>
                    <ul className="divide-y divide-slate-100">
                      {verifiedDomains.map((domain) => (
                        <li key={domain.id} className="flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                          <span className="break-all text-sm font-medium text-slate-700">{domain.domain}</span>
                          <button
                            type="button"
                            aria-label={`View records for ${domain.domain}`}
                            onClick={() => handleViewRecords(domain.id)}
                            className="inline-flex h-9 shrink-0 items-center justify-center rounded-lg px-3 text-sm font-medium text-indigo-700 ring-1 ring-inset ring-indigo-200 hover:bg-indigo-50"
                          >
                            View records
                          </button>
                        </li>
                      ))}
                    </ul>
                  </section>
                ) : null}
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
