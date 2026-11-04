"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowDownRight, ArrowUpRight, Plus, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch } from "@/lib/auth";
import { formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type ContactList = {
  id: string;
  name: string;
  description: string;
  contact_count: number;
  created_at: string;
  updated_at: string;
};

export default function ListsPage() {
  const [newListName, setNewListName] = useState("");
  const [newListDesc, setNewListDesc] = useState("");
  const { toast } = useToast();

  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: async () => apiFetch<ContactList[]>("/lists"),
  });

  const lists = listsQuery.data ?? [];

  if (listsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Audience"
          title="Lists"
          description="Loading your lists..."
        />
      </div>
    );
  }

  if (listsQuery.error) {
    toast({
      title: "Unable to load lists",
      description: listsQuery.error.message,
      variant: "destructive",
    });
  }

  const handleCreateList = async () => {
    if (!newListName.trim()) return;
    try {
      await apiFetch("/lists", {
        method: "POST",
        body: JSON.stringify({ name: newListName, description: newListDesc }),
      });
      setNewListName("");
      setNewListDesc("");
      void listsQuery.refetch();
      toast({ title: "List created", description: "Your audience list is ready." });
    } catch (err: any) {
      toast({
        title: "Failed to create list",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    }
  };

  return (
    <div>
      <PageHeader
        eyebrow="Audience"
        title="Lists"
        description="Groups of contacts you can target in campaigns and automations."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
            onClick={() => toast({ title: "Create list", description: "Use the form below." })}
          >
            <Plus className="h-4 w-4" /> Create list
          </button>
        }
      />

      <div className="mb-5 flex flex-col gap-3 rounded-xl border border-indigo-200 bg-indigo-50 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-start gap-2 text-sm text-indigo-900">
          <Users className="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            {lists.length} audience list{lists.length !== 1 ? "s" : ""} in your workspace.
          </span>
        </div>
        <button
          className="text-sm font-medium text-indigo-900 underline-offset-2 hover:underline"
          onClick={() => toast({ title: "Create list", description: "Use the form below." })}
        >
          Create new list
        </button>
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {lists.length === 0 ? (
          <div className="col-span-3 flex flex-col items-center justify-center py-12 text-center">
            <Users className="mb-3 h-8 w-8 text-slate-300" />
            <p className="text-sm text-slate-500">No lists yet. Create your first audience list.</p>
          </div>
        ) : (
          lists.map((list) => (
            <Card key={list.id}>
              <CardContent className="p-5">
                <div className="flex items-start justify-between">
                  <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                    <Users className="h-4 w-4" />
                  </span>
                  <Badge variant="success">
                    <ArrowUpRight className="h-3 w-3" />
                    0%
                  </Badge>
                </div>
                <h3 className="mt-3 text-sm font-semibold text-slate-900">{list.name}</h3>
                <p className="mt-0.5 line-clamp-2 text-xs text-slate-500">
                  {list.description || "No description"}
                </p>
                <p className="mt-4 text-2xl font-semibold tracking-tight text-slate-900">
                  {formatNumber(list.contact_count)}
                </p>
                <p className="text-xs text-slate-500">contacts</p>
              </CardContent>
            </Card>
          ))
        )}
      </div>

      <div className="mt-5">
        <Card className="lg:col-span-2">
          <div className="border-b border-slate-100 px-5 py-4">
            <h3 className="text-sm font-semibold text-slate-900">All lists</h3>
          </div>
          <Table>
            <Thead>
              <Tr>
                <Th>List</Th>
                <Th className="text-right">Contacts</Th>
                <Th />
              </Tr>
            </Thead>
            <Tbody>
              {lists.length === 0 ? (
                <Tr>
                  <Td colSpan={3} className="text-center py-8 text-slate-500">
                    No lists found. Create your first audience list.
                  </Td>
                </Tr>
              ) : (
                lists.map((list) => (
                  <Tr key={list.id}>
                    <Td>
                      <p className="font-medium text-slate-900">{list.name}</p>
                      <p className="max-w-sm truncate text-xs text-slate-500">
                        {list.description || "—"}
                      </p>
                    </Td>
                    <Td className="text-right tabular-nums">
                      {formatNumber(list.contact_count)}
                    </Td>
                    <Td>
                      <button className="text-xs font-medium text-indigo-600 hover:text-indigo-500">
                        Manage
                      </button>
                    </Td>
                  </Tr>
                ))
              )}
            </Tbody>
          </Table>
        </Card>
      </div>
    </div>
  );
}
