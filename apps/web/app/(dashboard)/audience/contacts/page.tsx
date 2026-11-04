"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Download, Plus, Search, Upload, UserCheck, UserMinus, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type Contact, type ListResponse } from "@/lib/auth";
import { formatDate, initials } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

const avatarColors = [
  "bg-indigo-100 text-indigo-700",
  "bg-cyan-100 text-cyan-700",
  "bg-amber-100 text-amber-700",
  "bg-emerald-100 text-emerald-700",
  "bg-rose-100 text-rose-700",
  "bg-violet-100 text-violet-700",
];

export default function ContactsPage() {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
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

  const contacts = query.data?.data ?? [];
  const total = query.data?.pagination.total ?? 0;

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

  if (query.error) {
    toast({
      title: "Unable to load contacts",
      description: query.error.message,
      variant: "destructive",
    });
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
              onClick={() => toast({ title: "Import", description: "CSV import wizard coming in Phase 2." })}
            >
              <Upload className="h-4 w-4" /> Import
            </button>
            <button
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-3.5 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
              onClick={() => toast({ title: "Export", description: "CSV export coming in Phase 2." })}
            >
              <Download className="h-4 w-4" /> Export
            </button>
            <button
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
              onClick={() => toast({ title: "Add contact", description: "Single contact create coming in Phase 2." })}
            >
              <Plus className="h-4 w-4" /> Add contact
            </button>
          </>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Total contacts" value={total} change={12.4} icon={<Users className="h-4 w-4" />} />
        <StatCard
          label="Subscribed"
          value={contacts.filter((c) => c.status === "subscribed").length}
          change={9.8}
          icon={<UserCheck className="h-4 w-4" />}
        />
        <StatCard
          label="Unsubscribed"
          value={contacts.filter((c) => c.status === "unsubscribed").length}
          change={-2.1}
          icon={<UserMinus className="h-4 w-4" />}
        />
        <StatCard
          label="Bounced or complained"
          value={contacts.filter((c) => c.status === "bounced" || c.status === "complained").length}
          change={0.4}
          valueIsNumber
        />
      </div>

      <Card className="mt-5">
        <div className="flex flex-col gap-3 border-b border-slate-100 px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-1 flex-col gap-2 sm:flex-row">
            <div className="relative sm:max-w-xs sm:flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
              <input
                placeholder="Search by name or email"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="h-9 w-full rounded-lg border-0 bg-slate-50 pl-9 pr-3 text-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:bg-white focus:ring-2 focus:ring-inset focus:ring-indigo-500"
              />
            </div>
            <select
              value={status}
              onChange={(e) => setStatus(e.target.value)}
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

        <Table>
          <Thead>
            <Tr>
              <Th className="w-8">
                <input type="checkbox" aria-label="Select all contacts" />
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
                    <input type="checkbox" aria-label={`Select ${contact.email}`} />
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
                      <span className="text-xs text-slate-400">—</span>
                    </div>
                  </Td>
                  <Td className="text-xs text-slate-600">{contact.source || "—"}</Td>
                  <Td className="text-xs text-slate-500">{formatDate(contact.created_at)}</Td>
                  <Td>
                    <button
                      className="text-xs font-medium text-indigo-600 hover:text-indigo-500"
                      onClick={() => toast({ title: "Contact profile", description: "Contact detail view coming in Phase 2." })}
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
    </div>
  );
}
