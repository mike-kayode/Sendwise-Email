"use client";

import { useQuery } from "@tanstack/react-query";
import { Download, Users } from "lucide-react";
import { useParams } from "next/navigation";
import { useState } from "react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, Tbody, Td, Th, Thead, Tr } from "@/components/ui/table";
import { apiFetch, getAccessToken, type Contact, type ListResponse } from "@/lib/auth";
import { apiUrl } from "@/lib/api-url";
import { useToast } from "@/components/ui/use-toast";

type AudienceList = {
  id: string;
  name: string;
  description: string;
  contact_count: number;
};

export default function AudienceListDetailPage() {
  const params = useParams<{ id: string }>();
  const listId = params.id;
  const [offset, setOffset] = useState(0);
  const [contactsOffset, setContactsOffset] = useState(0);
  const [selectedContactIDs, setSelectedContactIDs] = useState<string[]>([]);
  const [name, setName] = useState<string | null>(null);
  const [description, setDescription] = useState<string | null>(null);
  const { toast } = useToast();

  const listQuery = useQuery({
    queryKey: ["lists", listId, "detail"],
    queryFn: () => apiFetch<AudienceList>(`/lists/${listId}`),
  });
  const membersQuery = useQuery({
    queryKey: ["lists", listId, "contacts", offset],
    queryFn: () => apiFetch<ListResponse<Contact>>(`/lists/${listId}/contacts?limit=20&offset=${offset}`),
  });
  const contactsQuery = useQuery({
    queryKey: ["contacts", "list-add", listId, contactsOffset],
    queryFn: () => apiFetch<ListResponse<Contact>>(`/contacts?limit=200&offset=${contactsOffset}`),
  });

  const members = membersQuery.data?.data ?? [];
  const total = membersQuery.data?.pagination.total ?? 0;
  const memberIDs = new Set(members.map((contact) => contact.id));
  const availableContacts = (contactsQuery.data?.data ?? []).filter((contact) => !memberIDs.has(contact.id));

  const refresh = async () => {
    await Promise.all([listQuery.refetch(), membersQuery.refetch(), contactsQuery.refetch()]);
  };

  const updateList = async () => {
    const updatedName = name ?? listQuery.data?.name ?? "";
    const updatedDescription = description ?? listQuery.data?.description ?? "";
    if (!listQuery.data || !updatedName.trim()) return;
    try {
      await apiFetch(`/lists/${listId}`, {
        method: "PATCH",
        body: JSON.stringify({ name: updatedName, description: updatedDescription }),
      });
      setName(null);
      setDescription(null);
      await refresh();
      toast({ title: "List updated", description: "The list details have been saved." });
    } catch (error) {
      toast({ title: "Update failed", description: error instanceof Error ? error.message : "The list could not be updated.", variant: "destructive" });
    }
  };

  const addSelected = async () => {
    if (selectedContactIDs.length === 0) return;
    try {
      await apiFetch(`/lists/${listId}/contacts`, {
        method: "POST",
        body: JSON.stringify({ contact_ids: selectedContactIDs }),
      });
      setSelectedContactIDs([]);
      await refresh();
      toast({ title: "Contacts added", description: "List membership has been updated." });
    } catch (error) {
      toast({ title: "Could not add contacts", description: error instanceof Error ? error.message : "List membership could not be updated.", variant: "destructive" });
    }
  };

  const removeMember = async (contact: Contact) => {
    if (!window.confirm(`Remove ${contact.email} from this list?`)) return;
    try {
      await apiFetch(`/lists/${listId}/contacts/${contact.id}`, { method: "DELETE" });
      await refresh();
      toast({ title: "Contact removed", description: "The contact remains in your audience." });
    } catch (error) {
      toast({ title: "Could not remove contact", description: error instanceof Error ? error.message : "List membership could not be updated.", variant: "destructive" });
    }
  };

  const exportList = async () => {
    try {
      const token = getAccessToken();
      const response = await fetch(apiUrl(`/api/v1/lists/${listId}/contacts/export`), {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        credentials: "include",
        cache: "no-store",
      });
      if (!response.ok) throw new Error(`List export failed (HTTP ${response.status}).`);
      const url = URL.createObjectURL(await response.blob());
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "list-contacts.csv";
      anchor.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      toast({ title: "Export failed", description: error instanceof Error ? error.message : "The list could not be exported.", variant: "destructive" });
    }
  };

  if (listQuery.isLoading) {
    return <PageHeader eyebrow="Audience" title="List" description="Loading list..." />;
  }
  if (listQuery.error || !listQuery.data) {
    return (
      <div className="space-y-4">
        <PageHeader eyebrow="Audience" title="List" description="Unable to load this list." />
        <p role="alert" className="text-sm text-rose-700">{listQuery.error instanceof Error ? listQuery.error.message : "List not found."}</p>
        <Button variant="secondary" onClick={() => void listQuery.refetch()}>Retry</Button>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <PageHeader
        eyebrow="Audience / Lists"
        title={listQuery.data.name}
        description={`${listQuery.data.contact_count} members · ${listQuery.data.description || "No description"}`}
        actions={<Button variant="secondary" onClick={() => void exportList()}><Download className="mr-2 h-4 w-4" />Export CSV</Button>}
      />

      <Card>
        <CardContent className="grid gap-3 p-5 sm:grid-cols-[1fr_2fr_auto]">
          <Input aria-label="List name" value={name ?? listQuery.data.name} onChange={(event) => setName(event.target.value)} />
          <Input aria-label="List description" value={description ?? listQuery.data.description} onChange={(event) => setDescription(event.target.value)} />
          <Button onClick={() => void updateList()} disabled={!((name ?? listQuery.data.name).trim())}>Save details</Button>
        </CardContent>
      </Card>

      <Card>
        <div className="border-b border-slate-100 px-5 py-4">
          <h2 className="text-sm font-semibold">Add contacts</h2>
          <p className="mt-1 text-xs text-slate-500">Choose contacts from your audience and add them to this list.</p>
        </div>
        <CardContent className="space-y-3 p-5">
          {contactsQuery.isLoading ? <p className="text-sm text-slate-500">Loading contacts...</p> : null}
          {contactsQuery.error ? (
            <div role="alert" className="flex items-center justify-between text-sm text-rose-700">
              <span>{(contactsQuery.error as Error).message}</span>
              <Button variant="secondary" onClick={() => void contactsQuery.refetch()}>Retry</Button>
            </div>
          ) : null}
          {!contactsQuery.isLoading && !contactsQuery.error && availableContacts.length === 0 ? (
            <p className="text-sm text-slate-500">All available contacts are already in this list.</p>
          ) : null}
          <div className="grid gap-2 sm:grid-cols-2">
            {availableContacts.slice(0, 40).map((contact) => (
              <label key={contact.id} className="flex items-center gap-2 rounded-md border border-slate-100 px-3 py-2 text-sm">
                <input
                  type="checkbox"
                  checked={selectedContactIDs.includes(contact.id)}
                  onChange={(event) => setSelectedContactIDs((current) =>
                    event.target.checked ? [...current, contact.id] : current.filter((id) => id !== contact.id),
                  )}
                />
                <span>{contact.email}</span>
              </label>
            ))}
          </div>
          {contactsQuery.data && contactsQuery.data.pagination.total > 200 ? (
            <div className="flex items-center justify-between text-xs text-slate-500">
              <span>Showing {contactsOffset + 1}–{Math.min(contactsOffset + contactsQuery.data.data.length, contactsQuery.data.pagination.total)} of {contactsQuery.data.pagination.total}</span>
              <div className="flex gap-2">
                <Button variant="secondary" size="sm" disabled={contactsOffset === 0} onClick={() => setContactsOffset(Math.max(0, contactsOffset - 200))}>Previous contacts</Button>
                <Button variant="secondary" size="sm" disabled={contactsOffset + 200 >= contactsQuery.data.pagination.total} onClick={() => setContactsOffset(contactsOffset + 200)}>Next contacts</Button>
              </div>
            </div>
          ) : null}
          <Button onClick={() => void addSelected()} disabled={selectedContactIDs.length === 0}>
            Add selected ({selectedContactIDs.length})
          </Button>
        </CardContent>
      </Card>

      <Card>
        <div className="flex items-center justify-between border-b border-slate-100 px-5 py-4">
          <div>
            <h2 className="text-sm font-semibold">List members</h2>
            <p className="text-xs text-slate-500">Showing {members.length} of {total}</p>
          </div>
          <Users className="h-4 w-4 text-slate-400" />
        </div>
        {membersQuery.error ? (
          <div role="alert" className="flex items-center justify-between px-5 py-4 text-sm text-rose-700">
            <span>{(membersQuery.error as Error).message}</span>
            <Button variant="secondary" onClick={() => void membersQuery.refetch()}>Retry</Button>
          </div>
        ) : (
          <Table>
            <Thead><Tr><Th>Email</Th><Th>Status</Th><Th /></Tr></Thead>
            <Tbody>
              {membersQuery.isLoading ? (
                <Tr><Td colSpan={3} className="py-8 text-center text-slate-500">Loading members...</Td></Tr>
              ) : members.length === 0 ? (
                <Tr><Td colSpan={3} className="py-8 text-center text-slate-500">This list has no members yet.</Td></Tr>
              ) : members.map((member) => (
                <Tr key={member.id}>
                  <Td>{member.email}</Td>
                  <Td>{member.status}</Td>
                  <Td className="text-right">
                    <button className="text-xs font-medium text-rose-600" onClick={() => void removeMember(member)}>Remove</button>
                  </Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        )}
        <div className="flex justify-end gap-2 border-t border-slate-100 p-3">
          <Button variant="secondary" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 20))}>Previous</Button>
          <Button variant="secondary" disabled={offset + members.length >= total} onClick={() => setOffset(offset + 20)}>Next</Button>
        </div>
      </Card>
    </div>
  );
}
