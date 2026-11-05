"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Tag } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { apiFetch } from "@/lib/auth";
import { formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type ContactTag = {
  id: string;
  name: string;
  color: string;
  contact_count: number;
  created_at: string;
};

export default function TagsPage() {
  const [newTagName, setNewTagName] = useState("");
  const [newTagColor, setNewTagColor] = useState("#4f46e5");
  const { toast } = useToast();

  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: async () => {
      const res = await apiFetch<{ data: ContactTag[] | null }>("/tags");
      return res.data ?? [];
    },
  });

  const tags = tagsQuery.data ?? [];

  if (tagsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Audience"
          title="Tags"
          description="Loading your tags..."
        />
      </div>
    );
  }

  const handleCreateTag = async () => {
    if (!newTagName.trim()) return;
    try {
      await apiFetch("/tags", {
        method: "POST",
        body: JSON.stringify({ name: newTagName, color: newTagColor }),
      });
      setNewTagName("");
      void tagsQuery.refetch();
      toast({ title: "Tag created", description: "Your tag is ready to use." });
    } catch (err: any) {
      toast({
        title: "Failed to create tag",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    }
  };

  const colors = ["#4f46e5", "#0891b2", "#059669", "#d97706", "#db2777", "#7c3aed"];

  return (
    <div>
      <PageHeader
        eyebrow="Audience"
        title="Tags"
        description="Lightweight labels for filtering and personalizing your audience."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
            onClick={() => toast({ title: "New tag", description: "Use the form on the right." })}
          >
            <Plus className="h-4 w-4" /> New tag
          </button>
        }
      />

      {tagsQuery.error ? (
        <div className="mb-4 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
          Unable to load tags: {(tagsQuery.error as Error).message}
        </div>
      ) : null}

      <div className="grid gap-5 lg:grid-cols-3">
        <div className="space-y-3 lg:col-span-2">
          {tags.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12 text-center">
              <Tag className="mb-3 h-8 w-8 text-slate-300" />
              <p className="text-sm text-slate-500">No tags yet. Create your first tag.</p>
            </div>
          ) : (
            tags.map((tag) => (
              <Card key={tag.id}>
                <CardContent className="flex items-center gap-4 p-4">
                  <span
                    className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg"
                    style={{ backgroundColor: `${tag.color}1a`, color: tag.color }}
                  >
                    <Tag className="h-4 w-4" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-semibold text-slate-900">{tag.name}</p>
                    <p className="text-xs text-slate-500">
                      {formatNumber(tag.contact_count)} contacts
                    </p>
                  </div>
                  <div className="h-2 w-24 overflow-hidden rounded-full bg-slate-100">
                    <div
                      className="h-full rounded-full"
                      style={{ backgroundColor: tag.color, width: `${Math.min((tag.contact_count / 100) * 100, 100)}%` }}
                    />
                  </div>
                  <button className="rounded-lg px-3 py-1.5 text-xs font-medium text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
                    Edit
                  </button>
                </CardContent>
              </Card>
            ))
          )}
        </div>

        <Card className="h-fit">
          <CardContent className="space-y-3 p-5">
            <h3 className="text-sm font-semibold text-slate-900">Create a tag</h3>
            <p className="text-xs text-slate-500">
              Tags can be applied manually, through imports, or automatically from forms and the API.
            </p>
            <Input
              placeholder="e.g. webinar-2026"
              value={newTagName}
              onChange={(e) => setNewTagName(e.target.value)}
            />
            <div className="flex flex-wrap gap-1.5">
              {colors.map((color) => (
                <button
                  key={color}
                  onClick={() => setNewTagColor(color)}
                  aria-label={`Use ${color}`}
                  className={`h-6 w-6 rounded-full ring-2 ring-white ring-offset-1 ring-offset-slate-100 ${newTagColor === color ? "ring-offset-2 ring-indigo-500" : ""}`}
                  style={{ backgroundColor: color }}
                />
              ))}
            </div>
            <button
              onClick={handleCreateTag}
              className="inline-flex h-9 w-full items-center justify-center rounded-lg bg-indigo-600 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
              disabled={!newTagName.trim()}
            >
              Create tag
            </button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
