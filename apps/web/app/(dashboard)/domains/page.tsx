"use client";

import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle2, Copy, Globe, Key, Plus, RefreshCw, ShieldCheck, XCircle, Send } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge, Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type SendingDomain } from "@/lib/auth";
import { formatDateTime } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

const recordStatusText = (status?: string) => (status ? status.replace("_", " ") : "unknown");

export default function DomainsPage() {
  const [newDomain, setNewDomain] = useState("");
  const [dkimKey, setDkimKey] = useState("");
  const [editingDkimDomain, setEditingDkimDomain] = useState<string | null>(null);
  const [selectedDomainId, setSelectedDomainId] = useState<string | null>(null);
  const [verifyingAll, setVerifyingAll] = useState(false);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const dnsSectionRef = useRef<HTMLDivElement>(null);
  const { toast } = useToast();

  const domainsQuery = useQuery({
    queryKey: ["domains"],
    queryFn: async () => apiFetch<SendingDomain[]>("/domains"),
  });

  const domains = domainsQuery.data ?? [];
  const unverified = domains.filter((d) => d.status !== "verified");
  const verifiedCount = domains.length - unverified.length;
  const selectedDomain = domains.find((d) => d.id === selectedDomainId) ?? null;
  const domainsForTable = selectedDomain ? [selectedDomain] : domains;

  const handleSetDkimKey = async (domainId: string) => {
    if (!dkimKey.trim()) return;
    try {
      await apiFetch(`/domains/${domainId}/dkim-key`, {
        method: "POST",
        body: JSON.stringify({ dkim_public_key: dkimKey }),
      });
      setEditingDkimDomain(null);
      setDkimKey("");
      void domainsQuery.refetch();
      toast({ title: "DKIM key saved", description: "Public key stored for this domain." });
    } catch (err: any) {
      toast({
        title: "Failed to save DKIM key",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    }
  };

  const handleAddDomain = async () => {
    if (!newDomain.trim()) return;
    try {
      await apiFetch("/domains", {
        method: "POST",
        body: JSON.stringify({ domain: newDomain }),
      });
      setNewDomain("");
      void domainsQuery.refetch();
      toast({ title: "Domain added", description: "Add the DNS records below, then verify." });
    } catch (err: any) {
      toast({ title: "Failed to add domain", description: err?.message ?? "Unknown error", variant: "destructive" });
    }
  };

  const handleVerify = async (id: string, domainName?: string) => {
    setVerifyingId(id);
    try {
      await apiFetch(`/domains/${id}/verify`, { method: "POST" });
      await domainsQuery.refetch();
      const updated = domainsQuery.data?.find((d) => d.id === id);
      toast({
        title: updated?.status === "verified" ? `${domainName ?? "Domain"} is verified` : "DNS check complete",
        description:
          updated?.status === "verified"
            ? "SPF, DKIM, and DMARC all passed. This domain is ready to send."
            : "Some records are still missing. Review the DNS table below.",
        variant: updated?.status === "verified" ? undefined : "destructive",
      });
    } catch (err: any) {
      toast({ title: "Verification failed", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setVerifyingId(null);
    }
  };

  const handleVerifyAll = async () => {
    if (unverified.length === 0) return;
    setVerifyingAll(true);
    let passed = 0;
    for (const domain of unverified) {
      try {
        const result = await apiFetch<{ domain: SendingDomain }>(`/domains/${domain.id}/verify`, { method: "POST" });
        if (result.domain?.status === "verified") passed += 1;
      } catch {
        // Counted as not passed; refetch below shows the real state.
      }
    }
    await domainsQuery.refetch();
    setVerifyingAll(false);
    toast({
      title: "Verification run finished",
      description: `${passed} of ${unverified.length} domain(s) now fully verified.`,
      variant: passed === unverified.length ? undefined : "destructive",
    });
  };

  const handleReviewRecords = () => {
    const target = unverified[0] ?? domains[0];
    if (target) setSelectedDomainId(target.id);
    dnsSectionRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const copyText = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast({ title: `${label} copied`, description: "Paste it at your DNS provider." });
    } catch {
      toast({ title: "Copy failed", description: "Select the value and copy it manually.", variant: "destructive" });
    }
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

      {domains.length === 0 ? null : unverified.length === 0 ? (
        <div className="mb-5 flex items-center gap-2 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-900">
          <Send className="h-4 w-4 shrink-0" />
          <span>
            <span className="font-medium">All {domains.length} domain(s) are ready to send.</span>{" "}
            SPF, DKIM, and DMARC are passing for every domain.
          </span>
        </div>
      ) : (
        <div className="mb-5 flex flex-col gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-2 text-sm text-amber-900">
            <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" />
            <span>
              <span className="font-medium">
                {unverified.length} of {domains.length} domain(s) need attention.
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
            const isReady = domain.status === "verified";
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
                    {isReady ? (
                      <Badge variant="success">
                        <CheckCircle2 className="mr-1 h-3 w-3" /> Ready to send
                      </Badge>
                    ) : (
                      <Badge variant="warning">
                        <XCircle className="mr-1 h-3 w-3" /> Verification needed
                      </Badge>
                    )}
                  </div>
                  <p className="mt-3 text-sm font-semibold text-slate-900">{domain.domain}</p>
                  <p className="text-xs text-slate-500">
                    {domain.last_checked_at
                      ? `Checked ${formatDateTime(domain.last_checked_at)}`
                      : "Never checked"}{" "}
                    · <StatusBadge status={domain.status} />
                  </p>

                  <div className="mt-4 grid grid-cols-3 gap-2 border-t border-slate-100 pt-3 text-xs">
                    {[
                      { label: "SPF", status: domain.spf_status },
                      { label: "DKIM", status: domain.dkim_status },
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
                        : "Verify now"}
                  </button>
                </CardContent>
              </Card>
            );
          })
        )}
      </div>

      <div ref={dnsSectionRef}>
      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex items-start justify-between">
            <div>
              <CardTitle id="dns-records">DNS records</CardTitle>
              <CardDescription>
                Add these records at your DNS provider, then click verify. DNS changes can take up to 48
                hours to propagate.
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
              {domains.length > 0 ? (
                <Badge variant="success">
                  {verifiedCount} of {domains.length} verified
                </Badge>
              ) : null}
            </div>
          </CardHeader>
          <CardContent className="px-0 py-0">
            {domains.length === 0 ? (
              <div className="p-8 text-center text-slate-500">Add a domain to see its DNS records.</div>
            ) : (
              <>
                {selectedDomain ? (
                  <div className="border-b border-slate-100 px-6 py-2 text-xs font-medium text-slate-600">
                    Showing records for <span className="font-semibold">{selectedDomain.domain}</span>
                  </div>
                ) : null}
                <Table>
                  <Thead>
                    <Tr>
                      <Th>Type</Th>
                      <Th>Name</Th>
                      <Th>Value</Th>
                      <Th>Purpose</Th>
                      <Th>Status</Th>
                      <Th />
                    </Tr>
                  </Thead>
                  <Tbody>
                    {domainsForTable.flatMap((domain) =>
                      [
                        {
                          type: "TXT",
                          name: domain.domain ?? "",
                          value: "v=spf1 mx ~all",
                          purpose: "SPF authorizes your sending infrastructure.",
                          status: domain.spf_status,
                        },
                        {
                          type: "TXT",
                          name: `${domain.dkim_selector || "mail"}._domainkey.${domain.domain ?? ""}`,
                          value: "v=DKIM1; k=rsa; p=<PUBLIC_KEY_FROM_STALWART>",
                          purpose: "DKIM cryptographically signs outbound messages.",
                          status: domain.dkim_status,
                        },
                        {
                          type: "TXT",
                          name: `_dmarc.${domain.domain ?? ""}`,
                          value: `v=DMARC1; p=none; rua=mailto:dmarc@${domain.domain}`,
                          purpose: "DMARC tells receivers what to do on auth failure.",
                          status: domain.dmarc_status,
                        },
                      ].map((record, idx) => {
                        const isDkim = record.type === "TXT" && record.name.includes("_domainkey");
                        const isEditingDkim = isDkim && editingDkimDomain === domain.id;
                        return (
                          <Tr key={`${domain.id}-${idx}`}>
                            <Td>
                              <Badge variant="outline">{record.type}</Badge>
                            </Td>
                            <Td className="font-mono text-xs text-slate-700">
                              {record.name}
                            </Td>
                            <Td className="max-w-[240px] truncate font-mono text-xs text-slate-500">
                              {isEditingDkim ? (
                                <Input
                                  value={dkimKey}
                                  onChange={(e) => setDkimKey(e.target.value)}
                                  className="h-7 text-xs font-mono truncate"
                                  placeholder="Paste the DKIM public key"
                                />
                              ) : (
                                record.value
                              )}
                            </Td>
                            <Td className="text-xs text-slate-600">{record.purpose}</Td>
                            <Td>
                              <StatusBadge status={record.status ?? ""} />
                            </Td>
                            <Td>
                              {isEditingDkim ? (
                                <>
                                  <button
                                    onClick={() => handleSetDkimKey(domain.id)}
                                    className="rounded p-1 text-emerald-600 hover:bg-emerald-50"
                                    aria-label="Save DKIM key"
                                  >
                                    <Key className="h-3.5 w-3.5" />
                                  </button>
                                  <button
                                    onClick={() => setEditingDkimDomain(null)}
                                    className="ml-1 rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                                    aria-label="Cancel"
                                  >
                                    <XCircle className="h-3.5 w-3.5" />
                                  </button>
                                </>
                              ) : record.status !== "verified" && isDkim ? (
                                <button
                                  onClick={() => setEditingDkimDomain(domain.id)}
                                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                                  aria-label="Enter DKIM public key"
                                >
                                  <Key className="h-3.5 w-3.5" />
                                </button>
                              ) : (
                                <button
                                  onClick={() => copyText(record.value, `${record.type} record`)}
                                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                                  aria-label="Copy record"
                                >
                                  <Copy className="h-3.5 w-3.5" />
                                </button>
                              )}
                            </Td>
                          </Tr>
                        );
                      }),
                    )}
                  </Tbody>
                </Table>
              </>
            )}
          </CardContent>
        </Card>

        <Card className="h-fit">
          <CardHeader>
            <CardTitle>Add a domain</CardTitle>
            <CardDescription>
              Use a subdomain for sending to protect your root domain.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <Input
              placeholder="mail.yourdomain.com"
              value={newDomain}
              onChange={(e) => setNewDomain(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void handleAddDomain();
              }}
            />
            <button
              onClick={() => void handleAddDomain()}
              className="inline-flex h-9 w-full items-center justify-center gap-1.5 rounded-lg bg-indigo-600 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
              disabled={!newDomain.trim()}
            >
              <Plus className="h-4 w-4" /> Add domain
            </button>
            <div className="rounded-lg bg-slate-50 p-3 text-xs text-slate-500">
              After adding, we show the exact records to create. Click <span className="font-medium">Verify now</span>{" "}
              once your DNS provider has published them.
            </div>
          </CardContent>
        </Card>
        </div>
      </div>
    </div>
  );
}
