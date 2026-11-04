"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { apiFetch, type SuppressionItem } from "@/lib/auth";
import { formatDateTime } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

export default function SuppressionsPage() {
  const [offset, setOffset] = useState(0);
  const { toast } = useToast();

  const query = useQuery({
    queryKey: ["suppressions", offset],
    queryFn: async () =>
      apiFetch<{ data: SuppressionItem[] }>(
        `/suppressions?offset=${offset}&limit=50`
      ),
  });

  const items = query.data?.data ?? [];

  const handleRemove = async (email: string) => {
    if (!window.confirm(`Remove suppression for ${email}?`)) return;
    try {
      await apiFetch(`/suppressions/${encodeURIComponent(email)}`, {
        method: "DELETE",
      });
      void query.refetch();
      toast({ title: "Removed", description: "Address removed from suppression list." });
    } catch (err: any) {
      toast({
        title: "Failed to remove",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    }
  };

  if (query.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Deliverability"
          title="Suppressions"
          description="Loading suppressed addresses..."
        />
      </div>
    );
  }

  if (query.error) {
    toast({
      title: "Unable to load suppressions",
      description: query.error.message,
      variant: "destructive",
    });
  }

  return (
    <div>
      <PageHeader
        eyebrow="Deliverability"
        title="Suppressions"
        description="Addresses that will never receive email from your workspace."
        actions={
          <Button onClick={() => void query.refetch()}>
            <RefreshCw className="mr-2 h-4 w-4" />
            Refresh
          </Button>
        }
      />

      <div className="space-y-5">
        <Card>
          <CardHeader>
            <CardTitle>Suppressed addresses</CardTitle>
            <CardDescription>
              These contacts are excluded from all campaign sends.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table className="min-w-full">
              <Thead>
                <Tr>
                  <Th>Email</Th>
                  <Th>Reason</Th>
                  <Th>Source</Th>
                  <Th>Added</Th>
                  <Th className="text-right" />
                </Tr>
              </Thead>
              <Tbody>
                {items.length === 0 ? (
                  <Tr>
                    <Td colSpan={5} className="text-center py-8 text-slate-500">
                      No suppressed addresses.
                    </Td>
                  </Tr>
                ) : (
                  items.map((item) => (
                    <Tr key={item.email}>
                      <Td className="font-medium text-slate-900">
                        {item.email}
                      </Td>
                      <Td className="text-sm text-slate-600 capitalize">
                        {item.reason.replace(/_/g, " ")}
                      </Td>
                      <Td className="text-sm text-slate-600 capitalize">
                        {item.source}
                      </Td>
                      <Td className="text-sm text-slate-500">
                        {formatDateTime(item.created_at)}
                      </Td>
                      <Td className="text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleRemove(item.email)}
                        >
                          <Trash2 className="mr-1 h-3 w-3" /> Remove
                        </Button>
                      </Td>
                    </Tr>
                  ))
                )}
              </Tbody>
            </Table>
          </CardContent>
        </Card>

        <div className="flex items-center justify-between border-t border-slate-100 px-4 py-3 text-xs text-slate-500">
          <span>Showing {items.length} suppressed address(es)</span>
          <div className="flex gap-1">
            <button
              className={`rounded-md px-2.5 py-1 ring-1 ring-inset ${
                offset === 0 ? "disabled:opacity-40" : "hover:bg-slate-50"
              }`}
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - 50))}
            >
              Previous
            </button>
            <button
              className={`rounded-md px-2.5 py-1 ring-1 ring-inset ${
                items.length < 50 ? "disabled:opacity-40" : "hover:bg-slate-50"
              }`}
              disabled={items.length < 50}
              onClick={() => setOffset(offset + 50)}
            >
              Next
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}