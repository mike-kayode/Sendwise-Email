"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Filter, Plus } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { apiFetch } from "@/lib/auth";
import { formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type Segment = {
  id: string;
  name: string;
  description: string;
  rules: string;
  contact_count: number;
  created_at: string;
};

export default function SegmentsPage() {
  const [newSegmentName, setNewSegmentName] = useState("");
  const { toast } = useToast();

  const segmentsQuery = useQuery({
    queryKey: ["segments"],
    queryFn: async () => {
      const res = await apiFetch<{ data: Segment[] | null }>("/segments");
      return res.data ?? [];
    },
  });

  const segments = segmentsQuery.data ?? [];

  if (segmentsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Audience"
          title="Segments"
          description="Loading your segments..."
        />
      </div>
    );
  }

  const handleCreateSegment = async () => {
    if (!newSegmentName.trim()) return;
    try {
      await apiFetch("/segments", {
        method: "POST",
        body: JSON.stringify({ name: newSegmentName, rules: [] }),
      });
      setNewSegmentName("");
      void segmentsQuery.refetch();
      toast({ title: "Segment created", description: "Your segment is ready." });
    } catch (err: any) {
      toast({
        title: "Failed to create segment",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    }
  };

  return (
    <div>
      <PageHeader
        eyebrow="Audience"
        title="Segments"
        description="Dynamic groups that update automatically as contact behavior changes."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
            onClick={() => toast({ title: "New segment", description: "Use the segment builder on the right." })}
          >
            <Plus className="h-4 w-4" /> New segment
          </button>
        }
      />

      {segmentsQuery.error ? (
        <div className="mb-4 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
          Unable to load segments: {(segmentsQuery.error as Error).message}
        </div>
      ) : null}

      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <div className="border-b border-slate-100 px-5 py-4">
            <h3 className="text-sm font-semibold text-slate-900">Saved segments</h3>
            <p className="text-xs text-slate-500">Evaluated when a campaign or automation runs.</p>
          </div>
          <Table>
            <Thead>
              <Tr>
                <Th>Segment</Th>
                <Th>Rules</Th>
                <Th className="text-right">Contacts</Th>
                <Th />
              </Tr>
            </Thead>
            <Tbody>
              {segments.length === 0 ? (
                <Tr>
                  <Td colSpan={4} className="text-center py-8 text-slate-500">
                    No segments yet. Create your first segment.
                  </Td>
                </Tr>
              ) : (
                segments.map((segment) => (
                  <Tr key={segment.id}>
                    <Td>
                      <div className="flex items-center gap-2.5">
                        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                          <Filter className="h-3.5 w-3.5" />
                        </span>
                        <div>
                          <p className="font-medium text-slate-900">{segment.name}</p>
                          <p className="text-xs text-slate-500">{segment.description}</p>
                        </div>
                      </div>
                    </Td>
                    <Td className="max-w-[220px] truncate text-xs text-slate-600">{segment.rules}</Td>
                    <Td className="text-right tabular-nums">{formatNumber(segment.contact_count)}</Td>
                    <Td>
                      <button className="text-xs font-medium text-indigo-600 hover:text-indigo-500">Edit</button>
                    </Td>
                  </Tr>
                ))
              )}
            </Tbody>
          </Table>
        </Card>

        <Card className="h-fit">
          <CardContent className="space-y-3 p-5">
            <h3 className="text-sm font-semibold text-slate-900">Create a segment</h3>
            <p className="text-xs text-slate-500">
              Build a rule now and refine it later. Rules use AND logic by default.
            </p>
            <Input
              placeholder="e.g. Highly engaged"
              value={newSegmentName}
              onChange={(e) => setNewSegmentName(e.target.value)}
            />
            <div className="space-y-2 rounded-lg bg-slate-50 p-3 text-sm">
              <Select defaultValue="opened">
                <option value="opened">Opened any campaign</option>
                <option value="clicked">Clicked any campaign</option>
                <option value="joined">Joined a list</option>
                <option value="tag">Has a tag</option>
              </Select>
              <Select defaultValue="30">
                <option value="7">In the last 7 days</option>
                <option value="30">In the last 30 days</option>
                <option value="90">In the last 90 days</option>
              </Select>
              <Button variant="secondary" className="w-full">
                + Add condition
              </Button>
            </div>
            <Button
              onClick={handleCreateSegment}
              className="w-full"
              disabled={!newSegmentName.trim()}
            >
              Create segment
            </Button>
            <p className="text-center text-xs text-slate-400">Estimated 0 contacts</p>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
