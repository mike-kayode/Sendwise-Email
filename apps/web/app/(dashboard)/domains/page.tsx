"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle2, Copy, Globe, Key, Plus, RefreshCw, ShieldCheck, XCircle } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { StatusBadge, Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type SendingDomain, type DomainDNSRecord } from "@/lib/auth";
import { formatDateTime } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

const dnsRecords: DomainDNSRecord[] = [];

export default function DomainsPage() {
  const [newDomain, setNewDomain] = useState("");
  const [dkimKey, setDkimKey] = useState("");
  const [editingDkimDomain, setEditingDkimDomain] = useState<string | null>(null);
  const domainsQuery = useQuery({
    queryKey: ["domains"],
    queryFn: async () => apiFetch<SendingDomain[]>("/domains"),
  });

  const domains = domainsQuery.data ?? [];
  const { toast } = useToast();

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
    toast({
      title: "Unable to load domains",
      description: domainsQuery.error.message,
      variant: "destructive",
    });
  }

  const handleAddDomain = async () => {
    if (!newDomain.trim()) return;
    try {
      await apiFetch("/domains", {
        method: "POST",
        body: JSON.stringify({ domain: newDomain }),
      });
      setNewDomain("");
      void domainsQuery.refetch();
      toast({ title: "Domain added", description: "Verification will run automatically." });
    } catch (err: any) {
      toast({ title: "Failed to add domain", description: err?.message ?? "Unknown error", variant: "destructive" });
    }
  };

  const handleVerify = async (id: string) => {
    try {
      await apiFetch(`/domains/${id}/verify`, { method: "POST" });
      void domainsQuery.refetch();
      toast({ title: "Verification started", description: "DNS records are being checked." });
    } catch (err: any) {
      toast({ title: "Verification failed", description: err?.message ?? "Unknown error", variant: "destructive" });
    }
  };

  return (
    <div>
      <PageHeader
        eyebrow="Deliverability"
        title="Sending domains"
        description="Authenticate your domains with SPF, DKIM, and DMARC so inbox providers trust your mail."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
            onClick={() => toast({ title: "Verify all", description: "Bulk verify coming in Phase 2." })}
          >
            <RefreshCw className="h-4 w-4" /> Verify all
          </button>
        }
      />

      <div className="mb-5 flex flex-col gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-start gap-2 text-sm text-amber-900">
          <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            <span className="font-medium">One domain needs attention.</span>{" "}
            {domains.filter((d) => d.status !== "verified").length > 0 ? (
              "Some domains are not yet verified, which can hurt deliverability."
            ) : (
              "All domains are verified."
            )}
          </span>
        </div>
        <button className="text-sm font-medium text-amber-900 underline-offset-2 hover:underline">
          Review records
        </button>
      </div>

      <div className="mb-5 grid gap-4 md:grid-cols-3">
        {domains.length === 0 ? (
          <div className="col-span-3 flex flex-col items-center justify-center py-12 text-center">
            <Globe className="mb-3 h-8 w-8 text-slate-300" />
            <p className="text-sm text-slate-500">No sending domains yet.</p>
          </div>
        ) : (
          domains.map((domain) => (
            <Card key={domain.id}>
              <CardContent className="p-5">
                <div className="flex items-start justify-between">
                  <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                    <Globe className="h-4 w-4" />
                  </span>
                  <button
                    onClick={() => handleVerify(domain.id)}
                    className="rounded p-1 text-slate-400 hover:bg-slate-100"
                    aria-label={`Verify ${domain.domain}`}
                  >
                    <RefreshCw className="h-3.5 w-3.5" />
                  </button>
                </div>
                <p className="mt-3 text-sm font-semibold text-slate-900">{domain.domain}</p>
                <p className="text-xs text-slate-500">
                  Checked {domain.last_checked_at ? formatDateTime(domain.last_checked_at) : "never"}
                </p>
                <div className="mt-4 grid grid-cols-3 gap-2 border-t border-slate-100 pt-3 text-xs">
                  {[
                    { label: "SPF", ok: domain.spf_status === "verified" },
                    { label: "DKIM", ok: domain.dkim_status === "verified" },
                    { label: "DMARC", ok: domain.dmarc_status === "verified" },
                  ].map((record) => (
                    <div key={record.label} className="flex items-center gap-1">
                      {record.ok ? (
                        <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
                      ) : (
                        <XCircle className="h-3.5 w-3.5 text-slate-300" />
                      )}
                      <span className={record.ok ? "text-slate-600" : "text-slate-400"}>
                        {record.label}
                      </span>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          ))
        )}
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex items-start justify-between">
            <div>
              <CardTitle>DNS records</CardTitle>
              <CardDescription>
                Add these records at your DNS provider, then click verify.
                Verification runs automatically every few minutes.
                DNS changes can take up to 48 hours to propagate.
              </CardDescription>
            </div>
            {domains.length > 0 ? (
              <Badge variant="success">
                {domains.filter((d) => d.status === "verified").length} of{" "}
                {domains.length} verified
              </Badge>
            ) : null}
          </CardHeader>
          <CardContent className="px-0 py-0">
            {domains.length === 0 ? (
              <div className="p-8 text-center text-slate-500">
                Add a domain to see its DNS records.
              </div>
            ) : (
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
                  {domains.flatMap((domain) =>
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
                                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700 ml-1"
                                  aria-label="Cancel"
                                >
                                  <XCircle className="h-3.5 w-3.5" />
                                </button>
                              </>
                            ) : (
                              record.status !== "verified" && isDkim ? (
                                <button
                                  onClick={() => setEditingDkimDomain(domain.id)}
                                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                                  aria-label="Enter DKIM public key"
                                >
                                  <Key className="h-3.5 w-3.5" />
                                </button>
                              ) : (
                                <button
                                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
                                  aria-label="Copy record"
                                >
                                  <Copy className="h-3.5 w-3.5" />
                                </button>
                              )
                            )}
                          </Td>
                        </Tr>
                      );
                    })
                  )}
                </Tbody>
              </Table>
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
            />
            <button
              onClick={handleAddDomain}
              className="inline-flex h-9 w-full items-center justify-center gap-1.5 rounded-lg bg-indigo-600 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
              disabled={!newDomain.trim()}
            >
              <Plus className="h-4 w-4" /> Add domain
            </button>
            <div className="rounded-lg bg-slate-50 p-3 text-xs text-slate-500">
              After adding, we generate DKIM keys and show the exact records to add. Verification runs
              automatically every few minutes.
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
