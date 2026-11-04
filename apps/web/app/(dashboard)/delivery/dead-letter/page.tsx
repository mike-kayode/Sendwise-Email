"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { RefreshCw, RotateCcw } from "lucide-react";
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
import { apiFetch, type DeadLetterItem } from "@/lib/auth";
import { formatDateTime } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

export default function DeadLetterPage() {
  const [offset, setOffset] = useState(0);
  const { toast } = useToast();

  const query = useQuery({
    queryKey: ["dead-letter", offset],
    queryFn: async () =>
      apiFetch<{ data: DeadLetterItem[] }>(
        `/delivery/dead-letter?offset=${offset}&limit=50`
      ),
  });

  const items = query.data?.data ?? [];

  const handleReplay = async (id: string) => {
    try {
      await apiFetch(`/delivery/dead-letter/${id}/replay`, {
        method: "POST",
      });
      void query.refetch();
      toast({
        title: "Replayed",
        description: "Delivery job re-queued for retry.",
      });
    } catch (err: any) {
      toast({
        title: "Replay failed",
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
          title="Dead Letter Queue"
          description="Loading failed deliveries..."
        />
      </div>
    );
  }

  if (query.error) {
    toast({
      title: "Unable to load dead letter queue",
      description: query.error.message,
      variant: "destructive",
    });
  }

  return (
    <div>
      <PageHeader
        eyebrow="Deliverability"
        title="Dead Letter Queue"
        description="Delivery jobs that exhausted retries or exceeded maximum message age."
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
            <CardTitle>Failed deliveries</CardTitle>
            <CardDescription>
              Jobs that could not be delivered after all retry attempts.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table className="min-w-full">
              <Thead>
                <Tr>
                  <Th>Contact</Th>
                  <Th>Campaign</Th>
                  <Th>Reason</Th>
                  <Th>Attempts</Th>
                  <Th>Last error</Th>
                  <Th>Occurred</Th>
                  <Th className="text-right" />
                </Tr>
              </Thead>
              <Tbody>
                {items.length === 0 ? (
                  <Tr>
                    <Td colSpan={7} className="text-center py-8 text-slate-500">
                      No dead-lettered jobs. All deliveries are succeeding.
                    </Td>
                  </Tr>
                ) : (
                  items.map((item) => (
                    <Tr key={item.id}>
                      <Td className="font-medium text-slate-900">
                        {item.contact_id}
                      </Td>
                      <Td className="text-sm text-slate-600">
                        {item.campaign_id}
                      </Td>
                      <Td className="text-sm text-slate-600 capitalize">
                        {item.reason.replace(/_/g, " ")}
                      </Td>
                      <Td className="text-right tabular-nums text-sm">
                        {item.attempts}
                      </Td>
                      <Td className="text-sm text-slate-500 truncate max-w-xs">
                        {item.last_error}
                      </Td>
                      <Td className="text-sm text-slate-500">
                        {formatDateTime(item.created_at)}
                      </Td>
                      <Td className="text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleReplay(item.id)}
                          disabled={item.replayed_at != null}
                        >
                          {item.replayed_at ? (
                            <span className="text-xs">Replayed</span>
                          ) : (
                            <>
                              <RotateCcw className="mr-1 h-3 w-3" /> Replay
                            </>
                          )}
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
          <span>Showing {items.length} dead-lettered job(s)</span>
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