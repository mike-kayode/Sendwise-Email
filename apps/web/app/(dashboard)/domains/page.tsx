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

export default function DomainsPage() {
  const [newDomain, setNewDomain] = useState("");
  const [selectedDomainId, setSelectedDomainId] = useState<string | null>(null);
  const [verifyingAll, setVerifyingAll] = useState(false);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
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
  const domainIDs = domains.map((domain) => domain.id);
  const dnsRecordsQuery = useQuery({
    queryKey: ["domain-dns-records", domainIDs],
    enabled: domainIDs.length > 0,
    queryFn: async () => {
      const entries = await Promise.all(
        domains.map(async (domain) => {
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
  const verifiedCount = domains.length - needsAttention.length;
  const selectedDomain = domains.find((d) => d.id === selectedDomainId) ?? null;
  const domainsForTable = selectedDomain ? [selectedDomain] : domains;

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

  const handleVerify = useCallback(async (id: string, domainName?: string, silent = false) => {
    setVerifyingId(id);
    try {
      const result = await apiFetch<{
        domain: SendingDomain;
        dns_records: DomainDNSRecord[];
      }>(`/domains/${id}/verify`, { method: "POST" });
      await queryClient.invalidateQueries({ queryKey: ["domains"] });
      await queryClient.invalidateQueries({ queryKey: ["domain-dns-records"] });
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
    setVerifyingAll(false);
    toast({
      title: "Verification run finished",
      description: `${passed} of ${unverified.length} domain(s) verified; ${failed} check(s) failed. See each domain for details.`,
      variant: passed === unverified.length ? undefined : "destructive",
    });
  };

  const handleReviewRecords = () => {
    const target = unverified[0] ?? domains.find((domain) => domain.owner_conflict) ?? domains[0];
    if (target) setSelectedDomainId(target.id);
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
            const isSelected = selectedDomainId === domain.id;
            return (
              <Card
                key={domain.id}
                onClick={() => setSelectedDomainId(isSelected ? null : domain.id)}
                className={`cursor-pointer transition ${isSelected ? "ring-2 ring-indigo-500" : ""}`}
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
                Add these records at your DNS provider, then click verify. Checks query public and authoritative nameservers.
                Ownership, SPF, and DKIM gate verification; DMARC and MX are advisory.
              </CardDescription>
            </div>
            <div className="flex items-center gap-2">
              {selectedDomain ? (
                <button
                  onClick={() => setSelectedDomainId(null)}
                  className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
                >
                  Show all domains
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
                ) : null}
                <Table className="min-w-[1100px] table-fixed">
                  <Thead>
                    <Tr>
                      <Th className="w-[8%]">Type</Th>
                      <Th className="w-[22%]">Name</Th>
                      <Th className="w-[46%]">Value</Th>
                      <Th className="w-[8%]">TTL</Th>
                      <Th className="w-[16%]">Status</Th>
                    </Tr>
                  </Thead>
                  <Tbody>
                    {dnsRecordsQuery.error ? (
                      <Tr>
                        <Td colSpan={5} className="p-6 text-center text-sm text-red-600">
                          Could not load DNS records: {dnsRecordsQuery.error.message}
                        </Td>
                      </Tr>
                    ) : dnsRecordsQuery.isLoading ? (
                      <Tr>
                        <Td colSpan={5} className="p-6 text-center text-sm text-slate-500">
                          Loading DNS records...
                        </Td>
                      </Tr>
                    ) : (
                      domainsForTable.flatMap((domain) =>
                        (dnsRecordsQuery.data?.[domain.id] ?? []).map((record, idx) => (
                          <Tr key={`${domain.id}-${record.name}-${idx}`}>
                            <Td>
                              <Badge variant="outline">{record.type}</Badge>
                            </Td>
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
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
