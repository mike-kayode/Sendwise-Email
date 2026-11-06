"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Download, Plus, Search, Upload, UserCheck, UserMinus, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import {
  apiFetch,
  getAccessToken,
  type AnalyticsOverview,
  type Contact,
  type ListResponse,
} from "@/lib/auth";
import { apiUrl } from "@/lib/api-url";
import { formatDate, formatDateTime, initials } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";
import { ImportDialog } from "./import-dialog";
import { AddContactDialog } from "./add-contact-dialog";

const avatarColors = [
  "bg-indigo-100 text-indigo-700",
  "bg-cyan-100 text-cyan-700",
  "bg-amber-100 text-amber-700",
  "bg-emerald-100 text-emerald-700",
  "bg-rose-100 text-rose-700",
  "bg-violet-100 text-violet-700",
];

type ContactStats = {
  total: number;
  subscribed: number;
  unsubscribed: number;
  bouncedOrComplained: number;
};

type ContactTag = {
  id: string;
  name: string;
  color: string;
  contact_count: number;
};

type ContactTagPage = {
  data: ContactTag[];
  pagination: { total: number; limit: number; offset: number };
};

export default function ContactsPage() {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
  const [isImportOpen, setIsImportOpen] = useState(false);
  const [isAddOpen, setIsAddOpen] = useState(false);
  const [viewingId, setViewingId] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const [selectedContactIDs, setSelectedContactIDs] = useState<string[]>([]);
  const [selectedTagID, setSelectedTagID] = useState("");
  const { toast } = useToast();

  const query = useQuery({
    queryKey: ["contacts", search, status, offset],
    queryFn: async () => {
      const params = new URLSearchParams();
      if (search) params.set("search", search);
      if (status) params.set("status", status);
      params.set("limit", "20");
      params.set("offset", String(offset));
      return apiFetch<ListResponse<Contact>>(`/contacts?${params.toString()}`);
    },
  });

  const statsQuery = useQuery({
    queryKey: ["contacts", "stats"],
    queryFn: async (): Promise<ContactStats> => {
      const countByStatus = async (contactStatus: string) => {
        const result = await apiFetch<ListResponse<Contact>>(
          `/contacts?status=${contactStatus}&limit=1`,
        );
        return result.pagination.total;
      };
      const [overview, unsubscribed, bounced, complained] = await Promise.all([
        apiFetch<AnalyticsOverview>("/analytics/overview"),
        countByStatus("unsubscribed"),
        countByStatus("bounced"),
        countByStatus("complained"),
      ]);
      return {
        total: overview.contacts,
        subscribed: overview.subscribed,
        unsubscribed,
        bouncedOrComplained: bounced + complained,
      };
    },
  });

  const detailQuery = useQuery({
    queryKey: ["contact", viewingId],
    queryFn: async () => apiFetch<Contact>(`/contacts/${viewingId}`),
    enabled: viewingId !== null,
  });
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => apiFetch<ContactTagPage>("/tags?limit=200"),
  });
  const contactTagsQuery = useQuery({
    queryKey: ["contact", viewingId, "tags"],
    queryFn: () => apiFetch<ContactTagPage>(`/contacts/${viewingId}/tags?limit=200`),
    enabled: viewingId !== null,
  });

  const contacts = query.data?.data ?? [];
  const total = query.data?.pagination.total ?? 0;
  const stats = statsQuery.data;

  const refreshAll = () => {
    void query.refetch();
    void statsQuery.refetch();
  };

  const assignTagToSelected = async () => {
    if (!selectedTagID || selectedContactIDs.length === 0) return;
    try {
      await apiFetch("/contacts/tags", {
        method: "POST",
        body: JSON.stringify({ tag_id: selectedTagID, contact_ids: selectedContactIDs }),
      });
      setSelectedContactIDs([]);
      await Promise.all([tagsQuery.refetch(), query.refetch()]);
      toast({ title: "Tag assigned", description: `Tag applied to ${selectedContactIDs.length} selected contacts.` });
    } catch (error) {
      toast({ title: "Tag assignment failed", description: error instanceof Error ? error.message : "The tag could not be assigned.", variant: "destructive" });
    }
  };

  const updateContactTag = async (tagID: string, assign: boolean) => {
    if (!viewingId) return;
    try {
      await apiFetch(`/contacts/${viewingId}/tags/${tagID}`, { method: assign ? "POST" : "DELETE" });
      await Promise.all([contactTagsQuery.refetch(), tagsQuery.refetch(), query.refetch()]);
    } catch (error) {
      toast({ title: "Tag update failed", description: error instanceof Error ? error.message : "The contact tag could not be updated.", variant: "destructive" });
    }
  };

  const handleExport = async () => {
    if (exporting) return;
    setExporting(true);
    try {
      const token = getAccessToken();
      const response = await fetch(apiUrl("/api/v1/contacts/export"), {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        credentials: "include",
        cache: "no-store",
      });
      if (!response.ok) {
        throw new Error(
          response.status === 401
            ? "You must sign in again to export contacts."
            : `Export failed (HTTP ${response.status}).`,
        );
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "contacts.csv";
      anchor.click();
      URL.revokeObjectURL(url);
      toast({ title: "Export ready", description: "contacts.csv has been downloaded." });
    } catch (err: any) {
      toast({
        title: "Export failed",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    } finally {
      setExporting(false);
    }
  };

  if (query.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Audience"
          title="Contacts"
          description="Loading your contacts..."
        />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <StatCard key={i} label="Loading" value="—" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Audience"
        title="Contacts"
        description="Every person in your workspace, with their subscription state and tags."
        actions={
          <>
            <button
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-3.5 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
              onClick={() => setIsImportOpen(true)}
            >
              <Upload className="h-4 w-4" /> Import
            </button>
            <button
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-3.5 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50 disabled:opacity-50"
              onClick={() => void handleExport()}
              disabled={exporting}
            >
              <Download className="h-4 w-4" /> {exporting ? "Exporting..." : "Export"}
            </button>
            <button
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
              onClick={() => setIsAddOpen(true)}
            >
              <Plus className="h-4 w-4" /> Add contact
            </button>
          </>
        }
      />

      {query.error ? (
        <div className="mb-4 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
          Unable to load contacts: {(query.error as Error).message}
        </div>
      ) : null}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label="Total contacts"
          value={stats?.total ?? total}
          icon={<Users className="h-4 w-4" />}
        />
        <StatCard
          label="Subscribed"
          value={stats?.subscribed ?? 0}
          icon={<UserCheck className="h-4 w-4" />}
        />
        <StatCard
          label="Unsubscribed"
          value={stats?.unsubscribed ?? 0}
          icon={<UserMinus className="h-4 w-4" />}
        />
        <StatCard label="Bounced or complained" value={stats?.bouncedOrComplained ?? 0} />
      </div>

      <Card className="mt-5">
        <div className="flex flex-col gap-3 border-b border-slate-100 px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-1 flex-col gap-2 sm:flex-row">
            <div className="relative sm:max-w-xs sm:flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
              <input
                placeholder="Search by name or email"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setOffset(0);
                }}
                className="h-9 w-full rounded-lg border-0 bg-slate-50 pl-9 pr-3 text-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:bg-white focus:ring-2 focus:ring-inset focus:ring-indigo-500"
              />
            </div>
            <select
              value={status}
              onChange={(e) => {
                setStatus(e.target.value);
                setOffset(0);
              }}
              className="h-9 rounded-lg border-0 bg-slate-50 px-3 text-sm text-slate-700 ring-1 ring-inset ring-slate-200"
            >
              <option value="">All statuses</option>
              <option value="subscribed">Subscribed</option>
              <option value="pending">Pending</option>
              <option value="unsubscribed">Unsubscribed</option>
              <option value="bounced">Bounced</option>
              <option value="complained">Complained</option>
            </select>
          </div>
          <p className="text-xs text-slate-500">
            Showing {contacts.length} of {total}
          </p>
        </div>
        {selectedContactIDs.length > 0 ? (
          <div className="flex flex-wrap items-center gap-2 border-b border-slate-100 bg-indigo-50 px-4 py-3">
            <span className="text-sm text-indigo-900">{selectedContactIDs.length} selected</span>
            <select
              aria-label="Tag to assign"
              value={selectedTagID}
              onChange={(event) => setSelectedTagID(event.target.value)}
              className="h-9 rounded-lg border border-slate-200 bg-white px-3 text-sm"
              disabled={tagsQuery.isLoading || tagsQuery.data?.data.length === 0}
            >
              <option value="">Choose a tag</option>
              {(tagsQuery.data?.data ?? []).map((tag) => <option key={tag.id} value={tag.id}>{tag.name}</option>)}
            </select>
            <button
              type="button"
              className="rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
              disabled={!selectedTagID}
              onClick={() => void assignTagToSelected()}
            >
              Assign tag
            </button>
            <button type="button" className="text-sm text-slate-600" onClick={() => setSelectedContactIDs([])}>Clear selection</button>
            {tagsQuery.error ? <span role="alert" className="text-xs text-rose-700">Could not load tags.</span> : null}
          </div>
        ) : null}

        <Table>
          <Thead>
            <Tr>
              <Th className="w-8">
                <input
                  type="checkbox"
                  aria-label="Select all contacts on this page"
                  checked={contacts.length > 0 && contacts.every((contact) => selectedContactIDs.includes(contact.id))}
                  onChange={(event) => setSelectedContactIDs((current) => event.target.checked
                    ? Array.from(new Set([...current, ...contacts.map((contact) => contact.id)]))
                    : current.filter((id) => !contacts.some((contact) => contact.id === id)))}
                />
              </Th>
              <Th>Contact</Th>
              <Th>Status</Th>
              <Th>Tags</Th>
              <Th>Source</Th>
              <Th>Joined</Th>
              <Th />
            </Tr>
          </Thead>
          <Tbody>
            {contacts.length === 0 ? (
              <Tr>
                <Td colSpan={7} className="text-center py-8 text-slate-500">
                  No contacts found. Import contacts or add them manually.
                </Td>
              </Tr>
            ) : (
              contacts.map((contact, index) => (
                <Tr key={contact.id}>
                  <Td>
                    <input
                      type="checkbox"
                      aria-label={`Select ${contact.email}`}
                      checked={selectedContactIDs.includes(contact.id)}
                      onChange={(event) => setSelectedContactIDs((current) => event.target.checked
                        ? [...current, contact.id]
                        : current.filter((id) => id !== contact.id))}
                    />
                  </Td>
                  <Td>
                    <div className="flex items-center gap-2.5">
                      <span
                        className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-xs font-semibold ${avatarColors[index % avatarColors.length]}`}
                      >
                        {initials(contact.email)}
                      </span>
                      <div>
                        <p className="font-medium text-slate-900">
                          {contact.first_name || contact.last_name || contact.email}
                        </p>
                        <p className="text-xs text-slate-500">{contact.email}</p>
                      </div>
                    </div>
                  </Td>
                  <Td>
                    <StatusBadge status={contact.status} />
                  </Td>
                  <Td>
                    <div className="flex flex-wrap gap-1">
                      {(contact.tags ?? []).length === 0 ? <span className="text-xs text-slate-400">—</span> : null}
                      {(contact.tags ?? []).map((tag) => (
                        <span key={tag.id} className="rounded-full px-2 py-0.5 text-[11px]" style={{ color: tag.color, backgroundColor: `${tag.color}1a` }}>
                          {tag.name}
                        </span>
                      ))}
                    </div>
                  </Td>
                  <Td className="text-xs text-slate-600">{contact.source || "—"}</Td>
                  <Td className="text-xs text-slate-500">{formatDate(contact.created_at)}</Td>
                  <Td>
                    <button
                      className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
                      onClick={() => setViewingId(contact.id)}
                    >
                      View
                    </button>
                  </Td>
                </Tr>
              ))
            )}
          </Tbody>
        </Table>

        <div className="flex items-center justify-between border-t border-slate-100 px-4 py-3 text-xs text-slate-500">
          <span>Showing {Math.min(offset + contacts.length, total)} of {total}</span>
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

      <ImportDialog
        open={isImportOpen}
        onOpenChange={setIsImportOpen}
        onImportComplete={refreshAll}
      />

      <AddContactDialog open={isAddOpen} onOpenChange={setIsAddOpen} onCreated={refreshAll} />

      <Dialog
        open={viewingId !== null}
        onOpenChange={(next) => {
          if (!next) setViewingId(null);
        }}
      >
        <DialogContent>
          <DialogTitle className="text-base font-semibold text-slate-900">
            {detailQuery.data
              ? detailQuery.data.first_name || detailQuery.data.last_name
                ? `${detailQuery.data.first_name} ${detailQuery.data.last_name}`.trim()
                : detailQuery.data.email
              : "Contact"}
          </DialogTitle>
          <DialogDescription>Full profile and subscription history.</DialogDescription>

          {detailQuery.isLoading ? (
            <p className="text-sm text-slate-500">Loading contact...</p>
          ) : detailQuery.error ? (
            <p className="rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-700">
              {(detailQuery.error as Error).message}
            </p>
          ) : detailQuery.data ? (
            <div className="space-y-3 text-sm">
              <div className="flex items-center gap-3">
                <span className="flex h-10 w-10 items-center justify-center rounded-full bg-indigo-100 text-sm font-semibold text-indigo-700">
                  {initials(detailQuery.data.email)}
                </span>
                <div>
                  <p className="font-medium text-slate-900">{detailQuery.data.email}</p>
                  <StatusBadge status={detailQuery.data.status} />
                </div>
              </div>
              <div className="space-y-2 rounded-lg bg-slate-50 p-3">
                {[
                  { label: "First name", value: detailQuery.data.first_name || "—" },
                  { label: "Last name", value: detailQuery.data.last_name || "—" },
                  { label: "Source", value: detailQuery.data.source || "—" },
                  { label: "Joined", value: formatDateTime(detailQuery.data.created_at) },
                  { label: "Updated", value: formatDateTime(detailQuery.data.updated_at) },
                ].map((row) => (
                  <div key={row.label} className="flex justify-between gap-4">
                    <span className="text-slate-500">{row.label}</span>
                    <span className="text-right font-medium text-slate-900">{row.value}</span>
                  </div>
                ))}
              </div>
              <div>
                <p className="mb-2 text-xs font-medium uppercase tracking-wide text-slate-400">Tags</p>
                {contactTagsQuery.isLoading ? <p className="text-xs text-slate-500">Loading tags...</p> : null}
                {contactTagsQuery.error ? <p role="alert" className="text-xs text-rose-700">Unable to load contact tags.</p> : null}
                <div className="mb-2 flex flex-wrap gap-2">
                  {(contactTagsQuery.data?.data ?? []).map((tag) => (
                    <span key={tag.id} className="inline-flex items-center gap-2 rounded-full bg-slate-100 px-2.5 py-1 text-xs">
                      <span style={{ color: tag.color }}>{tag.name}</span>
                      <button type="button" aria-label={`Remove ${tag.name} tag`} onClick={() => void updateContactTag(tag.id, false)} className="text-slate-500">×</button>
                    </span>
                  ))}
                  {contactTagsQuery.data?.data.length === 0 ? <span className="text-xs text-slate-500">No tags assigned.</span> : null}
                </div>
                <div className="flex gap-2">
                  <select
                    aria-label="Tag to assign to contact"
                    value={selectedTagID}
                    onChange={(event) => setSelectedTagID(event.target.value)}
                    className="h-9 min-w-0 flex-1 rounded-lg border border-slate-200 bg-white px-2 text-xs"
                  >
                    <option value="">Choose a tag</option>
                    {(tagsQuery.data?.data ?? []).filter((tag) => !(contactTagsQuery.data?.data ?? []).some((current) => current.id === tag.id))
                      .map((tag) => <option key={tag.id} value={tag.id}>{tag.name}</option>)}
                  </select>
                  <Button size="sm" disabled={!selectedTagID} onClick={() => void updateContactTag(selectedTagID, true)}>Assign</Button>
                </div>
              </div>
              <div>
                <p className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-400">
                  Custom fields
                </p>
                <pre className="overflow-auto rounded-lg bg-slate-900 p-3 text-[11px] leading-relaxed text-slate-100">
                  {JSON.stringify(detailQuery.data.custom_fields ?? {}, null, 2)}
                </pre>
              </div>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  );
}
