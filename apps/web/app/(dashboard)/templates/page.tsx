"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { LayoutTemplate, MoreHorizontal, Plus, Search } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { apiFetch } from "@/lib/auth";
import { formatDate } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

const categories = ["All", "Newsletter", "Lifecycle", "Promotion", "Events", "General"];

type Template = {
  id: string;
  name: string;
  category: string;
  design: unknown;
  created_at: string;
  updated_at: string;
};

function TemplateThumb({ accent, variant }: { accent: string; variant: string }) {
  return (
    <div className="flex h-44 flex-col gap-2 overflow-hidden rounded-lg border border-slate-200 bg-white p-4">
      <div className="h-2 w-10 rounded-full" style={{ backgroundColor: accent }} />
      {variant === "digest" ? (
        <>
          <div className="h-2.5 w-3/4 rounded bg-slate-800" />
          <div className="h-14 rounded" style={{ backgroundColor: `${accent}22` }} />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="h-1.5 w-5/6 rounded bg-slate-200" />
          <div className="mt-auto h-5 w-20 rounded" style={{ backgroundColor: accent }} />
        </>
      ) : variant === "welcome" ? (
        <>
          <div className="h-2.5 w-2/3 rounded bg-slate-800" />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="h-1.5 w-4/6 rounded bg-slate-200" />
          <div className="mt-2 h-10 rounded" style={{ backgroundColor: `${accent}18` }} />
          <div className="mt-auto h-5 w-24 rounded" style={{ backgroundColor: accent }} />
        </>
      ) : variant === "launch" ? (
        <>
          <div className="h-16 rounded" style={{ backgroundColor: `${accent}25` }} />
          <div className="h-2.5 w-3/4 rounded bg-slate-800" />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="mt-auto flex gap-2">
            <div className="h-5 w-20 rounded" style={{ backgroundColor: accent }} />
            <div className="h-5 w-16 rounded border border-slate-200" />
          </div>
        </>
      ) : (
        <>
          <div className="h-2.5 w-1/2 rounded bg-slate-800" />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="h-1.5 w-11/12 rounded bg-slate-200" />
          <div className="h-1.5 w-4/5 rounded bg-slate-200" />
          <div className="mt-auto h-5 w-20 rounded" style={{ backgroundColor: accent }} />
        </>
      )}
    </div>
  );
}

export default function TemplatesPage() {
  const [search, setSearch] = useState("");
  const { toast } = useToast();

  const templatesQuery = useQuery({
    queryKey: ["templates", search],
    queryFn: async () => apiFetch<Template[]>("/templates"),
  });

  const templates = templatesQuery.data ?? [];

  if (templatesQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Marketing"
          title="Templates"
          description="Loading your templates..."
        />
      </div>
    );
  }

  if (templatesQuery.error) {
    toast({
      title: "Unable to load templates",
      description: templatesQuery.error.message,
      variant: "destructive",
    });
  }

  const filteredTemplates = templates.filter(
    (t) =>
      t.name.toLowerCase().includes(search.toLowerCase()) ||
      t.category.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Templates"
        description="Reusable email designs your whole team can start from."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
            onClick={() => toast({ title: "New template", description: "Template creation coming in Phase 3." })}
          >
            <Plus className="h-4 w-4" /> New template
          </button>
        }
      />

      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap gap-1">
          {categories.map((category, index) => (
            <span
              key={category}
              className={
                index === 0
                  ? "rounded-lg bg-slate-900 px-3 py-1.5 text-xs font-medium text-white"
                  : "rounded-lg px-3 py-1.5 text-xs font-medium text-slate-600 hover:bg-white"
              }
            >
              {category}
            </span>
          ))}
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
          <input
            placeholder="Search templates"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-9 w-full rounded-lg border-0 bg-white pl-9 pr-3 text-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:ring-2 focus:ring-inset focus:ring-indigo-500 sm:w-64"
          />
        </div>
      </div>

      <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        {filteredTemplates.length === 0 ? (
          <div className="col-span-3 flex flex-col items-center justify-center py-12 text-center">
            <LayoutTemplate className="mb-3 h-8 w-8 text-slate-300" />
            <p className="text-sm text-slate-500">No templates found.</p>
          </div>
        ) : (
          filteredTemplates.map((template) => (
            <Card key={template.id}>
              <CardContent className="p-4">
                <TemplateThumb accent="#4f46e5" variant="digest" />
                <div className="mt-3 flex items-start justify-between gap-2">
                  <div>
                    <p className="text-sm font-semibold text-slate-900">{template.name}</p>
                    <p className="mt-0.5 text-xs text-slate-500">
                      Updated {formatDate(template.updated_at)} · {template.category}
                    </p>
                  </div>
                  <button className="rounded-lg p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700" aria-label="Template actions">
                    <MoreHorizontal className="h-4 w-4" />
                  </button>
                </div>
                <div className="mt-3 flex gap-2">
                  <button className="h-8 flex-1 rounded-lg bg-slate-900 text-xs font-medium text-white hover:bg-slate-800">
                    Use template
                  </button>
                  <button className="flex h-8 items-center gap-1 rounded-lg px-2.5 text-xs font-medium text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
                    <LayoutTemplate className="h-3.5 w-3.5" /> Edit
                  </button>
                </div>
              </CardContent>
            </Card>
          ))
        )}

        <button className="flex min-h-[280px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-slate-300 bg-white/60 p-6 text-center hover:border-indigo-300 hover:bg-white">
          <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
            <Plus className="h-5 w-5" />
          </span>
          <span className="text-sm font-medium text-slate-900">Start from scratch</span>
          <span className="max-w-[180px] text-xs text-slate-500">Build a custom template with the drag-and-drop editor.</span>
        </button>
      </div>

      <div className="mt-5 flex items-center justify-between rounded-xl border border-slate-200 bg-white px-5 py-4">
        <div className="flex items-center gap-3">
          <Badge variant="indigo">Tip</Badge>
          <p className="text-sm text-slate-600">
            Templates use the same block format as campaigns, so previews stay consistent.
          </p>
        </div>
      </div>
    </div>
  );
}
